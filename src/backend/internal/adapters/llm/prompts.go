// Package llm is the AI check's side that talks to the language model: prompt templates (files and the teacher's edits),
// JSON schemas of the answers, the work as text for the model, and reading the answers. The rules that turn answers
// into points are in domain/assessment. Student text is always wrapped as data with a random boundary; code goes as
// written, comments too, and the injection check reads the same text.
package llm

import (
	"bytes"
	"embed"
	"errors"
	"fmt"
	"maps"
	"strings"
	"text/template"

	"moodle-autograder/internal/adapters/ollama"
	"moodle-autograder/internal/app/assessing"
	"moodle-autograder/internal/domain/assessment"
	"moodle-autograder/internal/domain/work"
)

// Prompts are text templates, not code: defaults ship as files in prompts/, the teacher's edits are stored in the
// database and replace them. Code only fills the variables, wraps student data and checks the answers.
//
//go:embed prompts/*.md
var defaults embed.FS

// Steps are the model calls of the AI check, in order.
var Steps = []assessing.PromptStep{
	{Key: "rubric", Title: "Checklist from the assignment description", Vars: []string{"Assignment", "MaxPoints", "Description", "TeacherNotes"}, Required: []string{"Description"}},
	{Key: "page", Title: "Reading a screenshot or a PDF page (the image is attached)", Vars: []string{"Source"}, Required: nil},
	{Key: "injection", Title: "Prompt injection check", Vars: []string{"Work"}, Required: []string{"Work"}},
	{Key: "check", Title: "Checking the work against the checklist", Vars: []string{"Assignment", "Description", "Checklist", "TeacherNotes", "Student", "PreviousComment", "Work"}, Required: []string{"Checklist", "Work"}},
	{Key: "comment", Title: "Comment for the student (sees only the checklist results)", Vars: []string{"Assignment", "Results", "PreviousComment"}, Required: []string{"Results"}},
}

type Prompts map[string]string // "check.user" -> template, "check.model" -> Ollama model name

// Defaults are the templates that ship with the program. No model is a default: each course chooses from what the
// installation downloaded.
func Defaults() Prompts {
	p := Prompts{}
	for _, s := range Steps {
		for _, part := range []string{".system", ".user"} {
			b, err := defaults.ReadFile("prompts/" + s.Key + part + ".md")
			if err != nil {
				panic(fmt.Sprintf("missing default prompt %s%s: %v", s.Key, part, err)) // a build error, caught by tests
			}
			p[s.Key+part] = string(b)
		}
		p[s.Key+".model"] = ""
	}

	return p
}

// With returns the defaults with the teacher's edits applied.
func With(overrides map[string]string) Prompts {
	p := Defaults()
	for k, v := range overrides {
		if _, ok := p[k]; ok {
			p[k] = v
		}
	}

	return p
}

// Validate parses a template and checks that the user template uses the required variables.
func Validate(key, body string) error {
	step, part, ok := strings.Cut(key, ".")
	var s *assessing.PromptStep
	for i := range Steps {
		if Steps[i].Key == step {
			s = &Steps[i]
		}
	}

	if !ok || s == nil || (part != "system" && part != "user" && part != "model") {
		return fmt.Errorf("unknown prompt %q", key)
	}

	if part == "model" {
		if strings.TrimSpace(body) == "" || strings.ContainsAny(body, " \n\t") {
			return errors.New("a model name as in `ollama list` is expected")
		}
		return nil
	}

	if strings.TrimSpace(body) == "" {
		return errors.New("the prompt is empty")
	}

	t, err := template.New(key).Option("missingkey=error").Parse(body)
	if err != nil {
		return fmt.Errorf("template error: %w", err)
	}

	sample := map[string]any{}
	for _, v := range s.Vars {
		sample[v] = "x"
	}

	if err := t.Execute(&bytes.Buffer{}, sample); err != nil {
		return fmt.Errorf("template error (available: %s): %w", strings.Join(s.Vars, ", "), err)
	}

	if part == "user" {
		for _, v := range s.Required {
			if !strings.Contains(body, "."+v) {
				return fmt.Errorf("the prompt must use {{.%s}}", v)
			}
		}
	}

	return nil
}

