package workflows

import (
	"context"
	"fmt"

	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/worker"
)

// The queues of the two worker processes: one holds the Moodle tokens and reads no files of students, the other reads them
// and holds no tokens. Workflows run on QueueCore.
var (
	MoodleQueues = []string{QueueMoodleRead, QueueMoodleWrite}
	CoreQueues   = []string{QueueCore, QueueLLM}
)

// RunWorkers starts one worker per task queue given.
func RunWorkers(ctx context.Context, c client.Client, a *Activities, served []string) error {
	queues := map[string][]any{
		QueueCore:        {a.ProcessSubmission, a.CheckAssignment, a.FinishSync, a.QueueWorks, a.FailWorks, a.ChecklistApproved},
		QueueMoodleRead:  {a.FetchCourse, a.DownloadFiles},
		QueueMoodleWrite: {a.PublishGrade},
		QueueLLM:         {a.EnsureRubric, a.AssessWork},
	}
	names := map[string][]string{
		QueueCore: {"ProcessSubmission", "CheckAssignment", "FinishSync", "QueueWorks", "FailWorks", "ChecklistApproved"}, QueueMoodleRead: {"FetchCourse", "DownloadFiles"},
		QueueMoodleWrite: {"PublishGrade"}, QueueLLM: {"EnsureRubric", "AssessWork"},
	}
	var workers []worker.Worker
	for _, queue := range served {
		fns := queues[queue]
		opts := worker.Options{}
		if queue == QueueLLM {
			opts.MaxConcurrentActivityExecutionSize = 1 // one GPU: one model call chain at a time
		}
		w := worker.New(c, queue, opts)
		if queue == QueueCore {
			w.RegisterWorkflow(SyncCourse)
			w.RegisterWorkflow(PublishGrades)
			w.RegisterWorkflow(AssessAssignment)
		}
		for i, fn := range fns {
			w.RegisterActivityWithOptions(fn, activity.RegisterOptions{Name: names[queue][i]})
		}
		if err := w.Start(); err != nil {
			return fmt.Errorf("start worker %s: %w", queue, err)
		}
		workers = append(workers, w)
	}

	<-ctx.Done()
	for _, w := range workers {
		w.Stop()
	}

	return nil
}
