package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"

	"moodle-autograder/internal/app"
	"moodle-autograder/internal/app/grading"
	"moodle-autograder/internal/domain/assessment"
)

// Schema grading: one draft per work, approvals (the exact snapshot that may be published) and publishing.

// SetIntegrity records the similarity level of a work for the teacher, creating its draft if needed. The level never
// changes the points or the comment.
func (s *Store) SetIntegrity(ctx context.Context, submissionID int64, level string) error {
	_, err := s.db.Exec(ctx, `insert into grading.grade_drafts as d (submission_id, integrity) values ($1, $2)
		on conflict (submission_id) do update set integrity = excluded.integrity, updated_at = now()`, submissionID, level)
	return wrap("set integrity", err)
}

// RestoreDrafts brings what Moodle holds into the draft of the same attempt, creating it if needed: an attempt graded
// there is a published work with those points, and a comment fills a draft without one. An attempt without a grade gets no
// points, whatever the attempts before it got. A draft the teacher edited or that left the draft state stays as it is, even
// with an empty comment: clearing it was their choice. Points of the AI stay until the teacher decides.
func (s *Store) RestoreDrafts(ctx context.Context, assignmentID int64) error {
	_, err := s.db.Exec(ctx, `insert into grading.grade_drafts as d (submission_id, points, comment, state)
		select sb.id, x.grade, x.comment, case when x.grade is null then 'draft' else 'published' end from moodle.moodle_attempts f
		cross join lateral jsonb_to_recordset(f.attempts) as x(attempt int, grade numeric, comment text)
		join moodle.assignments a on a.id = f.assignment_id
		join moodle.students st on st.course_id = a.course_id and st.moodle_user_id = f.moodle_user_id
		join moodle.submissions sb on sb.assignment_id = f.assignment_id and sb.student_id = st.id and sb.attempt = x.attempt
		where f.assignment_id = $1 and (x.grade is not null or x.comment <> '')
		on conflict (submission_id) do update set
			points = case when d.points is null and d.source = 'auto' then excluded.points else d.points end,
			comment = case when d.comment = '' then excluded.comment else d.comment end,
			state = case when d.points is null and d.source = 'auto' and excluded.points is not null then 'published' else d.state end,
			version = d.version + 1, updated_at = now()
		where d.source <> 'teacher' and d.state = 'draft'
			and ((d.points is null and d.source = 'auto' and excluded.points is not null) or (d.comment = '' and excluded.comment <> ''))`, assignmentID)
	return wrap("restore drafts", err)
}

// ApplyAIDraft puts the AI result into the grade draft unless the teacher edited it; a suspected prompt injection
// leaves no AI points. The similarity level does not matter: the teacher decides about copies.
func (s *Store) ApplyAIDraft(ctx context.Context, submissionID int64, res *assessment.Result, comment string) error {
	if res.Injection.Suspected {
		_, err := s.db.Exec(ctx, `update grading.grade_drafts set points = null, comment = '', assessment = null, source = 'auto', version = version + 1, updated_at = now()
			where submission_id = $1 and source = 'llm' and state = 'draft'`, submissionID)
		return wrap("clear ai draft", err)
	}

	raw, err := json.Marshal(res)
	if err != nil {
		return fmt.Errorf("encode assessment: %w", err)
	}

	_, err = s.db.Exec(ctx, `update grading.grade_drafts set points = $2, comment = $3, assessment = $4, source = 'llm', version = version + 1, updated_at = now()
		where submission_id = $1 and source <> 'teacher' and state = 'draft'`, submissionID, res.Points, comment, raw)
	return wrap("set ai draft", err)
}

func (s *Store) UpdateDraft(ctx context.Context, submissionID, userID int64, points *float64, comment string, version int) error {
	tag, err := s.db.Exec(ctx, `update grading.grade_drafts set points = $2, comment = $3, source = 'teacher', state = 'draft', publish_error = null, edited_by = $4,
		version = version + 1, updated_at = now() where submission_id = $1 and version = $5`, submissionID, points, comment, userID, version)
	if err != nil {
		return wrap("update draft", err)
	}

	if tag.RowsAffected() == 0 {
		return app.Fail(app.ErrConflict, "draft was changed by someone else")
	}

	return nil
}

