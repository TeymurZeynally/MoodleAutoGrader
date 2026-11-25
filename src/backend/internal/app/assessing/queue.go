package assessing

import (
	"context"
	"fmt"
	"maps"
	"slices"

	"moodle-autograder/internal/app"
)

// FailWorks records why queued works were not analyzed, so none stays "queued" forever.
func (s *Service) FailWorks(ctx context.Context, ids []int64, reason string) error {
	for _, id := range ids {
		if err := s.Store.FinishAssessment(ctx, id, "failed", "", 0, "", nil, nil, "", reason); err != nil {
			return err
		}
	}

	return nil
}

// QueueWorks picks the works of a job and marks them queued; an experiment leaves their status alone. Without models to
// run on nothing is queued: the works wait for the next sync, and the assignment's page says what is missing. A job
// that names no works takes those of the course's AI groups; works the teacher chose are checked whatever their group.
func (s *Service) QueueWorks(ctx context.Context, q Job) ([]int64, error) {
	if !s.Enabled() {
		return nil, nil
	}

	t, err := s.Store.AssignmentText(ctx, q.AssignmentID)
	if err != nil {
		return nil, err
	}

	if problem, err := s.modelsProblem(ctx, t.CourseID, q.AssignmentID, q.Models); err != nil || problem != "" {
		return nil, err
	}

	ids := q.SubmissionIDs
	if len(ids) == 0 {
		settings, err := s.Store.CourseSettings(ctx, t.CourseID)
		if err != nil {
			return nil, err
		}

		if ids, err = s.Store.SubmittedIDs(ctx, q.AssignmentID, q.IncludeGraded && !q.FromSync, settings.AIGroups); err != nil {
			return nil, err
		}
	}

	if q.Experiment {
		return ids, nil // the works keep their status: an experiment is not the AI check of record
	}

	return ids, s.Store.QueueAssessments(ctx, ids)
}

// AssignmentRunID is the id of the background check of an assignment: one id, so only one runs per assignment.
func AssignmentRunID(assignmentID int64) string {
	return fmt.Sprintf("assess-assignment-%d", assignmentID)
}

// JoinJobs is what two checks asked for one after the other become while a check runs: one over the works of both when
// they ask for the same, otherwise the later one, which the teacher asked for last.
func JoinJobs(a, b Job) Job {
	same := a.Force == b.Force && a.NewChecklist == b.NewChecklist && a.ChecklistOnly == b.ChecklistOnly && a.FromSync == b.FromSync &&
		a.IncludeGraded == b.IncludeGraded && a.Experiment == b.Experiment && maps.Equal(a.Models, b.Models)
	if !same {
		return b
	}

	if len(a.SubmissionIDs) == 0 || len(b.SubmissionIDs) == 0 {
		b.SubmissionIDs = nil // every work
		return b
	}

	ids := slices.Concat(a.SubmissionIDs, b.SubmissionIDs)
	slices.Sort(ids)
	b.SubmissionIDs = slices.Compact(ids)
	return b
}

// RerunRequest is a re-analysis of an assignment the teacher asked for.
type RerunRequest struct {
	SubmissionIDs []int64 // only these works; empty: every work
	FailedOnly    bool    // only the works whose last check failed
	NewChecklist  bool
	ChecklistOnly bool
	IncludeGraded bool
	Models        map[string]string // step -> model; empty: the course's models
}

// Rerun analyzes the works of an assignment again, whether or not they changed: every work, the chosen ones or the
// failed ones. Running the check by hand on the current checklist means the teacher accepts the checklist they see.
// While a check of the assignment runs, this one runs right after it (queued).
func (s *Service) Rerun(ctx context.Context, userID, assignmentID int64, r RerunRequest) (bool, error) {
	t, err := s.Store.AssignmentText(ctx, assignmentID)
	if err != nil {
		return false, err
	}

	if err := s.requireModels(ctx, t.CourseID, assignmentID, r.Models); err != nil {
		return false, err
	}

	if !r.ChecklistOnly {
		if err := s.requireFiles(ctx, t.CourseID); err != nil {
			return false, err
		}
	}

	job := Job{AssignmentID: assignmentID, Force: true, NewChecklist: r.NewChecklist, ChecklistOnly: r.ChecklistOnly, IncludeGraded: r.IncludeGraded, Models: r.Models}
	switch {
	case r.FailedOnly:
		ids, err := s.Store.FailedIDs(ctx, assignmentID)
		if err != nil {
			return false, err
		}
		if len(ids) == 0 {
			return false, app.Fail(app.ErrConflict, "no failed works to re-analyze")
		}
		job.SubmissionIDs, job.NewChecklist, job.ChecklistOnly = ids, false, false
	case len(r.SubmissionIDs) > 0:
		// Chosen works are checked on the checklist the teacher has seen: without one, the check would build it unseen.
		if _, ok, err := s.Store.LatestChecklist(ctx, assignmentID); err != nil {
			return false, err
		} else if !ok {
			return false, app.Fail(app.ErrConflict, "%s has no checklist yet: build it on the page of the assignment first", t.Name)
		}
		ids, err := s.chosen(ctx, assignmentID, r.SubmissionIDs)
		if err != nil {
			return false, err
		}
		job.SubmissionIDs, job.NewChecklist, job.ChecklistOnly = ids, false, false
	}

	if !job.ChecklistOnly && !job.NewChecklist {
		if err := s.Store.ApproveChecklist(ctx, assignmentID, userID); err != nil {
			return false, err
		}
	}

	return s.Runner.CheckAssignment(ctx, job)
}

