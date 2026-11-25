package assessing

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"moodle-autograder/internal/app"
	"moodle-autograder/internal/domain/assessment"
	"moodle-autograder/internal/domain/course"
	"moodle-autograder/internal/domain/work"
)

func isPermanent(err error) bool { return errors.Is(err, app.ErrPermanent) }

// ChecklistApproved reports whether the assignment has a checklist approved by the teacher.
func (s *Service) ChecklistApproved(ctx context.Context, assignmentID int64) (bool, error) {
	r, ok, err := s.Store.LatestChecklist(ctx, assignmentID)
	return ok && r.Approved, err
}

// EnsureChecklist returns the checklist for the current assignment description, building it when the description
// changed or when asked to. 0 means the assignment cannot be checked automatically.
func (s *Service) EnsureChecklist(ctx context.Context, assignmentID int64, rebuild, experiment bool) (int64, error) {
	if !s.Enabled() {
		return 0, nil
	}

	t, err := s.Store.AssignmentText(ctx, assignmentID)
	if err != nil {
		return 0, err
	}

	defer func() { _ = s.Store.ClearActivity(context.WithoutCancel(ctx)) }()
	if t.MaxGrade == nil || strings.TrimSpace(t.Intro) == "" {
		return 0, nil // a Moodle scale or no description: the teacher grades
	}

	over, err := s.Store.PromptOverrides(ctx, t.CourseID)
	if err != nil {
		return 0, err
	}

	settings, err := s.Store.CourseSettings(ctx, t.CourseID)
	if err != nil {
		return 0, err
	}

	m := s.Assessors(over, nil)
	sum := sha256.Sum256([]byte(t.Name + "\n" + t.Intro + fmt.Sprint(*t.MaxGrade) + "\n" + t.Notes + "\n" + m.ChecklistPrompt()))
	introHash := hex.EncodeToString(sum[:])
	r, ok, err := s.Store.LatestChecklist(ctx, assignmentID)
	if err != nil {
		return 0, err
	}

	// The teacher's checklist is kept until they ask for a new one; a generated one follows the description. An
	// experiment compares models on the checklist in use.
	if ok && (experiment || (!rebuild && (r.Source == "teacher" || (r.IntroHash == introHash && r.Model == m.ChecklistModel())))) {
		return r.ID, nil
	}

	// No model to build it with yet: a sync goes on, and a request of the teacher was refused before it got here.
	if problem, err := s.modelsProblem(ctx, t.CourseID, assignmentID, nil); err != nil || problem != "" {
		return 0, err
	}

	if err := s.Store.SetActivity(ctx, "checklist", assignmentID, 0, "building the checklist"); err != nil {
		return 0, err
	}

	criteria, extras, err := m.BuildChecklist(ctx, t.Name, t.Intro, t.Notes, *t.MaxGrade)
	if err != nil {
		return 0, err
	}

	criteria, extras = assessment.NormalizeRubric(criteria, *t.MaxGrade, settings.PointsStep), assessment.NormalizeExtras(extras)
	if len(criteria) == 0 {
		return 0, fmt.Errorf("the model returned an empty checklist: %w", app.ErrPermanent)
	}

	return s.Store.SaveChecklist(ctx, assignmentID, introHash, "model", m.ChecklistModel(), nil, criteria, extras)
}

// ChecklistPage is what the checklist editor shows, with how far the check of the whole assignment got.
type ChecklistPage struct {
	Checklist   *assessment.Checklist `json:"checklist"`
	Notes       string                `json:"notes"`
	MaxGrade    *float64              `json:"max_grade"`
	Description string                `json:"description"`
	Settings    course.Settings       `json:"settings"`
	Running     bool                  `json:"running"`
	Progress    *Progress             `json:"progress"`
	// ModelsProblem says why the AI check of the course cannot run; empty when it can.
	ModelsProblem string `json:"models_problem"`
}

func (s *Service) Checklist(ctx context.Context, assignmentID int64) (ChecklistPage, error) {
	r, ok, err := s.Store.LatestChecklist(ctx, assignmentID)
	if err != nil {
		return ChecklistPage{}, err
	}

	t, err := s.Store.AssignmentText(ctx, assignmentID)
	if err != nil {
		return ChecklistPage{}, err
	}

	settings, err := s.Store.CourseSettings(ctx, t.CourseID)
	if err != nil {
		return ChecklistPage{}, err
	}

	page := ChecklistPage{Notes: t.Notes, MaxGrade: t.MaxGrade, Description: work.DescriptionText(t.Intro), Settings: settings}
	if ok {
		page.Checklist = &r
	}

	// The page is useful without the progress, so a failure to read it only leaves the page as "not running".
	if p, running, err := s.Runner.CheckProgress(ctx, assignmentID); err != nil {
		slog.Warn("check progress", "assignment", assignmentID, "err", err)
	} else if running {
		page.Running, page.Progress = true, &p
	}

	if s.Enabled() {
		problem, err := s.modelsProblem(ctx, t.CourseID, assignmentID, nil)
		if err != nil {
			problem = err.Error()
		}
		page.ModelsProblem = problem
	}

	return page, nil
}

// SaveChecklist stores the teacher's checklist; the points must add up to the maximum grade.
func (s *Service) SaveChecklist(ctx context.Context, userID, assignmentID int64, criteria, extras []assessment.Criterion) error {
	t, err := s.Store.AssignmentText(ctx, assignmentID)
	if err != nil {
		return err
	}

	checked, err := assessment.TeacherChecklist(criteria, t.MaxGrade)
	if err != nil {
		return app.Fail(app.ErrInvalid, "%s", err.Error())
	}

	_, err = s.Store.SaveChecklist(ctx, assignmentID, "", "teacher", "", &userID, checked, assessment.NormalizeExtras(extras))
	return err
}

func (s *Service) ApproveChecklist(ctx context.Context, userID, assignmentID int64) error {
	return s.Store.ApproveChecklist(ctx, assignmentID, userID)
}

// SetAICheck turns the AI check of one assignment on or off; off, no checklist is built and no work of it analyzed.
func (s *Service) SetAICheck(ctx context.Context, assignmentID int64, on bool) error {
	return s.Store.SetAssignmentAICheck(ctx, assignmentID, on)
}

// SaveNotes stores the teacher's notes for the grader of an assignment.
func (s *Service) SaveNotes(ctx context.Context, assignmentID int64, notes string) error {
	if len(notes) > 8000 {
		return app.Fail(app.ErrInvalid, "the notes are longer than 8000 characters")
	}

	return s.Store.SetGraderNotes(ctx, assignmentID, strings.TrimSpace(notes))
}
