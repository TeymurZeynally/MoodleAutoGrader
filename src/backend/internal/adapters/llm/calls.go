package llm

import (
	"fmt"
	"strings"

	"moodle-autograder/internal/adapters/ollama"
	"moodle-autograder/internal/domain/assessment"
)

// RubricPrompt asks for a checklist from the assignment description (teacher-written, trusted).
func (p Prompts) RubricPrompt(name, intro, notes string, maxPoints float64) ([]ollama.Message, error) {
	return p.messages("rubric", map[string]any{"Assignment": name, "MaxPoints": fmt.Sprint(maxPoints), "Description": plain(intro), "TeacherNotes": notes})
}

func (p Prompts) PagePrompt(source string) ([]ollama.Message, error) {
	return p.messages("page", map[string]any{"Source": wrap(nonce(), source)}) // a file name, chosen by the student
}

// InjectionPrompt asks whether the student's text addresses the grader. It gets exactly what the checklist step gets:
// whatever the filter does not read could speak to the grader unchecked.
func (p Prompts) InjectionPrompt(in assessment.Work) ([]ollama.Message, error) {
	return p.messages("injection", map[string]any{"Work": wrap(nonce(), WorkText(in))})
}

func (p Prompts) CheckPrompt(name, intro, notes string, st assessment.Student, previous string, criteria, extras []assessment.Criterion, in assessment.Work) ([]ollama.Message, error) {
	return p.messages("check", map[string]any{
		"Assignment": name, "Description": clip(plain(intro), 20000), "TeacherNotes": notes,
		"Student": StudentText(st), "PreviousComment": previous, "Checklist": ChecklistText(criteria, extras), "Work": wrap(nonce(), WorkText(in)),
	})
}

// CommentPrompt writes feedback from the checklist results only: the student's own text is not in this call.
func (p Prompts) CommentPrompt(name string, results []assessment.CriterionResult, previous string) ([]ollama.Message, error) {
	var b strings.Builder
	for _, r := range results {
		fmt.Fprintf(&b, "- [%s] %s: %s\n", r.Status, r.Text, r.Note)
	}

	return p.messages("comment", map[string]any{"Assignment": name, "Results": strings.TrimSpace(b.String()), "PreviousComment": previous})
}
