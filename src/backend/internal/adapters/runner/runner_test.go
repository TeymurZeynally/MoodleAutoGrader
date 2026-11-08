package runner

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"moodle-autograder/internal/app"
	"moodle-autograder/internal/app/assessing"
	"moodle-autograder/internal/app/grading"
	"moodle-autograder/internal/app/syncing"
	"moodle-autograder/internal/delivery/workflows"

	commonpb "go.temporal.io/api/common/v1"
	enumspb "go.temporal.io/api/enums/v1"
	failurepb "go.temporal.io/api/failure/v1"
	"go.temporal.io/api/serviceerror"
	workflowpb "go.temporal.io/api/workflow/v1"
	"go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/converter"
)

type fakeClient struct {
	client.Client
	opts         client.StartWorkflowOptions
	workflow     any
	input        any
	cancelled    bool
	startErr     error
	alreadyRuns  bool
	terminated   string
	terminateErr error
	described    *workflowservice.DescribeWorkflowExecutionResponse
	describeErr  error
	query        string
	progress     any
	signalled    string
	signalJob    any
	signalErrs   []error // one per SignalWorkflow call
}

func (f *fakeClient) SignalWorkflow(ctx context.Context, id, _, signal string, arg any) error {
	f.signalled, f.signalJob, f.cancelled = id+"/"+signal, arg, ctx.Err() != nil
	if len(f.signalErrs) == 0 {
		return nil
	}

	err := f.signalErrs[0]
	f.signalErrs = f.signalErrs[1:]
	if err != nil {
		f.alreadyRuns = false // the running check ended
	}

	return err
}

func (f *fakeClient) ExecuteWorkflow(ctx context.Context, opts client.StartWorkflowOptions, workflow any, args ...any) (client.WorkflowRun, error) {
	f.opts, f.workflow, f.input, f.cancelled = opts, workflow, args[0], ctx.Err() != nil
	if f.alreadyRuns && opts.WorkflowExecutionErrorWhenAlreadyStarted { // as the SDK: otherwise it returns the running workflow
		return nil, &serviceerror.WorkflowExecutionAlreadyStarted{Message: "running"}
	}

	return nil, f.startErr
}

func (f *fakeClient) TerminateWorkflow(ctx context.Context, id, _, _ string, _ ...any) error {
	f.terminated, f.cancelled = id, ctx.Err() != nil
	return f.terminateErr
}

func (f *fakeClient) DescribeWorkflowExecution(context.Context, string, string) (*workflowservice.DescribeWorkflowExecutionResponse, error) {
	return f.described, f.describeErr
}

func (f *fakeClient) QueryWorkflow(_ context.Context, _, _, query string, _ ...any) (converter.EncodedValue, error) {
	f.query = query
	if f.progress == nil {
		return nil, errors.New("no such query")
	}

	raw, err := json.Marshal(f.progress)
	return jsonValue(raw), err
}

type jsonValue []byte

func (v jsonValue) HasValue() bool    { return true }
func (v jsonValue) Get(out any) error { return json.Unmarshal(v, out) }

func functionName(fn any) string {
	name := runtime.FuncForPC(reflect.ValueOf(fn).Pointer()).Name()
	return name[strings.LastIndexByte(name, '.')+1:]
}

// The names and types the adapter uses must be what delivery/workflows registers: nothing checks a name given as a
// string until the workflow fails to start in production.
func TestStartsTheWorkflowsTheWorkersRegister(t *testing.T) {
	ctx := context.Background()
	for _, c := range []struct {
		name string
		fn   any
		call func(Temporal) error
	}{
		{"sync", workflows.SyncCourse, func(r Temporal) error { return r.Sync(ctx, "run", syncing.Job{CourseID: 1}) }},
		{"publish", workflows.PublishGrades, func(r Temporal) error { return r.Publish(ctx, 1, grading.Job{ApprovalIDs: []int64{1}}) }},
		{"assignment", workflows.AssessAssignment, func(r Temporal) error { _, err := r.CheckAssignment(ctx, assessing.Job{AssignmentID: 1}); return err }},
		{"work", workflows.AssessAssignment, func(r Temporal) error { return r.CheckWork(ctx, assessing.Job{SubmissionIDs: []int64{1}}) }},
	} {
		f := &fakeClient{}
		if err := c.call(Temporal{Client: f}); err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if f.workflow != functionName(c.fn) {
			t.Errorf("%s: starts %v, the worker registers %s", c.name, f.workflow, functionName(c.fn))
		}
		if f.opts.TaskQueue != workflows.QueueCore {
			t.Errorf("%s: queue %s, workflows run on %s", c.name, f.opts.TaskQueue, workflows.QueueCore)
		}
		if got, want := reflect.TypeOf(f.input), reflect.TypeOf(c.fn).In(1); got != want {
			t.Errorf("%s: input %v, the workflow takes %v", c.name, got, want)
		}
	}

	if progressQuery != workflows.ProgressQuery || rerunSignal != workflows.RerunSignal {
		t.Errorf("query %s, signal %s; workflows answer %s, listen to %s", progressQuery, rerunSignal, workflows.ProgressQuery, workflows.RerunSignal)
	}
}

