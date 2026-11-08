package postgres

import (
	"context"
	"encoding/json"
	"errors"

	"moodle-autograder/internal/app/assessing"
	"moodle-autograder/internal/domain/assessment"

	"github.com/jackc/pgx/v5"
)

// Schema ai: the current AI check result of every work, the queue, and what the model is doing right now.

// statusOfResult is the status a stored result deserves: needs_attention when it has reasons to look or a suspected
// prompt injection, done otherwise.
const statusOfResult = `case when (jsonb_typeof(result->'attention') = 'array' and jsonb_array_length(result->'attention') > 0)
	or coalesce((result->'injection'->>'suspected')::boolean, false) then 'needs_attention' else 'done' end`

// AssessmentHash returns the input hash of the last finished analysis ("" when none), whatever the current status.
func (s *Store) AssessmentHash(ctx context.Context, submissionID int64) (string, error) {
	var h string
	err := s.db.QueryRow(ctx, `select coalesce(input_hash, '') from ai.assessments where submission_id = $1 and result is not null`, submissionID).Scan(&h)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}

	return h, wrap("assessment hash", err)
}

// QueueAssessments marks works as waiting for analysis, keeping their last result visible until it is replaced.
func (s *Store) QueueAssessments(ctx context.Context, ids []int64) error {
	_, err := s.db.Exec(ctx, `insert into ai.assessments (submission_id, status) select unnest($1::bigint[]), 'queued'
		on conflict (submission_id) do update set status = 'queued', queued_at = now(), error = null`, ids)
	return wrap("queue assessments", err)
}

func (s *Store) StartAssessment(ctx context.Context, submissionID int64) error {
	_, err := s.db.Exec(ctx, `insert into ai.assessments (submission_id, status, started_at) values ($1, 'running', now())
		on conflict (submission_id) do update set status = 'running', started_at = now(), error = null`, submissionID)
	return wrap("start assessment", err)
}

// FinishAssessment stores the result as a new run and makes it current; status is done, needs_attention or failed
// (with errMsg). A failed run keeps the previous result current.
func (s *Store) FinishAssessment(ctx context.Context, submissionID int64, status, inputHash string, checklistID int64, model string, stepModels map[string]string, res *assessment.Result, comment, errMsg string) error {
	raw, points, err := encodeResult(res)
	if err != nil {
		return err
	}

	tx, err := s.db.Begin(ctx)
	if err != nil {
		return wrap("begin", err)
	}

	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	runID, err := insertRun(ctx, tx, submissionID, false, status, inputHash, checklistID, model, stepModels, raw, points, comment, errMsg)
	if err != nil {
		return err
	}

	if res != nil && status != "failed" {
		met := 0
		for _, c := range res.Criteria {
			if c.Status == "met" {
				met++
			}
		}
		details, _ := json.Marshal(map[string]any{"score": res.Score, "late_penalty": res.LatePenalty, "met": met, "total": len(res.Criteria), "model": model, "status": status, "run_id": runID})
		if _, err := tx.Exec(ctx, `insert into grading.review_events (submission_id, kind, at, points, comment, details) values ($1, 'ai', now(), $2, $3, $4)`,
			submissionID, res.Points, comment, details); err != nil {
			return wrap("record ai check", err)
		}
	}

	_, err = tx.Exec(ctx, `update ai.assessments set status = $2, input_hash = case when $2 = 'failed' then input_hash else nullif($3, '') end,
		checklist_id = case when $2 = 'failed' then checklist_id else nullif($4, 0) end, model = $5,
		result = case when $2 = 'failed' then result else $6 end, points = case when $2 = 'failed' then points else $7 end,
		comment = case when $2 = 'failed' then comment else $8 end, error = nullif($9, ''), finished_at = now(),
		run_id = case when $2 = 'failed' then run_id else $10 end where submission_id = $1`,
		submissionID, status, inputHash, checklistID, model, raw, points, comment, errMsg, runID)
	if err != nil {
		return wrap("finish assessment", err)
	}

	return wrap("commit", tx.Commit(ctx))
}

// SkipAssessment marks an unchanged work as analyzed again without calling the model.
func (s *Store) SkipAssessment(ctx context.Context, submissionID int64) error {
	_, err := s.db.Exec(ctx, `update ai.assessments set status = `+statusOfResult+` where submission_id = $1`, submissionID)
	return wrap("skip assessment", err)
}

