package assessing

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"moodle-autograder/internal/app"
	"moodle-autograder/internal/domain/assessment"
	"moodle-autograder/internal/domain/course"
	"moodle-autograder/internal/domain/work"
)

type startStore struct {
	Store
	failed      []int64
	submitted   []int64
	approved    int
	cleared     int
	order       *[]string
	noModels    bool
	courseModel string
	maxGrade    *float64
	intro       string
}

func (f *startStore) ClearActivity(context.Context) error { return nil }

func (f *startStore) FailedIDs(context.Context, int64) ([]int64, error) { return f.failed, nil }
func (f *startStore) SubmittedIDs(context.Context, int64, bool) ([]int64, error) {
	return f.submitted, nil
}

func (f *startStore) ApproveChecklist(context.Context, int64, int64) error {
	f.approved++
	return nil
}

func (f *startStore) ClearQueue(context.Context, int64) error {
	f.cleared++
	*f.order = append(*f.order, "clear")
	return nil
}

func (f *startStore) Submission(context.Context, int64) (work.Submission, error) {
	return work.Submission{ID: 9, AssignmentID: 2}, nil
}

func (f *startStore) LatestChecklist(context.Context, int64) (assessment.Checklist, bool, error) {
	return assessment.Checklist{}, false, nil
}

func (f *startStore) AssignmentText(context.Context, int64) (work.Assignment, error) {
	return work.Assignment{Name: "Lab", Intro: f.intro, MaxGrade: f.maxGrade}, nil
}

func (f *startStore) CourseSettings(context.Context, int64) (course.Settings, error) {
	return course.Defaults(), nil
}

type fakeRunner struct {
	assignment, one []Job
	err             error
	progress        Progress
	running         bool
	progressErr     error
	order           *[]string
}

func (f *fakeRunner) CheckAssignment(_ context.Context, job Job) (bool, error) {
	f.assignment = append(f.assignment, job)
	return false, f.err
}

func rerun(ctx context.Context, s *Service, userID, assignmentID int64, r RerunRequest) error {
	_, err := s.Rerun(ctx, userID, assignmentID, r)
	return err
}

func (f *fakeRunner) CheckWork(_ context.Context, job Job) error {
	f.one = append(f.one, job)
	return f.err
}

func (f *fakeRunner) StopCheck(context.Context, int64) error {
	*f.order = append(*f.order, "stop")
	return f.err
}

func (f *fakeRunner) CheckProgress(context.Context, int64) (Progress, bool, error) {
	return f.progress, f.running, f.progressErr
}

type fakeModels []string

func (f fakeModels) Models(context.Context) ([]string, error) { return f, nil }

// fakePrompts knows two steps; a course's models are its overrides.
type fakePrompts struct{ Prompts }

func (fakePrompts) Steps() []PromptStep                                { return []PromptStep{{Key: "rubric"}, {Key: "check"}} }
func (fakePrompts) Effective(over map[string]string) map[string]string { return over }

func (f *startStore) PromptOverrides(context.Context, int64) (map[string]string, error) {
	if f.noModels {
		return map[string]string{}, nil
	}

	if f.courseModel != "" {
		return map[string]string{"rubric.model": f.courseModel, "check.model": f.courseModel}, nil
	}

	return map[string]string{"rubric.model": "model-a:8b", "check.model": "model-a:8b"}, nil
}

func startService() (*Service, *startStore, *fakeRunner) {
	order := &[]string{}
	st, r := &startStore{order: order}, &fakeRunner{order: order}
	return &Service{Store: st, Runner: r, Prompts: fakePrompts{}, Models: fakeModels{"model-a:8b", "model-b:12b"}}, st, r
}

func TestNothingStartsUntilTheCourseHasItsModels(t *testing.T) {
	s, st, r := startService()
	st.noModels = true
	ctx := context.Background()
	for name, err := range map[string]error{"rerun": rerun(ctx, s, 1, 2, RerunRequest{}), "recheck": s.Recheck(ctx, 9, nil), "try": s.TryModels(ctx, 9, map[string]string{"check": "model-b:12b"})} {
		if !errors.Is(err, app.ErrInvalid) || !strings.Contains(err.Error(), "rubric") {
			t.Errorf("%s: %v", name, err)
		}
	}

	if st.approved != 0 || len(r.assignment) != 0 || len(r.one) != 0 {
		t.Fatalf("approved %d, started %d and %d", st.approved, len(r.assignment), len(r.one))
	}

	st.noModels, st.courseModel = false, "deleted:1b"
	if err := rerun(ctx, s, 1, 2, RerunRequest{}); !errors.Is(err, app.ErrInvalid) || !strings.Contains(err.Error(), "deleted:1b") {
		t.Fatalf("a course's model that is not downloaded: %v", err)
	}
}

func TestChecklistPageSaysWhatIsMissing(t *testing.T) {
	s, st, _ := startService()
	s.Assessors = func(_, _ map[string]string) Assessor { return nil }
	st.noModels = true
	page, err := s.Checklist(context.Background(), 2)
	if err != nil || !strings.Contains(page.ModelsProblem, "AI prompts") {
		t.Fatalf("%q %v", page.ModelsProblem, err)
	}

	st.noModels = false
	if page, err = s.Checklist(context.Background(), 2); err != nil || page.ModelsProblem != "" {
		t.Fatalf("%q %v", page.ModelsProblem, err)
	}
}

