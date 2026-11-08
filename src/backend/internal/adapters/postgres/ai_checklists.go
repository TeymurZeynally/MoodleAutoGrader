package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"moodle-autograder/internal/domain/assessment"

	"github.com/jackc/pgx/v5"
)

// Schema ai: checklists of assignments, written by the model or the teacher.

// LatestChecklist returns the newest checklist of an assignment; ok is false when there is none yet.
func (s *Store) LatestChecklist(ctx context.Context, assignmentID int64) (assessment.Checklist, bool, error) {
	var r assessment.Checklist
	var raw, extras []byte
	err := s.db.QueryRow(ctx, `select id, intro_hash, source, model, criteria, extras, created_at, approved_at is not null
		from ai.checklists where assignment_id = $1 order by id desc limit 1`, assignmentID).
		Scan(&r.ID, &r.IntroHash, &r.Source, &r.Model, &raw, &extras, &r.CreatedAt, &r.Approved)
	if errors.Is(err, pgx.ErrNoRows) {
		return r, false, nil
	}

	if err != nil {
		return r, false, wrap("latest checklist", err)
	}

	if err := json.Unmarshal(extras, &r.Extras); err != nil {
		return r, false, fmt.Errorf("decode extras: %w", err)
	}

	return r, true, wrap("decode checklist", json.Unmarshal(raw, &r.Criteria))
}

// SaveChecklist stores a checklist; source is "model" or "teacher" (userID set for the teacher's, approved at once).
func (s *Store) SaveChecklist(ctx context.Context, assignmentID int64, introHash, source, model string, userID *int64, criteria, extras []assessment.Criterion) (int64, error) {
	raw, err := json.Marshal(criteria)
	if err != nil {
		return 0, fmt.Errorf("encode checklist: %w", err)
	}

	ex, err := json.Marshal(nonNil(extras))
	if err != nil {
		return 0, fmt.Errorf("encode extras: %w", err)
	}

	var id int64
	err = s.db.QueryRow(ctx, `insert into ai.checklists (assignment_id, intro_hash, source, model, created_by, criteria, extras, approved_at, approved_by)
		values ($1, $2, $3, $4, $5, $6, $7, case when $3 = 'teacher' then now() end, case when $3 = 'teacher' then $5::bigint end) returning id`,
		assignmentID, introHash, source, model, userID, raw, ex).Scan(&id)
	return id, wrap("save checklist", err)
}

// ApproveChecklist marks the latest checklist of an assignment as approved by the teacher.
func (s *Store) ApproveChecklist(ctx context.Context, assignmentID, userID int64) error {
	_, err := s.db.Exec(ctx, `update ai.checklists set approved_at = coalesce(approved_at, now()), approved_by = coalesce(approved_by, $2)
		where id = (select max(id) from ai.checklists where assignment_id = $1)`, assignmentID, userID)
	return wrap("approve checklist", err)
}
