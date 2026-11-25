package postgres

import (
	"context"
	"encoding/json"
	"fmt"

	"moodle-autograder/internal/app/seeding"
	"moodle-autograder/internal/domain/assessment"
	"moodle-autograder/internal/domain/course"

	"github.com/jackc/pgx/v5"
)

// Prepared data of a course (app/seeding), across the schemas moodle, settings and ai: nothing that exists is changed.

// CourseByMoodleID finds a course of this Moodle site by its id there.
func (s *Store) CourseByMoodleID(ctx context.Context, site string, moodleCourseID int64) (int64, error) {
	var id int64
	err := s.db.QueryRow(ctx, `select id from moodle.courses where moodle_site = $1 and moodle_course_id = $2`, site, moodleCourseID).Scan(&id)
	return id, found("course", err)
}

// AssignmentTargets lists the assignments of a course with their ids in Moodle.
func (s *Store) AssignmentTargets(ctx context.Context, courseID int64) ([]seeding.Target, error) {
	rows, err := s.db.Query(ctx, `select id, moodle_assign_id, name, grader_notes, max_grade::float8 from moodle.assignments where course_id = $1 order by id`, courseID)
	if err != nil {
		return nil, wrap("assignment targets", err)
	}

	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (seeding.Target, error) {
		var t seeding.Target
		err := r.Scan(&t.ID, &t.MoodleID, &t.Name, &t.Notes, &t.MaxGrade)
		return t, err
	})
}

// SeedSettings stores the rules of a course that has never saved any.
func (s *Store) SeedSettings(ctx context.Context, courseID int64, c course.Settings) error {
	_, err := s.db.Exec(ctx, `insert into settings.course_settings (course_id, track_deadlines, late_penalty_enabled, late_penalty, require_code, extras_enabled,
		late_notice, max_images, partial_credit, points_step, similar_percent, copy_percent, copy_identical_files, template_min_works, template_share,
		code_extensions, ignored_dirs, download_files, ai_check, ai_groups, read_all_pages)
		values ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21) on conflict (course_id) do nothing`,
		courseID, c.TrackDeadlines, c.LatePenaltyEnabled, c.LatePenalty, c.RequireCode, c.ExtrasEnabled,
		c.LateNotice, c.MaxImages, c.PartialCredit, c.PointsStep, c.SimilarPercent, c.CopyPercent, c.CopyIdenticalFiles, c.TemplateMinWorks, c.TemplateShare,
		nonNil(c.CodeExtensions), nonNil(c.IgnoredDirs), c.DownloadFiles, c.AICheck, nonNil(c.AIGroups), c.ReadAllPages)
	return wrap("seed course settings", err)
}

// SeedPrompt stores a template or a model the course has not changed.
func (s *Store) SeedPrompt(ctx context.Context, courseID int64, key, body string) error {
	_, err := s.db.Exec(ctx, `insert into settings.prompt_overrides (course_id, key, body) values ($1, $2, $3) on conflict (course_id, key) do nothing`, courseID, key, body)
	return wrap("seed prompt", err)
}

// SeedChecklist stores an approved checklist for an assignment that has none, as a teacher's: it is kept until the
// teacher asks for a new one.
func (s *Store) SeedChecklist(ctx context.Context, assignmentID int64, criteria, extras []assessment.Criterion) error {
	raw, err := json.Marshal(criteria)
	if err != nil {
		return fmt.Errorf("encode checklist: %w", err)
	}

	ex, err := json.Marshal(nonNil(extras))
	if err != nil {
		return fmt.Errorf("encode extras: %w", err)
	}

	_, err = s.db.Exec(ctx, `insert into ai.checklists (assignment_id, intro_hash, source, model, criteria, extras, approved_at)
		select $1::bigint, '', 'teacher', '', $2::jsonb, $3::jsonb, now() where not exists (select 1 from ai.checklists where assignment_id = $1::bigint)`, assignmentID, raw, ex)
	return wrap("seed checklist", err)
}

// SeedNotes stores the notes for the grader of an assignment that has none.
func (s *Store) SeedNotes(ctx context.Context, assignmentID int64, notes string) error {
	_, err := s.db.Exec(ctx, `update moodle.assignments set grader_notes = $2 where id = $1 and grader_notes = ''`, assignmentID, notes)
	return wrap("seed notes", err)
}
