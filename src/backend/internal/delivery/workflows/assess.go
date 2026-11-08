package workflows

import (
	"context"
	"time"

	"moodle-autograder/internal/app/assessing"

	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

// AssessInput is what the use cases of assessing hand over, and what SyncCourse starts for each assignment.
type AssessInput = assessing.Job

type WorkInput struct {
	SubmissionID, RubricID int64 // RubricID is the checklist; the field keeps its old name for running workflows
	Force                  bool
	Experiment             bool
	Models                 map[string]string
}

// RerunSignal brings a check the teacher asked for while this one runs (one runs per assignment): it starts as soon as
// this one ends. Several of them for chosen works join into one.
const RerunSignal = "rerun"

// AssessAssignment analyzes works one by one on the GPU (the llm worker runs one activity at a time). It runs apart from
// SyncCourse, so a sync finishes quickly and the analysis continues in the background.
func AssessAssignment(ctx workflow.Context, in AssessInput) error {
	return thenRerun(ctx, assessAssignment(ctx, in))
}

// thenRerun continues with the check that came by RerunSignal, if any. Without one it returns as before, so the
// histories of running workflows replay the same.
func thenRerun(ctx workflow.Context, err error) error {
	if err != nil {
		return err
	}

	signals := workflow.GetSignalChannel(ctx, RerunSignal)
	var next, more AssessInput
	if !signals.ReceiveAsync(&next) {
		return nil
	}

	for signals.ReceiveAsync(&more) {
		next = assessing.JoinJobs(next, more)
	}

	return workflow.NewContinueAsNewError(ctx, AssessAssignment, next)
}

func assessAssignment(ctx workflow.Context, in AssessInput) error {
	var a *Activities
	core := activityCtx(ctx, QueueCore, time.Minute)
	// Heartbeats let Temporal notice within 90 s that a worker died (an app restart), instead of waiting 40 minutes.
	gpu := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		TaskQueue: QueueLLM, StartToCloseTimeout: 40 * time.Minute, HeartbeatTimeout: 90 * time.Second,
		RetryPolicy: &temporal.RetryPolicy{InitialInterval: 10 * time.Second, MaximumAttempts: 3},
	})
	prog := SyncProgress{Stage: "EnsureRubric", Total: 1}
	if err := workflow.SetQueryHandler(ctx, ProgressQuery, func() (SyncProgress, error) { return prog, nil }); err != nil {
		return err
	}

	if in.FromSync {
		var approved bool
		if err := workflow.ExecuteActivity(core, a.ChecklistApproved, in.AssignmentID).Get(ctx, &approved); err != nil {
			return err
		}
		in.ChecklistOnly = !approved // build or refresh the checklist and wait for the teacher
	}

	var ids []int64
	if !in.ChecklistOnly {
		if err := workflow.ExecuteActivity(core, a.QueueWorks, in).Get(ctx, &ids); err != nil {
			return err
		}
	}

	var rubricID int64
	if err := workflow.ExecuteActivity(gpu, a.EnsureRubric, in).Get(ctx, &rubricID); err != nil || rubricID == 0 {
		msg := "the assignment has no description in Moodle or uses a scale: nothing to check against"
		if err != nil {
			msg = "could not build the checklist: " + err.Error()
		}
		if len(ids) > 0 && !in.Experiment {
			_ = workflow.ExecuteActivity(core, a.FailWorks, FailInput{ids, msg}).Get(ctx, nil)
		}
		return err
	}

	if in.ChecklistOnly {
		return nil
	}

	prog = SyncProgress{Stage: "AssessWork", Total: len(ids)}
	for _, id := range ids {
		// A failed work is recorded as failed by the activity; the others still run.
		_ = workflow.ExecuteActivity(gpu, a.AssessWork, WorkInput{SubmissionID: id, RubricID: rubricID, Force: in.Force || in.Experiment, Experiment: in.Experiment, Models: in.Models}).Get(ctx, nil)
		prog.Done++
	}

	return nil
}

// The activities below are thin: the AI check itself is app/assessing. Their names and inputs are part of running
// workflows' history, so they stay as they are.

// ChecklistApproved (core) reports whether the assignment has a checklist approved by the teacher that still matches
// the description; EnsureRubric replaces an outdated one with a new, unapproved checklist.
func (a *Activities) ChecklistApproved(ctx context.Context, assignmentID int64) (bool, error) {
	return a.Assess.ChecklistApproved(ctx, assignmentID)
}

type FailInput struct {
	SubmissionIDs []int64
	Error         string
}

// FailWorks (core) records why queued works were not analyzed, so none stays "queued" forever.
func (a *Activities) FailWorks(ctx context.Context, in FailInput) error {
	return a.Assess.FailWorks(ctx, in.SubmissionIDs, in.Error)
}

// QueueWorks (core) picks the works to analyze and marks them queued.
func (a *Activities) QueueWorks(ctx context.Context, in AssessInput) ([]int64, error) {
	return a.Assess.QueueWorks(ctx, in)
}

// alive sends activity heartbeats every 20 s until stop is called; model calls take minutes without other signs of life.
func alive(ctx context.Context) (stop func()) {
	done := make(chan struct{})
	go func() {
		t := time.NewTicker(20 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case <-t.C:
				activity.RecordHeartbeat(ctx)
			}
		}
	}()
	return func() { close(done) }
}

// EnsureRubric (llm) returns the checklist for the current assignment description, generating it when the
// description changed or when asked to. 0 means the assignment cannot be checked automatically. ("Rubric" is the
// old name of the checklist; the activity keeps it for running workflows.)
func (a *Activities) EnsureRubric(ctx context.Context, in AssessInput) (int64, error) {
	defer alive(ctx)()
	id, err := a.Assess.EnsureChecklist(ctx, in.AssignmentID, in.NewChecklist, in.Experiment)
	return id, done(err)
}

// AssessWork (llm) analyzes one work; the result or the error is stored on the work.
func (a *Activities) AssessWork(ctx context.Context, in WorkInput) error {
	defer alive(ctx)()
	return a.Assess.AssessWork(ctx, assessing.WorkRequest{SubmissionID: in.SubmissionID, ChecklistID: in.RubricID, Force: in.Force, Experiment: in.Experiment, Models: in.Models})
}