// Model returns the model a step runs on.
func (p Prompts) Model(step string) string { return p[step+".model"] }

// Models lists the distinct models of all steps, for the analysis record.
func (p Prompts) Models() string {
	seen := map[string]bool{}
	var out []string
	for _, s := range Steps {
		if m := p.Model(s.Key); !seen[m] {
			seen[m] = true
			out = append(out, m)
		}
	}

	return strings.Join(out, ", ")
}

// StepModels maps every step to its model, for the run record.
func (p Prompts) StepModels() map[string]string {
	out := map[string]string{}
	for _, s := range Steps {
		out[s.Key] = p.Model(s.Key)
	}

	return out
}

// WithModels returns a copy that runs some steps on other models (an experiment); unknown steps and empty names are ignored.
func (p Prompts) WithModels(models map[string]string) Prompts {
	out := Prompts{}
	maps.Copy(out, p)
	for step, m := range models {
		if _, ok := p[step+".model"]; ok && strings.TrimSpace(m) != "" {
			out[step+".model"] = strings.TrimSpace(m)
		}
	}

	return out
}

func (p Prompts) messages(step string, data map[string]any) ([]ollama.Message, error) {
	var out []ollama.Message
	for _, part := range []string{"system", "user"} {
		t, err := template.New(step).Option("missingkey=zero").Parse(p[step+"."+part])
		if err != nil {
			return nil, fmt.Errorf("prompt %s.%s: %w", step, part, err)
		}
		var b bytes.Buffer
		if err := t.Execute(&b, data); err != nil {
			return nil, fmt.Errorf("prompt %s.%s: %w", step, part, err)
		}
		out = append(out, ollama.Message{Role: part, Content: strings.TrimSpace(b.String())})
	}

	return out, nil
}

// Preview renders every step for an assignment as the model would get it, without any student work.
func (p Prompts) Preview(name, intro, notes string, maxPoints float64, criteria, extras []assessment.Criterion) ([]assessing.PreviewStep, error) {
	work := "<<<STUDENT-DATA (random boundary)>>>\n[the student's work is inserted here]\n<<<END STUDENT-DATA (random boundary)>>>"
	data := map[string]map[string]any{
		"rubric":    {"Assignment": name, "MaxPoints": fmt.Sprint(maxPoints), "Description": plain(intro), "TeacherNotes": notes},
		"page":      {"Source": "report.pdf p.2"},
		"injection": {"Work": work},
		"check": {
			"Assignment": name, "Description": plain(intro), "TeacherNotes": notes, "Checklist": ChecklistText(criteria, extras), "Work": work,
			"Student": "Student: (full name and group from Moodle).", "PreviousComment": "(the comment you published for the previous upload, if any)",
		},
		"comment": {"Assignment": name, "Results": "- [met] … - …\n- [not_met] … - …", "PreviousComment": "(the comment you published for the previous upload, if any)"},
	}
	var out []assessing.PreviewStep
	for _, s := range Steps {
		m, err := p.messages(s.Key, data[s.Key])
		if err != nil {
			return nil, err
		}
		out = append(out, assessing.PreviewStep{Key: s.Key, Title: s.Title, System: m[0].Content, User: m[1].Content})
	}

	return out, nil
}

// Catalog is the prompt editor's side of this package (assessing.Prompts).
type Catalog struct{}

func (Catalog) Steps() []assessing.PromptStep { return Steps }
func (Catalog) Defaults() map[string]string   { return Defaults() }
func (Catalog) Effective(overrides map[string]string) map[string]string {
	return With(overrides)
}
func (Catalog) Validate(key, body string) error { return Validate(key, body) }
func (Catalog) Preview(overrides map[string]string, a work.Assignment, criteria, extras []assessment.Criterion) ([]assessing.PreviewStep, error) {
	maxPoints := 0.0
	if a.MaxGrade != nil {
		maxPoints = *a.MaxGrade
	}

	return With(overrides).Preview(a.Name, a.Intro, a.Notes, maxPoints, criteria, extras)
}

var _ assessing.Prompts = Catalog{}