func TestASyncWithoutModelsQueuesNothingAndBuildsNothing(t *testing.T) {
	s, st, _ := startService()
	s.Assessors = func(_, _ map[string]string) Assessor { return fakeAssessor{} }
	st.noModels = true
	ctx := context.Background()
	if ids, err := s.QueueWorks(ctx, Job{AssignmentID: 2, FromSync: true}); err != nil || ids != nil {
		t.Fatalf("%v %v", ids, err)
	}

	st.maxGrade, st.intro = new(5.0), "Do the lab"
	if id, err := s.EnsureChecklist(ctx, 2, false, false); err != nil || id != 0 {
		t.Fatalf("%d %v", id, err)
	}
}

func TestRerunChecksEveryWorkAndAcceptsTheChecklist(t *testing.T) {
	s, st, r := startService()
	models := map[string]string{"check": "model-b:12b"}
	if err := rerun(context.Background(), s, 1, 2, RerunRequest{IncludeGraded: true, Models: models}); err != nil {
		t.Fatal(err)
	}

	j := r.assignment[0]
	if st.approved != 1 || j.AssignmentID != 2 || !j.Force || !j.IncludeGraded || j.SubmissionIDs != nil || j.Models["check"] != "model-b:12b" {
		t.Fatalf("approved %d, job %+v", st.approved, j)
	}
}

func TestANewChecklistIsNotAcceptedBeforeTheTeacherSeesIt(t *testing.T) {
	for _, req := range []RerunRequest{{NewChecklist: true}, {NewChecklist: true, ChecklistOnly: true}} {
		s, st, r := startService()
		if err := rerun(context.Background(), s, 1, 2, req); err != nil {
			t.Fatal(err)
		}
		if st.approved != 0 || r.assignment[0].NewChecklist != req.NewChecklist || r.assignment[0].ChecklistOnly != req.ChecklistOnly {
			t.Fatalf("%+v: approved %d, job %+v", req, st.approved, r.assignment[0])
		}
	}
}

func TestRerunOfFailedWorksTakesOnlyThem(t *testing.T) {
	s, st, r := startService()
	st.failed = []int64{4, 6}
	if err := rerun(context.Background(), s, 1, 2, RerunRequest{FailedOnly: true, NewChecklist: true, ChecklistOnly: true}); err != nil {
		t.Fatal(err)
	}

	j := r.assignment[0]
	if !slices.Equal(j.SubmissionIDs, []int64{4, 6}) || j.NewChecklist || j.ChecklistOnly || st.approved != 1 {
		t.Fatalf("failed works keep the current checklist: %+v, approved %d", j, st.approved)
	}
}

func TestRerunOfChosenWorksTakesOnlyWorksOfTheAssignment(t *testing.T) {
	s, st, r := startService()
	st.submitted = []int64{3, 4, 5}
	if err := rerun(context.Background(), s, 1, 2, RerunRequest{SubmissionIDs: []int64{5, 3, 5}, NewChecklist: true}); err != nil {
		t.Fatal(err)
	}

	if j := r.assignment[0]; !slices.Equal(j.SubmissionIDs, []int64{3, 5}) || j.NewChecklist || !j.Force || st.approved != 1 {
		t.Fatalf("the chosen works on the current checklist: %+v, approved %d", j, st.approved)
	}

	if err := rerun(context.Background(), s, 1, 2, RerunRequest{SubmissionIDs: []int64{3, 99}}); !errors.Is(err, app.ErrInvalid) || len(r.assignment) != 1 {
		t.Fatalf("a work of another assignment is refused: %v", err)
	}
}

func TestJobsAskedForWhileACheckRunsJoin(t *testing.T) {
	chosen := func(ids ...int64) Job { return Job{AssignmentID: 2, Force: true, SubmissionIDs: ids} }
	if got := JoinJobs(chosen(5, 3), chosen(3, 7)); !slices.Equal(got.SubmissionIDs, []int64{3, 5, 7}) {
		t.Fatalf("the works of both: %+v", got)
	}

	if got := JoinJobs(chosen(5), Job{AssignmentID: 2, Force: true}); got.SubmissionIDs != nil {
		t.Fatalf("every work covers the chosen ones: %+v", got)
	}

	later := Job{AssignmentID: 2, Force: true, Models: map[string]string{"check": "other:9b"}, SubmissionIDs: []int64{7}}
	if got := JoinJobs(chosen(5), later); !slices.Equal(got.SubmissionIDs, []int64{7}) || got.Models["check"] != "other:9b" {
		t.Fatalf("a different request: the later one wins: %+v", got)
	}
}

func TestNothingStartsWithoutFailedWorks(t *testing.T) {
	s, st, r := startService()
	err := rerun(context.Background(), s, 1, 2, RerunRequest{FailedOnly: true})
	if !errors.Is(err, app.ErrConflict) || len(r.assignment) != 0 || st.approved != 0 {
		t.Fatalf("err %v, jobs %d, approved %d", err, len(r.assignment), st.approved)
	}
}

