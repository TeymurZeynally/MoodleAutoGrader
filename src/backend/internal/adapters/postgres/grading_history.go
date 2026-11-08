package postgres

import (
	"context"
	"errors"
	"time"

	"moodle-autograder/internal/app/grading"

	"github.com/jackc/pgx/v5"
)

// Schema grading: the review history of a work (uploads, AI checks, published grades).

// History lists uploads, AI checks and published grades of the student who made the submission, for its assignment.
func (s *Store) History(ctx context.Context, submissionID int64) ([]grading.Event, error) {
	rows, err := s.db.Query(ctx, `with me as (select assignment_id, student_id from moodle.submissions where id = $1)
		select e.kind, e.at, sb.attempt, e.points::float8, e.comment, null::text, e.details
		from grading.review_events e join moodle.submissions sb on sb.id = e.submission_id join me on me.assignment_id = sb.assignment_id and me.student_id = sb.student_id
		union all
		select 'published', a.published_at, sb.attempt, a.points::float8, a.comment, u.full_name, '{}'::jsonb
		from grading.approvals a join moodle.submissions sb on sb.id = a.submission_id join me on me.assignment_id = sb.assignment_id and me.student_id = sb.student_id
		left join identity.users u on u.id = a.approved_by where a.published_at is not null
		order by 2`, submissionID)
	if err != nil {
		return nil, wrap("history", err)
	}

	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (grading.Event, error) {
		var e grading.Event
		err := r.Scan(&e.Kind, &e.At, &e.Attempt, &e.Points, &e.Comment, &e.By, &e.Details)
		return e, err
	})
}

// PreviousComment is the last comment published to the student for this assignment before the current upload, if any;
// without a record of publishing here, the teacher's comment on an earlier attempt in the copy from Moodle.
func (s *Store) PreviousComment(ctx context.Context, submissionID int64) (string, *time.Time, error) {
	var c string
	var at time.Time
	err := s.db.QueryRow(ctx, `select a.comment, a.published_at from grading.approvals a join moodle.submissions sb on sb.id = a.submission_id
		join moodle.submissions me on me.id = $1 and me.assignment_id = sb.assignment_id and me.student_id = sb.student_id
		where a.published_at is not null and a.published_at < coalesce(me.submitted_at, now()) and a.comment <> ''
		order by a.published_at desc limit 1`, submissionID).Scan(&c, &at)
	if errors.Is(err, pgx.ErrNoRows) {
		return s.previousMoodleComment(ctx, submissionID)
	}

	if err != nil {
		return "", nil, wrap("previous comment", err)
	}

	return c, &at, nil
}

func (s *Store) previousMoodleComment(ctx context.Context, submissionID int64) (string, *time.Time, error) {
	var c string
	var at *time.Time
	err := s.db.QueryRow(ctx, `select x.comment, x.graded_at from moodle.submissions me join moodle.students st on st.id = me.student_id
		join moodle.moodle_attempts f on f.assignment_id = me.assignment_id and f.moodle_user_id = st.moodle_user_id
		cross join lateral jsonb_to_recordset(f.attempts) as x(attempt int, comment text, graded_at timestamptz)
		where me.id = $1 and x.comment <> '' and (x.attempt < me.attempt or (x.attempt = me.attempt and x.graded_at < me.submitted_at))
		order by x.attempt desc limit 1`, submissionID).Scan(&c, &at)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil, nil
	}

	if err != nil {
		return "", nil, wrap("previous comment from moodle", err)
	}

	return c, at, nil
}
