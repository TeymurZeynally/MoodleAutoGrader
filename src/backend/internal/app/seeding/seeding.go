// Package seeding fills a course with prepared data: the settings, the prompts and models of the AI check, the approved
// checklists and the notes for the grader, so that colleagues on the same Moodle site start from what a teacher already
// approved. Data of a course is taken only where the course has none: what its teachers changed stays. The files live
// outside the repository, a checklist must not be public where students can read it.
package seeding

import (
	"context"
	"log/slog"

	"moodle-autograder/internal/app"
	"moodle-autograder/internal/domain/assessment"
	"moodle-autograder/internal/domain/course"
)

// Course is the prepared data of a course; what is absent is simply not loaded.
type Course struct {
	Settings    *course.Settings
	Prompts     map[string]string    // "check.system" -> template, "check.model" -> model name
	Assignments map[int64]Assignment // by the assignment's id in Moodle
}

// Assignment is the prepared data of an assignment. Name only tells a person reading the file which one it is.
type Assignment struct {
	Name     string                 `json:"name,omitempty"`
	Notes    string                 `json:"notes,omitempty"`
	Criteria []assessment.Criterion `json:"criteria,omitempty"`
	Extras   []assessment.Criterion `json:"extras,omitempty"`
}

// Target is an assignment of a course in the database.
type Target struct {
	ID, MoodleID int64
	Name, Notes  string
	MaxGrade     *float64
}

// Source reads the prepared data; an empty Course when there is none for this course.
type Source interface {
	Course(moodleCourseID int64) (Course, error)
}

// Prompts checks a template or a model name before it is stored.
type Prompts interface {
	Validate(key, body string) error
}

// Store keeps the data of a course. The Seed methods change nothing that already exists.
type Store interface {
	CourseMoodleID(ctx context.Context, courseID int64) (int64, error)
	CourseByMoodleID(ctx context.Context, site string, moodleCourseID int64) (int64, error)
	AssignmentTargets(ctx context.Context, courseID int64) ([]Target, error)

	SeedSettings(ctx context.Context, courseID int64, c course.Settings) error
	SeedPrompt(ctx context.Context, courseID int64, key, body string) error
	SeedChecklist(ctx context.Context, assignmentID int64, criteria, extras []assessment.Criterion) error
	SeedNotes(ctx context.Context, assignmentID int64, notes string) error

	CourseSettings(ctx context.Context, courseID int64) (course.Settings, error)
	PromptOverrides(ctx context.Context, courseID int64) (map[string]string, error)
	LatestChecklist(ctx context.Context, assignmentID int64) (assessment.Checklist, bool, error)
}

type Service struct {
	Store   Store
	Source  Source
	Prompts Prompts
	Site    string // the Moodle site of this installation
}

// Apply loads what the course does not have yet. It runs on every sync, once the assignments are stored, so new
// assignments get their data too. A file that does not make sense is left out with a warning: it must not stop a sync.
func (s *Service) Apply(ctx context.Context, courseID int64) error {
	moodleID, err := s.Store.CourseMoodleID(ctx, courseID)
	if err != nil {
		return err
	}

	seed, err := s.Source.Course(moodleID)
	if err != nil {
		slog.Warn("seed not loaded", "moodle_course", moodleID, "err", err)
		return nil
	}

	if seed.Settings != nil {
		c := seed.Settings.Normalize()
		if err := c.Validate(); err != nil {
			slog.Warn("seed settings left out", "moodle_course", moodleID, "err", err)
		} else if err := s.Store.SeedSettings(ctx, courseID, c); err != nil {
			return err
		}
	}

	for key, body := range seed.Prompts {
		if err := s.Prompts.Validate(key, body); err != nil {
			slog.Warn("seed prompt left out", "moodle_course", moodleID, "prompt", key, "err", err)
			continue
		}
		if err := s.Store.SeedPrompt(ctx, courseID, key, body); err != nil {
			return err
		}
	}

	targets, err := s.Store.AssignmentTargets(ctx, courseID)
	if err != nil {
		return err
	}

	for _, t := range targets {
		if a, ok := seed.Assignments[t.MoodleID]; ok {
			if err := s.applyAssignment(ctx, t, a); err != nil {
				return err
			}
		}
	}

	return nil
}

func (s *Service) applyAssignment(ctx context.Context, t Target, a Assignment) error {
	if a.Notes != "" {
		if err := s.Store.SeedNotes(ctx, t.ID, a.Notes); err != nil {
			return err
		}
	}

	if len(a.Criteria) == 0 {
		return nil
	}

	criteria, err := assessment.TeacherChecklist(a.Criteria, t.MaxGrade)
	if err != nil {
		slog.Warn("seed checklist left out", "moodle_assignment", t.MoodleID, "err", err)
		return nil
	}

	return s.Store.SeedChecklist(ctx, t.ID, criteria, assessment.NormalizeExtras(a.Extras))
}

// Export reads a course as prepared data for others: its settings, what its teachers changed in the prompts and models,
// the notes and the checklists a teacher approved.
func (s *Service) Export(ctx context.Context, moodleCourseID int64) (Course, error) {
	courseID, err := s.Store.CourseByMoodleID(ctx, s.Site, moodleCourseID)
	if err != nil {
		return Course{}, err
	}

	settings, err := s.Store.CourseSettings(ctx, courseID)
	if err != nil {
		return Course{}, err
	}

	prompts, err := s.Store.PromptOverrides(ctx, courseID)
	if err != nil {
		return Course{}, err
	}

	targets, err := s.Store.AssignmentTargets(ctx, courseID)
	if err != nil {
		return Course{}, err
	}

	out := Course{Settings: &settings, Prompts: prompts, Assignments: map[int64]Assignment{}}
	for _, t := range targets {
		a := Assignment{Name: t.Name, Notes: t.Notes}
		r, ok, err := s.Store.LatestChecklist(ctx, t.ID)
		if err != nil {
			return Course{}, err
		}
		if ok && r.Approved {
			a.Criteria, a.Extras = r.Criteria, r.Extras
		}
		if a.Notes != "" || len(a.Criteria) > 0 {
			out.Assignments[t.MoodleID] = a
		}
	}

	if len(out.Assignments) == 0 && len(prompts) == 0 {
		return out, app.Fail(app.ErrNotFound, "the course has no changed prompts, notes or approved checklists to export")
	}

	return out, nil
}