// Running workflows keep their input in Temporal's history as JSON. A renamed field would silently decode to zero.
func TestWorkflowInputsKeepTheirFormat(t *testing.T) {
	for _, c := range []struct {
		input any
		want  string
	}{
		{workflows.SyncInput{CourseID: 1, Session: "s"}, `{"CourseID":1,"Session":"s"}`},
		{workflows.PublishInput{ApprovalIDs: []int64{1}, Session: "s"}, `{"ApprovalIDs":[1],"Session":"s"}`},
		{
			workflows.AssessInput{AssignmentID: 1, SubmissionIDs: []int64{2}, Force: true, NewChecklist: true, ChecklistOnly: true, FromSync: true, IncludeGraded: true, Experiment: true, Models: map[string]string{"check": "m"}},
			`{"AssignmentID":1,"SubmissionIDs":[2],"Force":true,"NewChecklist":true,"ChecklistOnly":true,"FromSync":true,"IncludeGraded":true,"Experiment":true,"Models":{"check":"m"}}`,
		},
	} {
		got, err := json.Marshal(c.input)
		if err != nil || string(got) != c.want {
			t.Errorf("%T: %s (%v), running workflows hold %s", c.input, got, err, c.want)
		}
	}
}

func TestRunIDs(t *testing.T) {
	ctx := context.Background()
	f := &fakeClient{}
	r := Temporal{Client: f}
	if _ = r.Sync(ctx, "sync-course-1-2", syncing.Job{}); f.opts.ID != "sync-course-1-2" {
		t.Errorf("sync: %s", f.opts.ID)
	}

	if _, _ = r.CheckAssignment(ctx, assessing.Job{AssignmentID: 7}); f.opts.ID != "assess-assignment-7" || f.opts.WorkflowIDConflictPolicy != enumspb.WORKFLOW_ID_CONFLICT_POLICY_FAIL {
		t.Errorf("one check per assignment: %s %v", f.opts.ID, f.opts.WorkflowIDConflictPolicy)
	}

	if _ = r.CheckWork(ctx, assessing.Job{SubmissionIDs: []int64{5}}); !strings.HasPrefix(f.opts.ID, "assess-submission-5-") {
		t.Errorf("work: %s", f.opts.ID)
	}

	if _ = r.CheckWork(ctx, assessing.Job{SubmissionIDs: []int64{5}, Experiment: true}); !strings.HasPrefix(f.opts.ID, "experiment-submission-5-") {
		t.Errorf("experiment: %s", f.opts.ID)
	}

	if _ = r.Publish(ctx, 3, grading.Job{}); !strings.HasPrefix(f.opts.ID, "publish-3-") {
		t.Errorf("publish: %s", f.opts.ID)
	}
}

func TestAClosedTabDoesNotCancelStartOrStop(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	f := &fakeClient{}
	r := Temporal{Client: f}
	if _ = r.Sync(ctx, "run", syncing.Job{}); f.cancelled {
		t.Error("start got a cancelled context")
	}

	if _ = r.StopCheck(ctx, 7); f.cancelled || f.terminated != "assess-assignment-7" {
		t.Errorf("stop: cancelled %v, terminated %q", f.cancelled, f.terminated)
	}
}

func TestACheckAskedForWhileOneRunsRunsNext(t *testing.T) {
	f := &fakeClient{alreadyRuns: true}
	job := assessing.Job{AssignmentID: 7, SubmissionIDs: []int64{3}}
	queued, err := Temporal{Client: f}.CheckAssignment(context.Background(), job)
	if err != nil || !queued || f.signalled != "assess-assignment-7/"+workflows.RerunSignal || !reflect.DeepEqual(f.signalJob, job) {
		t.Fatalf("queued %v, err %v, signal %q %+v", queued, err, f.signalled, f.signalJob)
	}

	f = &fakeClient{alreadyRuns: true, signalErrs: []error{&serviceerror.NotFound{Message: "ended"}}}
	if queued, err := (Temporal{Client: f}).CheckAssignment(context.Background(), job); err != nil || queued {
		t.Fatalf("the running check ended in between: started instead, queued %v, err %v", queued, err)
	}

	f = &fakeClient{startErr: errors.New("down")}
	if _, err := (Temporal{Client: f}).CheckAssignment(context.Background(), job); err == nil || errors.Is(err, app.ErrConflict) {
		t.Fatalf("other errors are internal: %v", err)
	}
}

