package postgres

import (
	"context"

	"moodle-autograder/internal/app/grading"

	"github.com/jackc/pgx/v5"
)

// The grade sheet, of an assignment or of a student, reads every schema: students and submissions (moodle), fingerprints and matches
// (integrity), the AI check (ai) and drafts (grading).

// Submissions is the sheet of an assignment: every enrolled student, plus anyone who submitted (also students who left
// the course since), one row each.
func (s *Store) Submissions(ctx context.Context, assignmentID int64) ([]grading.Row, error) {
	return s.sheet(ctx, assignmentID, 0, 0)
}

// StudentSubmissions is the sheet of one student: a row per assignment of the course, by deadline.
func (s *Store) StudentSubmissions(ctx context.Context, courseID, studentID int64) ([]grading.Row, error) {
	return s.sheet(ctx, 0, courseID, studentID)
}

// sheet reads the rows of an assignment, or with assignmentID 0 the rows of a student of a course. A row is the newest
// attempt the student submitted, or the newest one if none is. A newer attempt not submitted yet is reported next to it:
// Moodle opens one by itself when a low grade is published, and it holds nothing to grade.
func (s *Store) sheet(ctx context.Context, assignmentID, courseID, studentID int64) ([]grading.Row, error) {
	rows, err := s.db.Query(ctx, `select coalesce(s.id, 0), st.id, st.moodle_user_id, st.full_name, moodle.short_name(st.last_name, st.first_name, st.full_name),
		st.enrolled, mg.grade::float8, mg.graded_at, st.groups, coalesce(s.moodle_status, 'none'), s.submitted_at,
		coalesce((select array_agg(file_name order by id) from moodle.submission_files where submission_id = s.id), '{}'),
		coalesce(f.flags, '{}'), coalesce(d.integrity, 'ok'), d.points::float8, coalesce(d.comment, ''), coalesce(d.source, 'auto'), coalesce(d.version, 0),
		coalesce(d.state, 'draft'), d.publish_error,
		ass.status, ass.points::float8,
		case when jsonb_typeof(ass.result->'criteria') = 'array' then (select count(*) from jsonb_array_elements(ass.result->'criteria') e where e->>'status' = 'met') else 0 end,
		case when jsonb_typeof(ass.result->'criteria') = 'array' then jsonb_array_length(ass.result->'criteria') else 0 end,
		case when jsonb_typeof(ass.result->'extras') = 'array' then exists(select 1 from jsonb_array_elements(ass.result->'extras') e where e->>'status' = 'met') else false end,
		(ass.result->>'score')::float8, coalesce((ass.result->>'late_penalty')::float8, 0),
		coalesce(s.submitted_at > a.due_at, false), coalesce(s.attempt, 0),
		exists(select 1 from grading.approvals ap join moodle.submissions o on o.id = ap.submission_id where o.assignment_id = a.id and o.student_id = st.id
			and ap.published_at is not null and ap.comment <> '' and ap.published_at < s.submitted_at)
		or exists(select 1 from moodle.moodle_attempts ma cross join lateral jsonb_to_recordset(ma.attempts) as x(attempt int, comment text, graded_at timestamptz)
			where ma.assignment_id = a.id and ma.moodle_user_id = st.moodle_user_id and x.comment <> ''
			and (x.attempt < s.attempt or (x.attempt = s.attempt and x.graded_at < s.submitted_at))),
		o.attempt, coalesce(o.moodle_status, ''),
		coalesce((select jsonb_agg(x.j || jsonb_build_object('submission_id', w.id) order by (x.j->>'attempt')::int)
			from moodle.moodle_attempts ma cross join lateral jsonb_array_elements(ma.attempts) as x(j)
			left join moodle.submissions w on w.assignment_id = a.id and w.student_id = st.id and w.attempt = (x.j->>'attempt')::int
			where ma.assignment_id = a.id and ma.moodle_user_id = st.moodle_user_id), '[]'),
		a.id, a.name, a.due_at, a.max_grade::float8, a.cmid, exists(select 1 from ai.checklists c where c.assignment_id = a.id),
		coalesce((select x.ai_check from settings.assignment_settings x where x.assignment_id = a.id), true)
		from moodle.assignments a join moodle.students st on st.course_id = a.course_id
		left join lateral (select x.* from moodle.submissions x where x.assignment_id = a.id and x.student_id = st.id
			order by x.moodle_status = 'submitted' desc, x.attempt desc limit 1) s on true
		left join lateral (select x.attempt, x.moodle_status from moodle.submissions x where x.assignment_id = a.id and x.student_id = st.id and x.attempt > s.attempt
			order by x.attempt desc limit 1) o on true
		left join integrity.submission_features f on f.submission_id = s.id
		left join grading.grade_drafts d on d.submission_id = s.id
		left join moodle.moodle_grades mg on mg.assignment_id = a.id and mg.moodle_user_id = st.moodle_user_id
		left join ai.assessments ass on ass.submission_id = s.id
		where (a.id = $1 or (a.course_id = $2 and st.id = $3)) and (st.enrolled or s.id is not null or st.id = $3)
		order by st.groups, st.full_name, a.due_at nulls last, a.name`, assignmentID, courseID, studentID)
	if err != nil {
		return nil, wrap("list submissions", err)
	}

	subs, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (grading.Row, error) {
		var x grading.Row
		var ai struct {
			Status     *string
			Points     *float64
			Met, Total int
			Extra      bool
			Score      *float64
			Late       float64
		}
		err := r.Scan(&x.ID, &x.StudentID, &x.MoodleUserID, &x.Student, &x.ShortName, &x.Enrolled, &x.MoodleGrade, &x.GradedAt, &x.Groups, &x.MoodleStatus, &x.SubmittedAt,
			&x.Files, &x.Flags, &x.Integrity, &x.Points, &x.Comment, &x.Source, &x.Version, &x.State, &x.PublishError,
			&ai.Status, &ai.Points, &ai.Met, &ai.Total, &ai.Extra, &ai.Score, &ai.Late, &x.Late, &x.Attempt, &x.Resubmitted, &x.OpenAttempt, &x.OpenStatus, &x.Attempts,
			&x.AssignmentID, &x.Assignment, &x.DueAt, &x.MaxGrade, &x.CMID, &x.HasChecklist, &x.AICheck)
		if ai.Status != nil {
			x.AI = &grading.AISummary{Status: *ai.Status, Points: ai.Points, Met: ai.Met, Total: ai.Total, Extra: ai.Extra, Score: ai.Score, Late: ai.Late}
		}
		return x, err
	})
	if err != nil {
		return nil, wrap("scan submissions", err)
	}

	var ids []int64
	for _, x := range subs {
		if x.ID != 0 {
			ids = append(ids, x.ID)
		}
	}

	mrows, err := s.db.Query(ctx, `select m.submission_id, o.id, st.full_name, moodle.short_name(st.last_name, st.first_name, st.full_name), st.groups, o.submitted_at,
		m.code_similarity, m.report_similarity, m.identical_files, m.severity
		from integrity.integrity_matches m join moodle.submissions o on o.id = m.other_submission_id join moodle.students st on st.id = o.student_id
		where m.submission_id = any($1) order by o.submitted_at nulls last`, ids)
	if err != nil {
		return nil, wrap("list matches", err)
	}

	byID := map[int64][]grading.Match{}
	_, err = pgx.CollectRows(mrows, func(r pgx.CollectableRow) (struct{}, error) {
		var id int64
		var m grading.Match
		err := r.Scan(&id, &m.OtherID, &m.Student, &m.ShortName, &m.Groups, &m.SubmittedAt, &m.CodeSimilarity, &m.ReportSimilarity, &m.IdenticalFiles, &m.Severity)
		byID[id] = append(byID[id], m)
		return struct{}{}, err
	})
	for i := range subs {
		subs[i].Matches = byID[subs[i].ID]
	}

	return subs, wrap("scan matches", err)
}
