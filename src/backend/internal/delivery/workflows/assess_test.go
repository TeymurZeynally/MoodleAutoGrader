package workflows

import (
	"context"
	"errors"
	"testing"

	"moodle-autograder/internal/app/assessing"

	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

func checkEnv(t *testing.T) *testsuite.TestWorkflowEnvironment {
	t.Helper()
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	stubs := map[string]any{
		"QueueWorks":   func(context.Context, AssessInput) ([]int64, error) { return []int64{1}, nil },
		"EnsureRubric": func(context.Context, AssessInput) (int64, error) { return 5, nil },
		"AssessWork":   func(context.Context, WorkInput) error { return nil },
	}
	for name, fn := range stubs {
		env.RegisterActivityWithOptions(fn, activity.RegisterOptions{Name: name})
	}

	return env
}

func TestACheckAskedForDuringARunRunsRightAfterIt(t *testing.T) {
	env := checkEnv(t)
	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(RerunSignal, assessing.Job{AssignmentID: 2, Force: true, SubmissionIDs: []int64{3}})
		env.SignalWorkflow(RerunSignal, assessing.Job{AssignmentID: 2, Force: true, SubmissionIDs: []int64{4}})
	}, 0)
	env.ExecuteWorkflow(AssessAssignment, assessing.Job{AssignmentID: 2})

	var next *workflow.ContinueAsNewError
	if !errors.As(env.GetWorkflowError(), &next) {
		t.Fatalf("continues with the job asked for: %v", env.GetWorkflowError())
	}

	var job assessing.Job
	if err := converter.GetDefaultDataConverter().FromPayloads(next.Input, &job); err != nil {
		t.Fatal(err)
	}

	if len(job.SubmissionIDs) != 2 || job.SubmissionIDs[0] != 3 || job.SubmissionIDs[1] != 4 || !job.Force {
		t.Fatalf("both requests in one: %+v", job)
	}
}

func TestACheckWithoutRequestsEndsAsBefore(t *testing.T) {
	env := checkEnv(t)
	env.ExecuteWorkflow(AssessAssignment, assessing.Job{AssignmentID: 2})
	if !env.IsWorkflowCompleted() || env.GetWorkflowError() != nil {
		t.Fatalf("%v", env.GetWorkflowError())
	}
}
