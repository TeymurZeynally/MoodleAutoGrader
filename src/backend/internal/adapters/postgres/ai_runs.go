package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"moodle-autograder/internal/app"
	"moodle-autograder/internal/app/assessing"
	"moodle-autograder/internal/domain/assessment"

	"github.com/jackc/pgx/v5"
)

// Schema ai: every AI check of a work is kept as a run, so results of different models can be compared.

// FinishExperiment stores a run with other models; the current result, the draft and the work's status stay as they are.
func (s *Store) FinishExperiment(ctx context.Context, submissionID int64, status, inputHash string, checklistID int64, model string, stepModels map[string]string, res *assessment.Result, comment, errMsg string, startedAt time.Time) error {
	raw, points, err := encodeResult(res)
	if err != nil {
		return err
	}

	id, err := insertRun(ctx, s.db, submissionID, true, status, inputHash, checklistID, model, stepModels, raw, points, comment, errMsg)
	if err != nil {
		return err
	}

	_, err = s.db.Exec(ctx, `update ai.assessment_runs set started_at = $2 where id = $1`, id, startedAt)
	return wrap("experiment start time", err)
}

func encodeResult(res *assessment.Result) ([]byte, *float64, error) {
	if res == nil {
		return nil, nil, nil
	}

	raw, err := json.Marshal(res)
	if err != nil {
		return nil, nil, fmt.Errorf("encode assessment: %w", err)
	}

	return raw, &res.Points, nil
}

// querier is a pool or a transaction.
type querier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

func insertRun(ctx context.Context, db querier, submissionID int64, experiment bool, status, inputHash string, checklistID int64, model string, stepModels map[string]string, raw []byte, points *float64, comment, errMsg string) (int64, error) {
	steps, err := json.Marshal(stepModels)
	if err != nil {
		return 0, fmt.Errorf("encode step models: %w", err)
	}

	var id int64
	err = db.QueryRow(ctx, `insert into ai.assessment_runs (submission_id, status, experiment, models, step_models, checklist_id, input_hash, result, points, comment, error, started_at)
		values ($1, $2, $3, $4, $5, nullif($6, 0), nullif($7, ''), $8, $9, nullif($10, ''), nullif($11, ''), (select started_at from ai.assessments where submission_id = $1))
		returning id`, submissionID, status, experiment, model, steps, checklistID, inputHash, raw, points, comment, errMsg).Scan(&id)
	return id, wrap("insert run", err)
}

// Runs lists the AI checks of a work, newest first.
func (s *Store) Runs(ctx context.Context, submissionID int64) ([]assessing.Run, error) {
	rows, err := s.db.Query(ctx, `select r.id, r.status, r.experiment, coalesce(a.run_id = r.id, false), r.models, r.step_models, r.points::float8, r.comment, r.error,
		r.started_at, r.finished_at, r.result from ai.assessment_runs r left join ai.assessments a on a.submission_id = r.submission_id
		where r.submission_id = $1 order by r.finished_at desc, r.id desc`, submissionID)
	if err != nil {
		return nil, wrap("runs", err)
	}

	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (assessing.Run, error) {
		var v assessing.Run
		err := r.Scan(&v.ID, &v.Status, &v.Experiment, &v.Current, &v.Models, &v.StepModels, &v.Points, &v.Comment, &v.Error, &v.StartedAt, &v.FinishedAt, &v.Result)
		return v, err
	})
}

// RunCourse returns the course of the work a run belongs to.
func (s *Store) RunCourse(ctx context.Context, runID int64) (int64, error) {
	return s.courseOf(ctx, "run course", `select a.course_id from ai.assessment_runs r join moodle.submissions s on s.id = r.submission_id
		join moodle.assignments a on a.id = s.assignment_id where r.id = $1`, runID)
}

// PromoteRun makes a finished run the current result of its work, with the run's input hash, so a sync does not
// analyze the work again. It returns the run for the draft.
func (s *Store) PromoteRun(ctx context.Context, runID int64) (submissionID int64, res *assessment.Result, comment string, err error) {
	var raw []byte
	err = s.db.QueryRow(ctx, `update ai.assessments a set status = r.status, input_hash = r.input_hash, checklist_id = r.checklist_id, model = r.models, result = r.result,
		points = r.points, comment = r.comment, error = null, finished_at = now(), run_id = r.id
		from ai.assessment_runs r where r.id = $1 and a.submission_id = r.submission_id and r.status <> 'failed' and r.result is not null
		returning a.submission_id, r.result, coalesce(r.comment, '')`, runID).Scan(&submissionID, &raw, &comment)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, nil, "", app.Fail(app.ErrConflict, "only a finished run of an analyzed work can become the current result")
	}

	if err != nil {
		return 0, nil, "", wrap("promote run", err)
	}

	res = &assessment.Result{}
	if err := json.Unmarshal(raw, res); err != nil {
		return 0, nil, "", fmt.Errorf("decode run: %w", err)
	}

	return submissionID, res, comment, nil
}
