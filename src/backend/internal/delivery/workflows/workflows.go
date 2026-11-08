// Package workflows holds the Temporal workflows (orchestration only) and activities.
// Activities run on the task queue of the role that holds the needed rights; payloads carry IDs, not content.
package workflows

import (
	"time"

	"moodle-autograder/internal/app/assessing"
	"moodle-autograder/internal/app/grading"
	"moodle-autograder/internal/app/syncing"

	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

const (
	QueueCore        = "core"
	QueueMoodleRead  = "moodle-read"
	QueueMoodleWrite = "moodle-write"
	QueueLLM         = "llm"
	parallelism      = 8 // concurrent activities per step: do not hammer Moodle
)

// SyncInput is what syncing.Start hands over; only moodle-* activities open its session.
type SyncInput = syncing.Job

type FetchResult struct {
	Assignments []int64
	Download    []int64 // submissions with new or changed files
	Process     []int64 // submissions to (re)extract
}

// SyncProgress is what a running SyncCourse reports through the "progress" query: the current stage and how many of
// its items are done in this run. Stages only move forward.
type SyncProgress struct {
	Stage       string `json:"stage"` // FetchCourse, DownloadFiles, ProcessSubmission, CheckAssignment, AssessSubmission, FinishSync
	Done, Total int
}

const ProgressQuery = "progress"

type SubmissionInput struct {
	SubmissionID int64
	Session      string
}

type FinishInput struct {
	CourseID int64
	Error    string
}

// PublishInput is what grading.StartPublish hands over.
type PublishInput = grading.Job

func activityCtx(ctx workflow.Context, queue string, timeout time.Duration) workflow.Context {
	return workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		TaskQueue: queue, StartToCloseTimeout: timeout,
		RetryPolicy: &temporal.RetryPolicy{InitialInterval: 2 * time.Second, BackoffCoefficient: 2, MaximumInterval: time.Minute, MaximumAttempts: 5},
	})
}

// SyncCourse runs a sync: Moodle metadata -> download changed files -> extract -> integrity per assignment -> LLM assessment (if enabled).
func SyncCourse(ctx workflow.Context, in SyncInput) error {
	var a *Activities
	read, core := activityCtx(ctx, QueueMoodleRead, 10*time.Minute), activityCtx(ctx, QueueCore, 10*time.Minute)
	prog := SyncProgress{Stage: "FetchCourse", Total: 1}
	if err := workflow.SetQueryHandler(ctx, ProgressQuery, func() (SyncProgress, error) { return prog, nil }); err != nil {
		return err
	}

	stage := func(name string, ids []int64, fn func(int64) workflow.Future) error {
		prog = SyncProgress{Stage: name, Total: len(ids)}
		return forAll(ctx, ids, &prog.Done, fn)
	}
	var res FetchResult
	err := workflow.ExecuteActivity(read, a.FetchCourse, in).Get(ctx, &res)
	if err == nil {
		err = stage("DownloadFiles", res.Download, func(id int64) workflow.Future {
			return workflow.ExecuteActivity(read, a.DownloadFiles, SubmissionInput{id, in.Session})
		})
	}

	if err == nil {
		err = stage("ProcessSubmission", res.Process, func(id int64) workflow.Future { return workflow.ExecuteActivity(core, a.ProcessSubmission, id) })
	}

	if err == nil {
		err = stage("CheckAssignment", res.Assignments, func(id int64) workflow.Future { return workflow.ExecuteActivity(core, a.CheckAssignment, id) })
	}

	if err == nil {
		// The AI check runs on its own (hours on one GPU); new or changed works are found by their input hash.
		prog = SyncProgress{Stage: "StartAssess", Total: len(res.Assignments)}
		for _, id := range res.Assignments {
			StartAssess(ctx, AssessInput{AssignmentID: id, FromSync: true})
			prog.Done++
		}
	}

	prog = SyncProgress{Stage: "FinishSync", Total: 1}
	msg := ""
	if err != nil {
		msg = err.Error()
	}

	if ferr := workflow.ExecuteActivity(core, a.FinishSync, FinishInput{in.CourseID, msg}).Get(ctx, nil); err == nil {
		err = ferr
	}

	return err
}

// PublishGrades publishes approved snapshots one by one; a failure of one student does not stop the others.
func PublishGrades(ctx workflow.Context, in PublishInput) error {
	var a *Activities
	write := activityCtx(ctx, QueueMoodleWrite, 2*time.Minute)
	for _, id := range in.ApprovalIDs {
		_ = workflow.ExecuteActivity(write, a.PublishGrade, SubmissionInput{id, in.Session}).Get(ctx, nil) // failures are recorded on the draft
	}

	return nil
}

// forAll runs fn for every id with bounded parallelism and returns the first error.
// forAll runs fn for every id, parallelism at a time, counting finished items in done.
func forAll(ctx workflow.Context, ids []int64, done *int, fn func(int64) workflow.Future) error {
	for start := 0; start < len(ids); start += parallelism {
		var futures []workflow.Future
		for _, id := range ids[start:min(start+parallelism, len(ids))] {
			futures = append(futures, fn(id))
		}
		for _, f := range futures {
			if err := f.Get(ctx, nil); err != nil {
				return err
			}
			*done++
		}
	}

	return nil
}

// StartAssess starts AssessAssignment as an abandoned child: it outlives the sync. If an analysis of the assignment is
// already running, nothing is started; the next sync picks up what it missed.
func StartAssess(ctx workflow.Context, in AssessInput) {
	cctx := workflow.WithChildOptions(ctx, workflow.ChildWorkflowOptions{
		WorkflowID: assessing.AssignmentRunID(in.AssignmentID), TaskQueue: QueueCore,
		ParentClosePolicy: enumspb.PARENT_CLOSE_POLICY_ABANDON, WorkflowIDReusePolicy: enumspb.WORKFLOW_ID_REUSE_POLICY_ALLOW_DUPLICATE,
	})
	_ = workflow.ExecuteChildWorkflow(cctx, AssessAssignment, in).GetChildWorkflowExecution().Get(ctx, nil)
}