func TestStoppingACheckThatDoesNotRunIsFine(t *testing.T) {
	f := &fakeClient{terminateErr: &serviceerror.NotFound{Message: "gone"}}
	if err := (Temporal{Client: f}).StopCheck(context.Background(), 7); err != nil {
		t.Fatal(err)
	}

	f.terminateErr = errors.New("down")
	if err := (Temporal{Client: f}).StopCheck(context.Background(), 7); err == nil {
		t.Fatal("a failure to stop is an error")
	}
}

func described(status enumspb.WorkflowExecutionStatus, pending ...*workflowpb.PendingActivityInfo) *workflowservice.DescribeWorkflowExecutionResponse {
	return &workflowservice.DescribeWorkflowExecutionResponse{WorkflowExecutionInfo: &workflowpb.WorkflowExecutionInfo{Status: status}, PendingActivities: pending}
}

func TestSyncRunReadsTheProgressTheWorkflowReports(t *testing.T) {
	retrying := &workflowpb.PendingActivityInfo{ActivityType: &commonpb.ActivityType{Name: "DownloadFiles"}, Attempt: 3, LastFailure: &failurepb.Failure{Message: "timeout"}}
	first := &workflowpb.PendingActivityInfo{ActivityType: &commonpb.ActivityType{Name: "DownloadFiles"}, Attempt: 1}
	f := &fakeClient{described: described(enumspb.WORKFLOW_EXECUTION_STATUS_RUNNING, first, retrying), progress: workflows.SyncProgress{Stage: "DownloadFiles", Done: 2, Total: 5}}
	run, err := Temporal{Client: f}.SyncRun(context.Background(), "run")
	if err != nil || !run.Running || run.Stage != "DownloadFiles" || run.Done != 2 || run.Total != 5 || run.Pending != 2 {
		t.Fatalf("%+v %v", run, err)
	}

	if f.query != workflows.ProgressQuery || len(run.Retrying) != 1 || run.Retrying[0].Attempt != 3 || run.Retrying[0].Error != "timeout" {
		t.Fatalf("%s %+v", f.query, run.Retrying)
	}
}

func TestARunWithoutTheQueryShowsTheActivityInFlight(t *testing.T) {
	f := &fakeClient{described: described(enumspb.WORKFLOW_EXECUTION_STATUS_RUNNING, &workflowpb.PendingActivityInfo{ActivityType: &commonpb.ActivityType{Name: "FetchCourse"}})}
	run, err := Temporal{Client: f}.SyncRun(context.Background(), "run")
	if err != nil || run.Stage != "FetchCourse" {
		t.Fatalf("%+v %v", run, err)
	}
}

func TestAClosedRunTellsHowItEnded(t *testing.T) {
	f := &fakeClient{described: described(enumspb.WORKFLOW_EXECUTION_STATUS_TERMINATED)}
	run, err := Temporal{Client: f}.SyncRun(context.Background(), "run")
	if err != nil || run.Running || run.Closed != "Terminated" {
		t.Fatalf("%+v %v", run, err)
	}
}

func TestCheckProgress(t *testing.T) {
	ctx := context.Background()
	f := &fakeClient{describeErr: &serviceerror.NotFound{Message: "never ran"}}
	r := Temporal{Client: f}
	if _, running, err := r.CheckProgress(ctx, 7); running || err != nil {
		t.Fatalf("an assignment never checked: %v %v", running, err)
	}

	f.describeErr, f.described = nil, described(enumspb.WORKFLOW_EXECUTION_STATUS_COMPLETED)
	if _, running, err := r.CheckProgress(ctx, 7); running || err != nil {
		t.Fatalf("a finished check: %v %v", running, err)
	}

	f.described, f.progress = described(enumspb.WORKFLOW_EXECUTION_STATUS_RUNNING), workflows.SyncProgress{Stage: "AssessWork", Done: 1, Total: 3}
	if p, running, err := r.CheckProgress(ctx, 7); !running || err != nil || p != (assessing.Progress{Stage: "AssessWork", Done: 1, Total: 3}) {
		t.Fatalf("%+v %v %v", p, running, err)
	}

	f.describeErr = errors.New("down")
	if _, running, err := r.CheckProgress(ctx, 7); running || err == nil {
		t.Fatalf("Temporal is down: %v %v", running, err)
	}
}
