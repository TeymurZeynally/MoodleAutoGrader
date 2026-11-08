package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"moodle-autograder/internal/app/grading"
	"moodle-autograder/internal/app/syncing"

	"github.com/jackc/pgx/v5"
)

// Schema moodle: students of a course and the copy of the Moodle gradebook.

// AttemptMarks tells, per student, what their attempts looked like at the last read from Moodle.
func (s *Store) AttemptMarks(ctx context.Context, assignmentID int64) (map[int64]string, error) {
	rows, err := s.db.Query(ctx, `select moodle_user_id, read_mark from moodle.moodle_attempts where assignment_id = $1`, assignmentID)
	if err != nil {
		return nil, wrap("attempt marks", err)
	}

	out := map[int64]string{}
	_, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (struct{}, error) {
		var id int64
		var mark string
		err := r.Scan(&id, &mark)
		out[id] = mark
		return struct{}{}, err
	})
	return out, wrap("scan attempt marks", err)
}

// SetAttempts stores a student's attempts from Moodle with the mark of this read; nil keeps what is known (Moodle refused).
func (s *Store) SetAttempts(ctx context.Context, assignmentID, moodleUserID int64, mark string, attempts []syncing.Attempt) error {
	var raw []byte
	if attempts != nil {
		list := make([]grading.AttemptInfo, len(attempts))
		for i, a := range attempts {
			files := make([]string, len(a.Files))
			for j, f := range a.Files {
				files[j] = f.Name
			}
			list[i] = grading.AttemptInfo{Attempt: a.Attempt, Status: a.Status, SubmittedAt: a.SubmittedAt, Files: files, Grade: a.Grade, GradedAt: a.GradedAt, Comment: a.Comment}
		}
		var err error
		if raw, err = json.Marshal(list); err != nil {
			return fmt.Errorf("encode attempts: %w", err)
		}
	}

	_, err := s.db.Exec(ctx, `insert into moodle.moodle_attempts as f (assignment_id, moodle_user_id, read_mark, attempts) values ($1, $2, $3, coalesce($4::jsonb, '[]'))
		on conflict (assignment_id, moodle_user_id) do update set read_mark = excluded.read_mark, attempts = coalesce($4::jsonb, f.attempts)`, assignmentID, moodleUserID, mark, raw)
	return wrap("set attempts", err)
}

// pseudonym is what models see instead of a student's name.
const pseudonym = `'S-' || upper(substr(md5($1::bigint::text || ':' || $2::bigint::text), 1, 6))`

// UpsertStudent stores a student. An empty name means the user is no longer enrolled (e.g. a graduated group):
// Moodle still returns their submissions, and the stored name and groups are kept.
func (s *Store) UpsertStudent(ctx context.Context, courseID, moodleUserID int64, name string, groups []string) (int64, error) {
	var id int64
	err := s.db.QueryRow(ctx, `insert into moodle.students (course_id, moodle_user_id, full_name, groups, pseudonym)
		values ($1::bigint, $2::bigint, coalesce(nullif($3::text, ''), 'User ' || $2::bigint::text), $4, `+pseudonym+`)
		on conflict (course_id, moodle_user_id) do update set full_name = case when $3::text = '' then students.full_name else excluded.full_name end,
		groups = case when $3::text = '' then students.groups else excluded.groups end returning id`, courseID, moodleUserID, name, nonNil(groups)).Scan(&id)
	return id, wrap("upsert student", err)
}

// SetRoster records every user enrolled with the student role, so students without a submission are listed too.
// Students who left the course (e.g. graduated groups) keep their rows and submissions but are no longer "enrolled".
func (s *Store) SetRoster(ctx context.Context, courseID int64, students []syncing.User) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return wrap("begin", err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `update moodle.students set enrolled = false where course_id = $1`, courseID); err != nil {
		return wrap("reset roster", err)
	}

	for _, u := range students {
		if _, err := tx.Exec(ctx, `insert into moodle.students (course_id, moodle_user_id, full_name, first_name, last_name, groups, enrolled, pseudonym)
			values ($1::bigint, $2::bigint, $3, $4, $5, $6, true, `+pseudonym+`)
			on conflict (course_id, moodle_user_id) do update set full_name = excluded.full_name, first_name = excluded.first_name, last_name = excluded.last_name,
			groups = excluded.groups, enrolled = true`,
			courseID, u.ID, u.FullName, strings.TrimSpace(u.FirstName), strings.TrimSpace(u.LastName), nonNil(u.Groups)); err != nil {
			return wrap("upsert roster", err)
		}
	}

	return wrap("commit roster", tx.Commit(ctx))
}

// SetMoodleGrades replaces the gradebook copy of an assignment.
func (s *Store) SetMoodleGrades(ctx context.Context, assignmentID int64, grades []syncing.Grade) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return wrap("begin", err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `delete from moodle.moodle_grades where assignment_id = $1`, assignmentID); err != nil {
		return wrap("clear grades", err)
	}

	for _, g := range grades {
		if _, err := tx.Exec(ctx, `insert into moodle.moodle_grades (assignment_id, moodle_user_id, grade, graded_at) values ($1, $2, $3, $4)`, assignmentID, g.UserID, g.Value, g.At); err != nil {
			return wrap("insert grade", err)
		}
	}

	return wrap("commit grades", tx.Commit(ctx))
}

func (s *Store) StudentNames(ctx context.Context, courseID int64) ([]string, error) {
	rows, err := s.db.Query(ctx, `select full_name from moodle.students where course_id = $1`, courseID)
	if err != nil {
		return nil, wrap("student names", err)
	}

	return pgx.CollectRows(rows, pgx.RowTo[string])
}
