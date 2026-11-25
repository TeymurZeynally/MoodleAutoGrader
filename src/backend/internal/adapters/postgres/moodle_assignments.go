package postgres

import (
	"context"

	"moodle-autograder/internal/app/courses"
	"moodle-autograder/internal/app/grading"
	"moodle-autograder/internal/app/syncing"
	"moodle-autograder/internal/domain/work"

	"github.com/jackc/pgx/v5"
)

// Schema moodle: assignments.

func (s *Store) UpsertAssignment(ctx context.Context, courseID int64, a syncing.Assignment) (int64, error) {
	var id int64
	err := s.db.QueryRow(ctx, `insert into moodle.assignments (course_id, moodle_assign_id, cmid, name, intro, due_at, max_grade, scale_id, marking_workflow)
		values ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		on conflict (course_id, moodle_assign_id) do update set cmid = excluded.cmid, name = excluded.name, intro = excluded.intro, due_at = excluded.due_at,
		max_grade = excluded.max_grade, scale_id = excluded.scale_id, marking_workflow = excluded.marking_workflow returning id`,
		courseID, a.ID, a.CMID, a.Name, a.Intro, a.DueAt, a.MaxGrade, a.ScaleID, a.MarkingWorkflow).Scan(&id)
	return id, wrap("upsert assignment", err)
}

func (s *Store) Assignments(ctx context.Context, courseID int64) ([]courses.Assignment, error) {
	rows, err := s.db.Query(ctx, `select a.id, a.name, a.due_at, a.max_grade::float8, a.marking_workflow,
		(select count(distinct s.student_id) from moodle.submissions s where s.assignment_id = a.id and s.moodle_status = 'submitted'),
		(select count(distinct s.student_id) from moodle.submissions s join grading.grade_drafts d on d.submission_id = s.id where s.assignment_id = a.id and d.integrity = 'high'),
		(select count(distinct s.student_id) from moodle.submissions s join grading.grade_drafts d on d.submission_id = s.id where s.assignment_id = a.id and d.state = 'published'),
		ai.queued, ai.running, ai.done, ai.attention, ai.failed, exists(select 1 from ai.checklists r where r.assignment_id = a.id),
		coalesce((select x.ai_check from settings.assignment_settings x where x.assignment_id = a.id), true)
		from moodle.assignments a
		cross join lateral (select count(*) filter (where x.status = 'queued') queued, count(*) filter (where x.status = 'running') running,
			count(*) filter (where x.status = 'done') done, count(*) filter (where x.status = 'needs_attention') attention, count(*) filter (where x.status = 'failed') failed
			from ai.assessments x join moodle.submissions s on s.id = x.submission_id where s.assignment_id = a.id) ai
		where a.course_id = $1 order by a.due_at nulls last, a.id`, courseID)
	if err != nil {
		return nil, wrap("list assignments", err)
	}

	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (courses.Assignment, error) {
		var a courses.Assignment
		err := r.Scan(&a.ID, &a.Name, &a.DueAt, &a.MaxGrade, &a.MarkingWorkflow, &a.Submitted, &a.Flagged, &a.Published,
			&a.AI.Queued, &a.AI.Running, &a.AI.Done, &a.AI.NeedsAttention, &a.AI.Failed, &a.AI.HasChecklist, &a.AICheck)
		return a, err
	})
}

func (s *Store) Assignment(ctx context.Context, assignmentID int64) (grading.Header, error) {
	var h grading.Header
	err := s.db.QueryRow(ctx, `select a.id, a.name, a.due_at, a.max_grade::float8, c.id, c.full_name, a.cmid,
		coalesce((select x.ai_check from settings.assignment_settings x where x.assignment_id = a.id), true)
		from moodle.assignments a join moodle.courses c on c.id = a.course_id where a.id = $1`, assignmentID).
		Scan(&h.ID, &h.Name, &h.DueAt, &h.MaxGrade, &h.CourseID, &h.CourseName, &h.CMID, &h.AICheck)
	return h, found("assignment", err)
}

func (s *Store) AssignmentCourse(ctx context.Context, assignmentID int64) (int64, error) {
	return s.courseOf(ctx, "assignment course", `select course_id from moodle.assignments where id = $1`, assignmentID)
}

func (s *Store) SetGraderNotes(ctx context.Context, assignmentID int64, notes string) error {
	_, err := s.db.Exec(ctx, `update moodle.assignments set grader_notes = $2 where id = $1`, assignmentID, notes)
	return wrap("set grader notes", err)
}

func (s *Store) AssignmentText(ctx context.Context, assignmentID int64) (work.Assignment, error) {
	var a work.Assignment
	err := s.db.QueryRow(ctx, `select name, coalesce(intro, ''), grader_notes, course_id, max_grade::float8 from moodle.assignments where id = $1`, assignmentID).
		Scan(&a.Name, &a.Intro, &a.Notes, &a.CourseID, &a.MaxGrade)
	return a, found("assignment text", err)
}
