package assessing

import (
	"context"
	"slices"
	"strings"

	"moodle-autograder/internal/app"
)

// PromptPart is one template (or the model name) of a step: the effective text, the default and who edited it.
type PromptPart struct {
	Body    string      `json:"body"`
	Default string      `json:"default"`
	Edited  *PromptEdit `json:"edited"`
}

// PromptPage is one step in the prompt editor.
type PromptPage struct {
	PromptStep
	System PromptPart `json:"system"`
	User   PromptPart `json:"user"`
	Model  PromptPart `json:"model"`
}

// PromptPages lists every step with its effective templates for the course.
func (s *Service) PromptPages(ctx context.Context, courseID int64) ([]PromptPage, error) {
	over, err := s.Store.PromptOverrides(ctx, courseID)
	if err != nil {
		return nil, err
	}

	edits, err := s.Store.PromptEdits(ctx, courseID)
	if err != nil {
		return nil, err
	}

	def, eff := s.Prompts.Defaults(), s.Prompts.Effective(over)
	part := func(k string) PromptPart {
		p := PromptPart{Body: eff[k], Default: def[k]}
		if e, ok := edits[k]; ok {
			p.Edited = &e
		}
		return p
	}
	var out []PromptPage
	for _, st := range s.Prompts.Steps() {
		out = append(out, PromptPage{PromptStep: st, System: part(st.Key + ".system"), User: part(st.Key + ".user"), Model: part(st.Key + ".model")})
	}

	return out, nil
}

// SavePrompt validates and stores a template of the course; an empty body restores the default.
func (s *Service) SavePrompt(ctx context.Context, userID, courseID int64, key, body string) error {
	if len(body) > 20000 {
		return app.Fail(app.ErrInvalid, "the prompt is longer than 20000 characters")
	}

	if _, ok := s.Prompts.Defaults()[key]; !ok {
		return app.Fail(app.ErrNotFound, "unknown prompt")
	}

	if body != "" {
		if err := s.Prompts.Validate(key, body); err != nil {
			return app.Fail(app.ErrInvalid, "%s", err.Error())
		}
		if strings.HasSuffix(key, ".model") && s.Models != nil {
			models, err := s.Models.Models(ctx)
			if err == nil && !slices.Contains(models, body) {
				return app.Fail(app.ErrInvalid, "model %s is not downloaded (docker compose run --rm ollama-cli pull %s); available: %s", body, body, strings.Join(models, ", "))
			}
		}
	}

	return s.Store.SetPrompt(ctx, courseID, key, body, userID)
}

// PromptPreview renders the prompts of an assignment as the model gets them, with a placeholder for the student's work.
func (s *Service) PromptPreview(ctx context.Context, assignmentID int64) ([]PreviewStep, error) {
	t, err := s.Store.AssignmentText(ctx, assignmentID)
	if err != nil {
		return nil, err
	}

	over, err := s.Store.PromptOverrides(ctx, t.CourseID)
	if err != nil {
		return nil, err
	}

	r, _, err := s.Store.LatestChecklist(ctx, assignmentID)
	if err != nil {
		return nil, err
	}

	steps, err := s.Prompts.Preview(over, t, r.Criteria, r.Extras)
	if err != nil {
		return nil, app.Fail(app.ErrInvalid, "%s", err.Error())
	}

	return steps, nil
}

// ModelList lists the models on the model server; empty when the AI check is off.
func (s *Service) ModelList(ctx context.Context) ([]string, error) {
	if s.Models == nil {
		return []string{}, nil
	}

	list, err := s.Models.Models(ctx)
	if err != nil {
		return nil, app.Fail(app.ErrUnavailable, "the model server is not reachable")
	}

	return list, nil
}

// modelsProblem says, in words for the teacher, why the AI check of an assignment cannot run: it is off in the course's
// settings or for the assignment, or the course's models with the given ones (step -> model) over them are not chosen
// or not downloaded; "" when it can.
func (s *Service) modelsProblem(ctx context.Context, courseID, assignmentID int64, models map[string]string) (string, error) {
	settings, err := s.Store.CourseSettings(ctx, courseID)
	if err != nil {
		return "", err
	}

	if !settings.AICheck {
		return `The AI check is off in this course: turn on "AI check" in the course settings.`, nil
	}

	on, err := s.Store.AssignmentAICheck(ctx, assignmentID)
	if err != nil {
		return "", err
	}

	if !on {
		return `The AI check is off for this assignment: turn it on in the "AI check" column of the course's assignments.`, nil
	}

	over, err := s.Store.PromptOverrides(ctx, courseID)
	if err != nil {
		return "", err
	}

	installed, err := s.Models.Models(ctx)
	if err != nil {
		return "", app.Fail(app.ErrUnavailable, "the model server is not reachable")
	}

	course := s.Prompts.Effective(over)
	var unset, absent []string
	for _, st := range s.Prompts.Steps() {
		m := models[st.Key]
		if m == "" {
			m = course[st.Key+".model"]
		}
		switch {
		case m == "":
			unset = append(unset, st.Key)
		case !slices.Contains(installed, m) && !slices.Contains(absent, m):
			absent = append(absent, m)
		}
	}

	switch {
	case len(absent) > 0:
		return "Not downloaded: " + strings.Join(absent, ", ") + ". Download with: docker compose run --rm ollama-cli pull <model>", nil
	case len(unset) > 0:
		return "Choose the models of the AI check on the course's “AI prompts” tab; no model yet for: " + strings.Join(unset, ", "), nil
	}

	return "", nil
}

// requireModels refuses to start a check that cannot run on its models. With the AI check off a request for the
// course's models does nothing, as before.
func (s *Service) requireModels(ctx context.Context, courseID, assignmentID int64, models map[string]string) error {
	if s.Models == nil {
		if len(models) > 0 {
			return app.Fail(app.ErrUnavailable, "the AI check is off")
		}
		return nil
	}

	problem, err := s.modelsProblem(ctx, courseID, assignmentID, models)
	if err != nil {
		return err
	}

	if problem != "" {
		return app.Fail(app.ErrInvalid, "%s", problem)
	}

	return nil
}