func TestModelsNotInstalledAreRefusedBeforeAnythingHappens(t *testing.T) {
	s, st, r := startService()
	bad := map[string]string{"check": "no-such-model"}
	ctx := context.Background()
	for name, err := range map[string]error{"rerun": rerun(ctx, s, 1, 2, RerunRequest{Models: bad}), "recheck": s.Recheck(ctx, 9, bad), "try": s.TryModels(ctx, 9, bad)} {
		if !errors.Is(err, app.ErrInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}

	if st.approved != 0 || len(r.assignment) != 0 || len(r.one) != 0 {
		t.Fatalf("approved %d, started %d and %d", st.approved, len(r.assignment), len(r.one))
	}
}

func TestRecheckReplacesTheResultAndTryModelsDoesNot(t *testing.T) {
	s, _, r := startService()
	ctx := context.Background()
	models := map[string]string{"check": "model-b:12b"}
	if err := s.Recheck(ctx, 9, nil); err != nil {
		t.Fatal(err)
	}

	if err := s.TryModels(ctx, 9, models); err != nil {
		t.Fatal(err)
	}

	re, try := r.one[0], r.one[1]
	if !re.Force || re.Experiment || re.AssignmentID != 2 || !slices.Equal(re.SubmissionIDs, []int64{9}) {
		t.Fatalf("recheck %+v", re)
	}

	if try.Force || !try.Experiment || try.AssignmentID != 2 || !slices.Equal(try.SubmissionIDs, []int64{9}) {
		t.Fatalf("try %+v", try)
	}

	if err := s.TryModels(ctx, 9, nil); !errors.Is(err, app.ErrInvalid) {
		t.Fatalf("an experiment needs models: %v", err)
	}
}

func TestStopStopsTheRunBeforeClearingTheQueue(t *testing.T) {
	s, st, r := startService()
	if err := s.Stop(context.Background(), 2); err != nil {
		t.Fatal(err)
	}

	if !slices.Equal(*r.order, []string{"stop", "clear"}) {
		t.Fatalf("a run still going would queue works again: %v", *r.order)
	}

	st.cleared, *r.order = 0, nil
	r.err = errors.New("temporal is down")
	if err := s.Stop(context.Background(), 2); err == nil || st.cleared != 0 {
		t.Fatalf("the queue stays while the run may still go: %v, cleared %d", err, st.cleared)
	}
}

func TestChecklistPageShowsTheProgressOfARunningCheck(t *testing.T) {
	s, _, r := startService()
	page, err := s.Checklist(context.Background(), 2)
	if err != nil || page.Running || page.Progress != nil {
		t.Fatalf("nothing is running: %+v %v", page, err)
	}

	r.running, r.progress = true, Progress{Stage: "AssessWork", Done: 1, Total: 3}
	page, err = s.Checklist(context.Background(), 2)
	if err != nil || !page.Running || page.Progress == nil || page.Progress.Done != 1 {
		t.Fatalf("%+v %v", page, err)
	}

	r.running, r.progressErr = false, errors.New("temporal is down")
	page, err = s.Checklist(context.Background(), 2)
	if err != nil || page.Running {
		t.Fatalf("the page does not depend on Temporal: %+v %v", page, err)
	}
}

func TestQueueWorksFollowsTheJob(t *testing.T) {
	st := &queueStore{ids: []int64{1, 2}}
	s := &Service{Store: st, Prompts: fakePrompts{}, Models: fakeModels{"m"}, Assessors: func(_, _ map[string]string) Assessor { return nil }}
	ctx := context.Background()
	if ids, err := s.QueueWorks(ctx, Job{AssignmentID: 2, IncludeGraded: true, FromSync: true}); err != nil || !slices.Equal(ids, []int64{1, 2}) || st.graded || !slices.Equal(st.queued, []int64{1, 2}) {
		t.Fatalf("a sync never takes graded works: %v %v graded %v queued %v", ids, err, st.graded, st.queued)
	}

	st.queued = nil
	if ids, err := s.QueueWorks(ctx, Job{SubmissionIDs: []int64{9}, Experiment: true}); err != nil || !slices.Equal(ids, []int64{9}) || st.queued != nil {
		t.Fatalf("an experiment leaves the status alone: %v %v queued %v", ids, err, st.queued)
	}
}

type queueStore struct {
	Store
	ids, queued []int64
	graded      bool
}

func (f *queueStore) AssignmentText(context.Context, int64) (work.Assignment, error) {
	return work.Assignment{}, nil
}

func (f *queueStore) PromptOverrides(context.Context, int64) (map[string]string, error) {
	return map[string]string{"rubric.model": "m", "check.model": "m"}, nil
}

func (f *queueStore) SubmittedIDs(_ context.Context, _ int64, graded bool) ([]int64, error) {
	f.graded = graded
	return f.ids, nil
}

func (f *queueStore) QueueAssessments(_ context.Context, ids []int64) error {
	f.queued = ids
	return nil
}