func (s *Store) Assessment(ctx context.Context, submissionID int64) (assessing.View, bool, error) {
	var a assessing.View
	err := s.db.QueryRow(ctx, `select status, model, points::float8, comment, error, finished_at, result from ai.assessments where submission_id = $1`, submissionID).
		Scan(&a.Status, &a.Model, &a.Points, &a.Comment, &a.Error, &a.FinishedAt, &a.Result)
	if errors.Is(err, pgx.ErrNoRows) {
		return a, false, nil
	}

	return a, err == nil, wrap("assessment", err)
}

// QueuedAssessments counts works waiting for the AI check.
func (s *Store) QueuedAssessments(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRow(ctx, `select count(*) from ai.assessments where status = 'queued'`).Scan(&n)
	return n, wrap("queued assessments", err)
}

// ClearQueue takes the works of an assignment off the AI check queue: works never analyzed are removed, the others get
// back the status of their last result.
func (s *Store) ClearQueue(ctx context.Context, assignmentID int64) error {
	where := `submission_id in (select id from moodle.submissions where assignment_id = $1) and status in ('queued', 'running')`
	if _, err := s.db.Exec(ctx, `delete from ai.assessments where result is null and `+where, assignmentID); err != nil {
		return wrap("clear queue", err)
	}

	if _, err := s.db.Exec(ctx, `update ai.assessments set status = `+statusOfResult+` where `+where, assignmentID); err != nil {
		return wrap("restore statuses", err)
	}

	_, err := s.db.Exec(ctx, `delete from ai.ai_activity where assignment_id = $1`, assignmentID)
	return wrap("clear activity", err)
}

// FailedIDs lists the submitted works of an assignment whose last AI check failed; an attempt the student has submitted
// again since does not count.
func (s *Store) FailedIDs(ctx context.Context, assignmentID int64) ([]int64, error) {
	rows, err := s.db.Query(ctx, `select s.id from moodle.submissions s join ai.assessments a on a.submission_id = s.id
		where s.assignment_id = $1 and s.moodle_status = 'submitted' and a.status = 'failed'
		and not exists (select 1 from moodle.submissions n where n.assignment_id = s.assignment_id and n.student_id = s.student_id and n.attempt > s.attempt and n.moodle_status = 'submitted')
		order by s.submitted_at nulls last, s.id`, assignmentID)
	if err != nil {
		return nil, wrap("failed ids", err)
	}

	ids, err := pgx.CollectRows(rows, pgx.RowTo[int64])
	return ids, wrap("scan failed ids", err)
}

// SetActivity records the job the model is busy with; step changes keep started_at.
func (s *Store) SetActivity(ctx context.Context, kind string, assignmentID, submissionID int64, step string) error {
	_, err := s.db.Exec(ctx, `insert into ai.ai_activity (id, kind, assignment_id, submission_id, step) values (1, $1, nullif($2, 0), nullif($3, 0), $4)
		on conflict (id) do update set step = excluded.step, updated_at = now(),
		started_at = case when ai_activity.kind = excluded.kind and ai_activity.assignment_id is not distinct from excluded.assignment_id
			and ai_activity.submission_id is not distinct from excluded.submission_id then ai_activity.started_at else now() end,
		kind = excluded.kind, assignment_id = excluded.assignment_id, submission_id = excluded.submission_id`, kind, assignmentID, submissionID, step)
	return wrap("set ai activity", err)
}

func (s *Store) ClearActivity(ctx context.Context) error {
	_, err := s.db.Exec(ctx, `delete from ai.ai_activity`)
	return wrap("clear ai activity", err)
}

// CurrentActivity returns the model's current job, or nil when it is idle.
func (s *Store) CurrentActivity(ctx context.Context) (*assessing.Activity, error) {
	var a assessing.Activity
	err := s.db.QueryRow(ctx, `select x.kind, x.assignment_id, asg.name, asg.course_id, x.submission_id, moodle.short_name(st.last_name, st.first_name, st.full_name),
		x.step, x.started_at, x.updated_at
		from ai.ai_activity x left join moodle.assignments asg on asg.id = x.assignment_id left join moodle.submissions sb on sb.id = x.submission_id
		left join moodle.students st on st.id = sb.student_id`).Scan(&a.Kind, &a.AssignmentID, &a.Assignment, &a.CourseID, &a.SubmissionID, &a.Student, &a.Step, &a.StartedAt, &a.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}

	if err != nil {
		return nil, wrap("ai activity", err)
	}

	return &a, nil
}