// chosen keeps the works of the assignment among the ones the teacher picked: an id of another assignment's work is
// refused, as it would be checked against this checklist.
func (s *Service) chosen(ctx context.Context, assignmentID int64, picked []int64) ([]int64, error) {
	own, err := s.Store.SubmittedIDs(ctx, assignmentID, true, nil)
	if err != nil {
		return nil, err
	}

	for _, id := range picked {
		if !slices.Contains(own, id) {
			return nil, app.Fail(app.ErrInvalid, "work %d is not a submitted work of this assignment", id)
		}
	}

	ids := slices.Clone(picked)
	slices.Sort(ids)
	return slices.Compact(ids), nil
}

// Recheck analyzes one work again, on the course's models or on the chosen ones; either way the result becomes the
// current one.
func (s *Service) Recheck(ctx context.Context, submissionID int64, models map[string]string) error {
	return s.startWork(ctx, submissionID, Job{Force: true, Models: models})
}

// TryModels checks one work on other models and keeps the result as a run next to the current one.
func (s *Service) TryModels(ctx context.Context, submissionID int64, models map[string]string) error {
	if len(models) == 0 {
		return app.Fail(app.ErrInvalid, "choose the models to try")
	}

	return s.startWork(ctx, submissionID, Job{Experiment: true, Models: models})
}

func (s *Service) startWork(ctx context.Context, submissionID int64, job Job) error {
	info, err := s.Store.Submission(ctx, submissionID)
	if err != nil {
		return err
	}

	if err := s.requireModels(ctx, info.CourseID, info.AssignmentID, job.Models); err != nil {
		return err
	}

	if err := s.requireFiles(ctx, info.CourseID); err != nil {
		return err
	}

	job.AssignmentID, job.SubmissionIDs = info.AssignmentID, []int64{submissionID}
	return s.Runner.CheckWork(ctx, job)
}

// requireFiles refuses to check works whose files the course does not download.
func (s *Service) requireFiles(ctx context.Context, courseID int64) error {
	settings, err := s.Store.CourseSettings(ctx, courseID)
	if err != nil {
		return err
	}

	if !settings.DownloadFiles {
		return app.Fail(app.ErrConflict, `this course does not download files: turn on "Download files" in its settings and sync`)
	}

	return nil
}

// Stop stops the check of an assignment and takes its works off the queue; finished results stay.
func (s *Service) Stop(ctx context.Context, assignmentID int64) error {
	if err := s.Runner.StopCheck(ctx, assignmentID); err != nil {
		return err
	}

	return s.Store.ClearQueue(ctx, assignmentID)
}

// Status is what the model is doing now and how many works wait.
type Status struct {
	Enabled  bool      `json:"enabled"`
	Activity *Activity `json:"activity"`
	Queued   int       `json:"queued"`
}

func (s *Service) Status(ctx context.Context) (Status, error) {
	act, err := s.Store.CurrentActivity(ctx)
	if err != nil {
		return Status{}, err
	}

	n, err := s.Store.QueuedAssessments(ctx)
	return Status{Enabled: s.Enabled(), Activity: act, Queued: n}, err
}

// Result is the current AI check result of a work; app.ErrNotFound before the first check.
func (s *Service) Result(ctx context.Context, submissionID int64) (View, error) {
	v, ok, err := s.Store.Assessment(ctx, submissionID)
	if err == nil && !ok {
		err = app.Fail(app.ErrNotFound, "not analyzed yet")
	}

	return v, err
}

// Runs lists the AI checks of a work, newest first.
func (s *Service) Runs(ctx context.Context, submissionID int64) ([]Run, error) {
	return s.Store.Runs(ctx, submissionID)
}

// Promote makes a run the current result of its work and puts its points and comment into the draft.
func (s *Service) Promote(ctx context.Context, userID, runID int64) error {
	sub, res, comment, err := s.Store.PromoteRun(ctx, runID)
	if err != nil {
		return err
	}

	if err := s.Store.ApplyAIDraft(ctx, sub, res, comment); err != nil {
		return err
	}

	return s.Store.Audit(ctx, userID, "run.promote", "submission", sub, map[string]any{"run_id": runID})
}
