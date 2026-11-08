package postgres

import (
	"context"

	"moodle-autograder/internal/app/courses"

	"github.com/jackc/pgx/v5"
)

// Schema moodle: courses, the copy of the teachers' rights in them, and the sync status.

// SetAvailableCourses records which courses the user may grade, as Moodle reports right now.
func (s *Store) SetAvailableCourses(ctx context.Context, userID int64, site string, list []courses.Remote) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return wrap("begin", err)
	}
	defer tx.Rollback(ctx)
	var ids []int64
	for _, c := range list {
		var id int64
		if err := tx.QueryRow(ctx, `insert into moodle.courses (moodle_site, moodle_course_id, full_name) values ($1, $2, $3)
			on conflict (moodle_site, moodle_course_id) do update set full_name = excluded.full_name returning id`, site, c.ID, c.FullName).Scan(&id); err != nil {
			return wrap("upsert course", err)
		}
		if _, err := tx.Exec(ctx, `insert into moodle.course_memberships (user_id, course_id, can_grade) values ($1, $2, true)
			on conflict (user_id, course_id) do update set can_grade = true, checked_at = now()`, userID, id); err != nil {
			return wrap("upsert membership", err)
		}
		ids = append(ids, id)
	}

	if _, err := tx.Exec(ctx, `update moodle.course_memberships set can_grade = false, checked_at = now() where user_id = $1 and course_id <> all($2)`, userID, ids); err != nil {
		return wrap("revoke memberships", err)
	}

	return wrap("commit", tx.Commit(ctx))
}

func (s *Store) Courses(ctx context.Context, userID int64, connectedOnly bool) ([]courses.Course, error) {
	rows, err := s.db.Query(ctx, `select c.id, c.moodle_course_id, c.full_name, c.connected_at is not null, c.sync_status, c.sync_error, c.last_synced_at,
		coalesce(w.works, 0), coalesce(w.processed, 0), coalesce(f.files, 0), coalesce(f.downloaded, 0), coalesce(f.bytes, 0), coalesce(f.bytes_downloaded, 0),
		c.sync_started_at, greatest(f.last_download, w.last_extract), c.sync_workflow_id
		from moodle.courses c join moodle.course_memberships m on m.course_id = c.id and m.user_id = $1 and m.can_grade
		left join lateral (select count(*) works, count(*) filter (where s.pipeline_state = 'INGESTED') processed,
			(select max(x.extracted_at) from integrity.submission_features x join moodle.submissions xs on xs.id = x.submission_id
				join moodle.assignments xa on xa.id = xs.assignment_id where xa.course_id = c.id) last_extract
			from moodle.submissions s join moodle.assignments a on a.id = s.assignment_id where a.course_id = c.id and s.moodle_status = 'submitted') w on true
		left join lateral (select count(*) files, count(sf.downloaded_at) downloaded, sum(sf.size_bytes) bytes,
			sum(sf.size_bytes) filter (where sf.downloaded_at is not null) bytes_downloaded, max(sf.downloaded_at) last_download
			from moodle.submission_files sf join moodle.submissions s on s.id = sf.submission_id join moodle.assignments a on a.id = s.assignment_id
			where a.course_id = c.id and s.moodle_status = 'submitted') f on true
		where not $2 or c.connected_at is not null order by c.full_name`, userID, connectedOnly)
	if err != nil {
		return nil, wrap("list courses", err)
	}

	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (courses.Course, error) {
		var c courses.Course
		err := r.Scan(&c.ID, &c.MoodleID, &c.Name, &c.Connected, &c.SyncStatus, &c.SyncError, &c.LastSynced,
			&c.Progress.Works, &c.Progress.WorksProcessed, &c.Progress.Files, &c.Progress.FilesDownloaded, &c.Progress.Bytes, &c.Progress.BytesDownloaded,
			&c.Progress.StartedAt, &c.Progress.LastActivity, &c.SyncRunID)
		return c, err
	})
}

func (s *Store) CanGrade(ctx context.Context, userID, courseID int64) (bool, error) {
	var ok bool
	err := s.db.QueryRow(ctx, `select exists(select 1 from moodle.course_memberships where user_id = $1 and course_id = $2 and can_grade)`, userID, courseID).Scan(&ok)
	return ok, wrap("check membership", err)
}

func (s *Store) SetConnected(ctx context.Context, userID, courseID int64, connected bool) error {
	_, err := s.db.Exec(ctx, `update moodle.courses set connected_at = case when $3 then coalesce(connected_at, now()) end,
		connected_by = case when $3 then coalesce(connected_by, $1) end where id = $2`, userID, courseID, connected)
	return wrap("connect course", err)
}

func (s *Store) CourseMoodleID(ctx context.Context, courseID int64) (int64, error) {
	var id int64
	err := s.db.QueryRow(ctx, `select moodle_course_id from moodle.courses where id = $1`, courseID).Scan(&id)
	return id, found("course", err)
}

// StartSync returns false if a sync is already running (status unchanged).
func (s *Store) StartSync(ctx context.Context, courseID int64, workflowID string) (bool, error) {
	tag, err := s.db.Exec(ctx, `update moodle.courses set sync_status = 'running', sync_error = null, sync_workflow_id = $2, sync_started_at = now()
		where id = $1 and sync_status <> 'running'`, courseID, workflowID)
	return tag.RowsAffected() == 1, wrap("start sync", err)
}

func (s *Store) FinishSync(ctx context.Context, courseID int64, syncErr string) error {
	_, err := s.db.Exec(ctx, `update moodle.courses set sync_status = case when $2 = '' then 'idle' else 'failed' end, sync_error = nullif($2, ''),
		last_synced_at = case when $2 = '' then now() else last_synced_at end where id = $1`, courseID, syncErr)
	return wrap("finish sync", err)
}

// SetMyGroups stores the teacher's own groups in the course, found by their Moodle user id among the enrolled users.
func (s *Store) SetMyGroups(ctx context.Context, userID, courseID int64, byMoodleID map[int64][]string) error {
	var moodleID int64
	if err := s.db.QueryRow(ctx, `select moodle_user_id from identity.users where id = $1`, userID).Scan(&moodleID); err != nil {
		return wrap("user moodle id", err)
	}

	_, err := s.db.Exec(ctx, `update moodle.course_memberships set groups = $3 where user_id = $1 and course_id = $2`, userID, courseID, nonNil(byMoodleID[moodleID]))
	return wrap("set my groups", err)
}

func (s *Store) MyGroups(ctx context.Context, userID, courseID int64) ([]string, error) {
	var groups []string
	err := s.db.QueryRow(ctx, `select coalesce((select groups from moodle.course_memberships where user_id = $1 and course_id = $2), '{}')`, userID, courseID).Scan(&groups)
	return groups, wrap("my groups", err)
}
