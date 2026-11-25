package grading

import (
	"context"

	"moodle-autograder/internal/domain/assessment"
)

// Export is the sheet of an assignment for a teacher to read away from the application: the submitted works with their
// drafts, and what the AI check decided on each criterion.
type Export struct {
	Assignment Header
	Criteria   []assessment.Criterion // the checklist the AI checks with; empty without one
	Works      []ExportWork
}

// ExportWork is one submitted work: its row of the sheet and its AI check, nil when it was not analyzed.
type ExportWork struct {
	Row
	AI *assessment.Result
}

// Export gathers the submitted works of an assignment, in the order of its sheet.
func (s *Service) Export(ctx context.Context, assignmentID int64) (Export, error) {
	head, err := s.Store.Assignment(ctx, assignmentID)
	if err != nil {
		return Export{}, err
	}

	rows, err := s.Store.Submissions(ctx, assignmentID)
	if err != nil {
		return Export{}, err
	}

	results, err := s.Store.AIResults(ctx, assignmentID)
	if err != nil {
		return Export{}, err
	}

	checklist, ok, err := s.Store.LatestChecklist(ctx, assignmentID)
	if err != nil {
		return Export{}, err
	}

	s.linkGrader(rows)
	out := Export{Assignment: head}
	if ok {
		out.Criteria = checklist.Criteria
	}

	for _, r := range rows {
		if r.ID == 0 || r.MoodleStatus != "submitted" {
			continue
		}
		w := ExportWork{Row: r}
		if res, found := results[r.ID]; found {
			w.AI = &res
		}
		out.Works = append(out.Works, w)
	}

	return out, nil
}
