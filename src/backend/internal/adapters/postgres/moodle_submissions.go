package postgres

import (
	"context"

	"moodle-autograder/internal/app/syncing"
	"moodle-autograder/internal/domain/work"

	"github.com/jackc/pgx/v5"
)

// Schema moodle: submissions and their files.

// SyncSubmission stores submission metadata and file list. It returns true when some file still has to be downloaded:
// a file is downloaded again only if its Moodle size or modification time changed.
func (s *Store) SyncSubmission(ctx context.Context, assignmentID, studentID int64, sub syncing.Submission) (int64, bool, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return 0, false, wrap("begin", err)
	}
	defer tx.Rollback(ctx)
	var id int64
	if err := tx.QueryRow(ctx, `insert into moodle.submissions (assignment_id, student_id, moodle_submission_id, attempt, moodle_status, submitted_at) values ($1, $2, $3, $4, $5, $6)
		on conflict (assignment_id, student_id, attempt) do update set moodle_submission_id = excluded.moodle_submission_id, moodle_status = excluded.moodle_status,
		submitted_at = excluded.submitted_at, synced_at = now() returning id`, assignmentID, studentID, sub.ID, sub.Attempt, sub.Status, sub.SubmittedAt).Scan(&id); err != nil {
		return 0, false, wrap("upsert submission", err)
	}

	if sub.Status == "submitted" && sub.SubmittedAt != nil { // a new upload (also a re-upload of the same attempt) goes into the history
		if _, err := tx.Exec(ctx, `insert into grading.review_events (submission_id, kind, at) select $1, 'upload', $2::timestamptz
			where not exists (select 1 from grading.review_events where submission_id = $1 and kind = 'upload' and at = $2::timestamptz)`, id, sub.SubmittedAt); err != nil {
			return 0, false, wrap("record upload", err)
		}
	}

	keep := []string{} // not nil: "<> all(NULL)" would keep every file when the student removed them all
	for _, f := range sub.Files {
		if _, err := tx.Exec(ctx, `insert into moodle.submission_files as f (submission_id, file_path, file_name, size_bytes, mime_type, moodle_modified_at, file_url)
			values ($1, $2, $3, $4, $5, $6, $7)
			on conflict (submission_id, file_path, file_name) do update set file_url = excluded.file_url, mime_type = excluded.mime_type,
			object_key = case when f.size_bytes = excluded.size_bytes and f.moodle_modified_at is not distinct from excluded.moodle_modified_at then f.object_key end,
			sha256 = case when f.size_bytes = excluded.size_bytes and f.moodle_modified_at is not distinct from excluded.moodle_modified_at then f.sha256 end,
			downloaded_at = case when f.size_bytes = excluded.size_bytes and f.moodle_modified_at is not distinct from excluded.moodle_modified_at then f.downloaded_at end,
			size_bytes = excluded.size_bytes, moodle_modified_at = excluded.moodle_modified_at`, id, f.Path, f.Name, f.Size, f.MimeType, f.Modified, f.URL); err != nil {
			return 0, false, wrap("upsert file", err)
		}
		keep = append(keep, f.Path+f.Name)
	}

	if _, err := tx.Exec(ctx, `delete from moodle.submission_files where submission_id = $1 and file_path || file_name <> all($2)`, id, keep); err != nil {
		return 0, false, wrap("delete removed files", err)
	}

	var pending bool
	if err := tx.QueryRow(ctx, `select exists(select 1 from moodle.submission_files where submission_id = $1 and object_key is null)`, id).Scan(&pending); err != nil {
		return 0, false, wrap("pending files", err)
	}

	return id, pending, wrap("commit", tx.Commit(ctx))
}

func (s *Store) Submission(ctx context.Context, id int64) (work.Submission, error) {
	var x work.Submission
	err := s.db.QueryRow(ctx, `select s.id, a.id, a.course_id, a.moodle_assign_id, st.moodle_user_id, st.full_name, st.pseudonym, a.name, a.intro,
		st.first_name, st.last_name, st.groups, a.max_grade::float8, s.submitted_at, a.due_at
		from moodle.submissions s join moodle.assignments a on a.id = s.assignment_id join moodle.students st on st.id = s.student_id where s.id = $1`, id).
		Scan(&x.ID, &x.AssignmentID, &x.CourseID, &x.MoodleAssignID, &x.MoodleUserID, &x.Student, &x.Pseudonym, &x.AssignmentName, &x.Intro,
			&x.FirstName, &x.LastName, &x.Groups, &x.MaxGrade, &x.SubmittedAt, &x.DueAt)
	return x, found("submission", err)
}

func (s *Store) SubmissionCourse(ctx context.Context, submissionID int64) (int64, error) {
	return s.courseOf(ctx, "submission course", `select a.course_id from moodle.submissions s join moodle.assignments a on a.id = s.assignment_id where s.id = $1`, submissionID)
}

// SubmittedIDs lists the works of an assignment to check, in upload order: each student's newest submitted attempt.
// Works that already have a grade in the Moodle gradebook are left out unless includeGraded, and so are works with a
// newer attempt opened: Moodle has no grade for that attempt yet, but the student has sent nothing new.
func (s *Store) SubmittedIDs(ctx context.Context, assignmentID int64, includeGraded bool) ([]int64, error) {
	rows, err := s.db.Query(ctx, `select s.id from moodle.submissions s join moodle.students st on st.id = s.student_id
		where s.assignment_id = $1 and s.moodle_status = 'submitted'
		and ($2 or not exists (select 1 from moodle.moodle_grades g where g.assignment_id = s.assignment_id and g.moodle_user_id = st.moodle_user_id))
		and not exists (select 1 from moodle.submissions n where n.assignment_id = s.assignment_id and n.student_id = s.student_id and n.attempt > s.attempt and (not $2 or n.moodle_status = 'submitted'))
		order by s.submitted_at nulls last, s.id`, assignmentID, includeGraded)
	if err != nil {
		return nil, wrap("submitted ids", err)
	}

	ids, err := pgx.CollectRows(rows, pgx.RowTo[int64])
	return ids, wrap("scan submitted ids", err)
}

func (s *Store) Files(ctx context.Context, submissionID int64, pendingOnly bool) ([]work.File, error) {
	rows, err := s.db.Query(ctx, `select id, file_path || file_name, file_url, coalesce(object_key, ''), coalesce(size_bytes, 0), coalesce(sha256, '')
		from moodle.submission_files where submission_id = $1 and (not $2 or object_key is null) order by id`, submissionID, pendingOnly)
	if err != nil {
		return nil, wrap("list files", err)
	}

	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (work.File, error) {
		var f work.File
		err := r.Scan(&f.ID, &f.Name, &f.URL, &f.Key, &f.Size, &f.SHA256)
		return f, err
	})
}

func (s *Store) MarkDownloaded(ctx context.Context, fileID int64, key, sha256 string) error {
	_, err := s.db.Exec(ctx, `update moodle.submission_files set object_key = $2, sha256 = $3, downloaded_at = now() where id = $1`, fileID, key, sha256)
	return wrap("mark downloaded", err)
}
