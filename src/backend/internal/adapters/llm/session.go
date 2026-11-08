package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"moodle-autograder/internal/adapters/ollama"
	"moodle-autograder/internal/app"
	"moodle-autograder/internal/app/assessing"
	"moodle-autograder/internal/domain/assessment"
	"moodle-autograder/internal/domain/work"
)

// Client runs the steps of the AI check on the model server.
type Client struct{ llm *ollama.Client }

func New(llm *ollama.Client) *Client { return &Client{llm: llm} }

// Session is the AI check with the prompts of a course (its edits over the defaults); models replace the course's
// model for some steps, for an experiment.
func (c *Client) Session(overrides, models map[string]string) assessing.Assessor {
	course := With(overrides)
	return &Session{llm: c.llm, course: course, run: course.WithModels(models)}
}

type Session struct {
	llm         *ollama.Client
	course, run Prompts
}

// Models and StepModels are what this run uses: the course's models, or an experiment's.
func (s *Session) Models() string                { return s.run.Models() }
func (s *Session) StepModels() map[string]string { return s.run.StepModels() }
func (s *Session) ChecklistModel() string        { return s.course.Model("rubric") }

// ChecklistPrompt is the checklist step's templates: editing them builds checklists anew.
func (s *Session) ChecklistPrompt() string {
	return s.course["rubric.system"] + s.course["rubric.user"]
}

func (s *Session) model(step string) string { return s.run.Model(step) }

// permanent marks a template error: the teacher has to fix the prompt, retrying does not help.
func permanent(err error) error { return fmt.Errorf("%w: %w", app.ErrPermanent, err) }

// BuildChecklist asks for a checklist from the assignment description (teacher-written, trusted), always on the
// course's model. The criteria come as the model wrote them: the use case scales the points and numbers them.
func (s *Session) BuildChecklist(ctx context.Context, name, intro, notes string, maxPoints float64) ([]assessment.Criterion, []assessment.Criterion, error) {
	msgs, err := s.course.RubricPrompt(name, intro, notes, maxPoints)
	if err != nil {
		return nil, nil, permanent(err)
	}

	var reply struct{ Criteria, Extras []assessment.Criterion }
	if _, err := s.llm.JSON(ctx, s.course.Model("rubric"), msgs, RubricSchema, &reply); err != nil {
		return nil, nil, err
	}

	return reply.Criteria, reply.Extras, nil
}

// ReadPage writes down what is on one screenshot or PDF page.
func (s *Session) ReadPage(ctx context.Context, source string, image []byte) (assessment.PageNote, error) {
	var note assessment.PageNote
	msgs, err := s.run.PagePrompt(source)
	if err != nil {
		return note, permanent(err)
	}

	msgs[len(msgs)-1].Images = [][]byte{image}
	if _, err := s.llm.JSON(ctx, s.model("page"), msgs, PageSchema, &note); err != nil {
		return note, err
	}

	note.Source = source
	return note, nil
}

// FindInjection asks whether the student's text addresses the grader; the alarm still needs its quotes verified. It reads
// the same text as Check, with less of it when the work does not fit into the model context.
func (s *Session) FindInjection(ctx context.Context, snap work.Snapshot, pages []assessment.PageNote, files []string) (assessment.Injection, error) {
	var inj assessment.Injection
	for share := 1.0; ; share /= 2 {
		msgs, err := s.run.InjectionPrompt(PrepareInputShare(snap, pages, files, share))
		if err != nil {
			return inj, permanent(err)
		}
		_, err = s.llm.JSON(ctx, s.model("injection"), msgs, InjectionSchema, &inj)
		if !errors.Is(err, ollama.ErrContextFull) || share < 0.2 {
			return inj, err
		}
	}
}

// Check asks for the verdicts on the checklist. A work too large for the model context is checked with less code and
// report instead of failing; verdicts without quotes are asked about once more.
func (s *Session) Check(ctx context.Context, req assessing.CheckRequest, step func(string)) (assessing.CheckResult, error) {
	input := PrepareInput(req.Snapshot, req.Pages, req.Files)
	out := assessing.CheckResult{Truncated: input.Truncated}
	schema := CheckSchema(req.Criteria, req.Extras)
	var reply json.RawMessage
	var msgs []ollama.Message
	var err error
	for share := 1.0; ; share /= 2 {
		if share < 1 {
			input = PrepareInputShare(req.Snapshot, req.Pages, req.Files, share)
			step(fmt.Sprintf("checking against the checklist (work too large: %d%% of it)", int(share*100)))
		}
		if msgs, err = s.run.CheckPrompt(req.Assignment, req.Intro, req.Notes, req.Student, req.PreviousComment, req.Criteria, req.Extras, input); err != nil {
			return out, permanent(err)
		}
		_, err = s.llm.JSON(ctx, s.model("check"), msgs, schema, &reply)
		if !errors.Is(err, ollama.ErrContextFull) || share < 0.2 {
			break
		}
		out.Truncated = true
	}

	if err != nil {
		return out, err
	}

	if missing := Unquoted(reply); len(missing) > 0 {
		// One more question: a "met" without a quote is marked for the teacher.
		step("asking for the missing quotes")
		msgs = append(msgs, ollama.Message{Role: "assistant", Content: string(reply)}, ollama.Message{Role: "user", Content: fmt.Sprintf(
			"You marked %s as done without quotes. Answer the whole checklist again: give exact quotes from the work for every \"met\" and \"partial\", or mark it \"not_met\".",
			strings.Join(missing, ", "))})
		var again json.RawMessage
		if _, err := s.llm.JSON(ctx, s.model("check"), msgs, schema, &again); err == nil {
			reply = again
		}
	}

	verdicts, err := ParseCheck(reply)
	if err != nil {
		return out, err
	}

	out.Criteria, out.Extras, out.Work = verdicts.Criteria, verdicts.Extras, input
	return out, nil
}

// WriteComment writes the comment for the student from the checklist results only.
func (s *Session) WriteComment(ctx context.Context, assignment string, results []assessment.CriterionResult, previous string) (string, error) {
	msgs, err := s.run.CommentPrompt(assignment, results, previous)
	if err != nil {
		return "", permanent(err)
	}

	var c struct{ Comment string }
	if _, err := s.llm.JSON(ctx, s.model("comment"), msgs, CommentSchema, &c); err != nil {
		return "", err
	}

	return c.Comment, nil
}