func contentHash(points float64, comment string) string {
	sum := sha256.Sum256([]byte(strconv.FormatFloat(points, 'f', -1, 64) + "\x00" + comment))
	return hex.EncodeToString(sum[:])
}

// Approve snapshots the current draft; only this snapshot can be published.
func (s *Store) Approve(ctx context.Context, submissionID, userID int64) (int64, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return 0, wrap("begin", err)
	}
	defer tx.Rollback(ctx)
	var points *float64
	var comment string
	var version int
	if err := tx.QueryRow(ctx, `select points::float8, comment, version from grading.grade_drafts where submission_id = $1 for update`, submissionID).Scan(&points, &comment, &version); err != nil {
		return 0, wrap("load draft", err)
	}

	if points == nil {
		return 0, app.Fail(app.ErrInvalid, "submission %d has no points", submissionID)
	}

	var id int64
	if err := tx.QueryRow(ctx, `insert into grading.approvals (submission_id, draft_version, points, comment, content_hash, approved_by) values ($1, $2, $3, $4, $5, $6)
		on conflict (submission_id, draft_version) do update set approved_by = excluded.approved_by returning id`,
		submissionID, version, *points, comment, contentHash(*points, comment), userID).Scan(&id); err != nil {
		return 0, wrap("insert approval", err)
	}

	if _, err := tx.Exec(ctx, `update grading.grade_drafts set state = 'approved', publish_error = null where submission_id = $1`, submissionID); err != nil {
		return 0, wrap("mark approved", err)
	}

	return id, wrap("commit", tx.Commit(ctx))
}

// Approval loads an approval and verifies its content hash, so a tampered row is never published.
func (s *Store) Approval(ctx context.Context, id int64) (grading.Approval, error) {
	var a grading.Approval
	var hash string
	err := s.db.QueryRow(ctx, `select ap.id, ap.submission_id, a.moodle_assign_id, st.moodle_user_id, ap.approved_by, ap.points::float8, ap.comment, ap.content_hash
		from grading.approvals ap join moodle.submissions s on s.id = ap.submission_id join moodle.assignments a on a.id = s.assignment_id
		join moodle.students st on st.id = s.student_id where ap.id = $1`, id).
		Scan(&a.ID, &a.SubmissionID, &a.MoodleAssignID, &a.MoodleUserID, &a.ApprovedBy, &a.Points, &a.Comment, &hash)
	if err != nil {
		return a, wrap("load approval", err)
	}

	if hash != contentHash(a.Points, a.Comment) {
		return a, fmt.Errorf("approval %d: content hash mismatch", id)
	}

	return a, nil
}

// MarkPublished records a grade Moodle accepted: the approval and the draft are published, and the copy of the
// Moodle gradebook holds the grade at once instead of after the next sync.
func (s *Store) MarkPublished(ctx context.Context, a grading.Approval) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return wrap("begin", err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `update grading.approvals set published_at = now() where id = $1`, a.ID); err != nil {
		return wrap("mark approval published", err)
	}

	if _, err := tx.Exec(ctx, `update grading.grade_drafts set state = 'published', publish_error = null where submission_id = $1`, a.SubmissionID); err != nil {
		return wrap("mark draft published", err)
	}

	if _, err := tx.Exec(ctx, `insert into moodle.moodle_grades (assignment_id, moodle_user_id, grade, graded_at) select assignment_id, $2, $3, now() from moodle.submissions where id = $1
		on conflict (assignment_id, moodle_user_id) do update set grade = excluded.grade, graded_at = excluded.graded_at`, a.SubmissionID, a.MoodleUserID, a.Points); err != nil {
		return wrap("record published grade", err)
	}

	return wrap("commit publish", tx.Commit(ctx))
}

func (s *Store) MarkPublishFailed(ctx context.Context, submissionID int64, msg string) error {
	_, err := s.db.Exec(ctx, `update grading.grade_drafts set state = 'publish_failed', publish_error = $2 where submission_id = $1`, submissionID, msg)
	return wrap("mark publish failed", err)
}
