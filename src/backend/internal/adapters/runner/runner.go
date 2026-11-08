// Package runner runs the background work of the use cases on Temporal: it implements the ports syncing.Runner,
// assessing.Runner, grading.Runner and courses.SyncRuns. It is the way out to Temporal; the way in, the workflows and
// activities Temporal calls, is delivery/workflows, as httpapi is the way in from a browser.
package runner

import (
	"context"
	"errors"
	"fmt"
	"time"

	"moodle-autograder/internal/app"
	"moodle-autograder/internal/app/assessing"
	"moodle-autograder/internal/app/courses"
	"moodle-autograder/internal/app/grading"
	"moodle-autograder/internal/app/syncing"

	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/api/serviceerror"
	"go.temporal.io/sdk/client"
)

// How Temporal finds the workflows of delivery/workflows. Running workflows hold these names, so they never change;
// runner_test.go compares them with the workflows.
const (
	queue           = "core"
	syncWorkflow    = "SyncCourse"
	assessWorkflow  = "AssessAssignment"
	publishWorkflow = "PublishGrades"
	progressQuery   = "progress"
	rerunSignal     = "rerun"
)

type Temporal struct{ Client client.Client }

// start is detached from the request: a closed browser tab does not cancel the start of a sync.
func (t Temporal) start(ctx context.Context, opts client.StartWorkflowOptions, workflow string, input any) error {
	opts.TaskQueue = queue
	if _, err := t.Client.ExecuteWorkflow(context.WithoutCancel(ctx), opts, workflow, input); err != nil {
		return fmt.Errorf("start workflow %s: %w", opts.ID, err)
	}

	return nil
}

func (t Temporal) Sync(ctx context.Context, runID string, job syncing.Job) error {
	return t.start(ctx, client.StartWorkflowOptions{ID: runID}, syncWorkflow, job)
}

func (t Temporal) Publish(ctx context.Context, userID int64, job grading.Job) error {
	return t.start(ctx, client.StartWorkflowOptions{ID: fmt.Sprintf("publish-%d-%d", userID, time.Now().UnixNano())}, publishWorkflow, job)
}

// CheckAssignment starts the check of an assignment, or, while one runs, hands the job to it to run next (queued).
func (t Temporal) CheckAssignment(ctx context.Context, job assessing.Job) (bool, error) {
	// Without WorkflowExecutionErrorWhenAlreadyStarted the SDK hides the conflict and returns the run already going.
	opts := client.StartWorkflowOptions{ID: assessing.AssignmentRunID(job.AssignmentID), WorkflowIDConflictPolicy: enumspb.WORKFLOW_ID_CONFLICT_POLICY_FAIL, WorkflowExecutionErrorWhenAlreadyStarted: true}
	for range 2 { // the running check may end between the two calls: then start again
		err := t.start(ctx, opts, assessWorkflow, job)
		if _, running := errors.AsType[*serviceerror.WorkflowExecutionAlreadyStarted](err); !running {
			return false, err
		}

		err = t.Client.SignalWorkflow(context.WithoutCancel(ctx), opts.ID, "", rerunSignal, job)
		if _, ended := errors.AsType[*serviceerror.NotFound](err); !ended {
			if err != nil {
				return false, fmt.Errorf("queue the check: %w", err)
			}
			return true, nil
		}
	}

	return false, app.Fail(app.ErrConflict, "the AI check of this assignment is starting and ending at once: try again")
}

func (t Temporal) CheckWork(ctx context.Context, job assessing.Job) error {
	kind := "assess"
	if job.Experiment {
		kind = "experiment"
	}

	return t.start(ctx, client.StartWorkflowOptions{ID: fmt.Sprintf("%s-submission-%d-%d", kind, job.SubmissionIDs[0], time.Now().UnixNano())}, assessWorkflow, job)
}

func (t Temporal) StopCheck(ctx context.Context, assignmentID int64) error {
	id := assessing.AssignmentRunID(assignmentID)
	err := t.Client.TerminateWorkflow(context.WithoutCancel(ctx), id, "", "stopped by the teacher")
	if _, missing := errors.AsType[*serviceerror.NotFound](err); missing {
		return nil
	}

	if err != nil {
		return fmt.Errorf("stop workflow %s: %w", id, err)
	}

	return nil
}

func (t Temporal) SyncRun(ctx context.Context, runID string) (courses.SyncRun, error) {
	return t.describe(ctx, runID)
}

func (t Temporal) CheckProgress(ctx context.Context, assignmentID int64) (assessing.Progress, bool, error) {
	run, err := t.describe(ctx, assessing.AssignmentRunID(assignmentID))
	if _, missing := errors.AsType[*serviceerror.NotFound](err); missing {
		return assessing.Progress{}, false, nil
	}

	if err != nil || !run.Running {
		return assessing.Progress{}, false, err
	}

	return assessing.Progress{Stage: run.Stage, Done: run.Done, Total: run.Total}, true, nil
}

// describe reads what a workflow is doing now: the progress it reports itself and the activities in flight.
func (t Temporal) describe(ctx context.Context, workflowID string) (courses.SyncRun, error) {
	var run courses.SyncRun
	resp, err := t.Client.DescribeWorkflowExecution(ctx, workflowID, "")
	if err != nil {
		return run, fmt.Errorf("describe workflow %s: %w", workflowID, err)
	}

	if status := resp.GetWorkflowExecutionInfo().GetStatus(); status != enumspb.WORKFLOW_EXECUTION_STATUS_RUNNING {
		run.Closed = status.String()
		return run, nil
	}

	run.Running = true
	var progress struct {
		Stage       string `json:"stage"`
		Done, Total int
	}
	if v, err := t.Client.QueryWorkflow(ctx, workflowID, "", progressQuery); err == nil {
		_ = v.Get(&progress)
	}

	run.Stage, run.Done, run.Total = progress.Stage, progress.Done, progress.Total
	inFlight := ""
	for _, a := range resp.GetPendingActivities() {
		name := a.GetActivityType().GetName()
		if inFlight == "" {
			inFlight = name
		}
		if name == inFlight {
			run.Pending++
		}
		if a.GetAttempt() > 1 && a.GetLastFailure() != nil {
			run.Retrying = append(run.Retrying, courses.Retry{Activity: name, Attempt: a.GetAttempt(), Error: a.GetLastFailure().GetMessage()})
		}
	}

	if run.Stage == "" { // a run started before the progress query existed
		run.Stage = inFlight
	}

	return run, nil
}
