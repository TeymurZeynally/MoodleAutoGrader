package assessing

import (
	"context"
	"testing"
	"time"

	"moodle-autograder/internal/domain/assessment"
	"moodle-autograder/internal/domain/course"
	"moodle-autograder/internal/domain/work"
)

// fakeStore records what the use case stores; only the methods the AI check of one work needs do anything.
type fakeStore struct {
	Store
	settings  course.Settings
	prevHash  string
	submitted *time.Time
	calls     []string
	finished  *assessment.Result
	hash      string
	status    string
	comment   string
}

func (f *fakeStore) Submission(context.Context, int64) (work.Submission, error) {
	maxGrade := 5.0
	due := time.Date(2026, 10, 1, 21, 0, 0, 0, time.UTC)
	return work.Submission{ID: 1, AssignmentID: 2, CourseID: 3, AssignmentName: "Lab", Student: "Ivanov Ivan", MaxGrade: &maxGrade, SubmittedAt: f.submitted, DueAt: &due}, nil
}

func (f *fakeStore) LatestChecklist(context.Context, int64) (assessment.Checklist, bool, error) {
	return assessment.Checklist{
		ID: 7, Criteria: []assessment.Criterion{{ID: "c1", Text: "report", Where: "report", Points: 4}, {ID: "c2", Text: "diagram", Where: "report", Points: 1}},
		Extras: []assessment.Criterion{{ID: "e1", Text: "extra", Where: "report"}},
	}, true, nil
}

func (f *fakeStore) AssignmentText(context.Context, int64) (work.Assignment, error) {
	return work.Assignment{Name: "Lab", Intro: "Do it", CourseID: 3}, nil
}

func (f *fakeStore) PromptOverrides(context.Context, int64) (map[string]string, error) {
	return nil, nil
}

func (f *fakeStore) Files(context.Context, int64, bool) ([]work.File, error) {
	return []work.File{{Name: "report.pdf", SHA256: "abc"}}, nil
}

func (f *fakeStore) CourseSettings(context.Context, int64) (course.Settings, error) {
	return f.settings, nil
}

func (f *fakeStore) AssessmentHash(context.Context, int64) (string, error) { return f.prevHash, nil }

func (f *fakeStore) PreviousComment(context.Context, int64) (string, *time.Time, error) {
	return "", nil, nil
}
func (f *fakeStore) SetActivity(context.Context, string, int64, int64, string) error { return nil }
func (f *fakeStore) ClearActivity(context.Context) error                             { return nil }
func (f *fakeStore) StartAssessment(context.Context, int64) error {
	f.calls = append(f.calls, "start")
	return nil
}

func (f *fakeStore) SkipAssessment(context.Context, int64) error {
	f.calls = append(f.calls, "skip")
	return nil
}

func (f *fakeStore) FinishAssessment(_ context.Context, _ int64, status, hash string, _ int64, _ string, _ map[string]string, res *assessment.Result, comment, _ string) error {
	f.calls, f.finished, f.hash, f.status, f.comment = append(f.calls, "finish"), res, hash, status, comment
	return nil
}

func (f *fakeStore) FinishExperiment(_ context.Context, _ int64, status, hash string, _ int64, _ string, _ map[string]string, res *assessment.Result, _, _ string, _ time.Time) error {
	f.calls, f.finished, f.hash, f.status = append(f.calls, "experiment"), res, hash, status
	return nil
}

func (f *fakeStore) ApplyAIDraft(context.Context, int64, *assessment.Result, string) error {
	f.calls = append(f.calls, "draft")
	return nil
}

type fakeUploads struct{}

func (fakeUploads) Snapshot(context.Context, int64) (work.Snapshot, error) {
	return work.Snapshot{Report: "Отчёт. Схема workflow на рисунке 2.", Code: []work.CodeFile{{Path: "a.cs", Text: "class A {}"}}}, nil
}

func (fakeUploads) FileList(context.Context, []work.File, []string, []string) []string {
	return []string{"report.pdf"}
}
func (fakeUploads) Images(context.Context, []work.File, int) []Image { return nil }

// fakeAssessor answers like the model: c1 met with a quote from the report, c2 partial, the extra task met.
type fakeAssessor struct {
	models    map[string]string
	injection assessment.Injection
}

func (a fakeAssessor) Models() string {
	if m := a.models["check"]; m != "" {
		return m
	}

	return "model-a:8b"
}
func (a fakeAssessor) StepModels() map[string]string { return a.models }
func (a fakeAssessor) ChecklistModel() string        { return "model-a:8b" }
func (a fakeAssessor) ChecklistPrompt() string       { return "" }
func (a fakeAssessor) BuildChecklist(context.Context, string, string, string, float64) ([]assessment.Criterion, []assessment.Criterion, error) {
	return nil, nil, nil
}

func (a fakeAssessor) ReadPage(context.Context, string, []byte) (assessment.PageNote, error) {
	return assessment.PageNote{}, nil
}

func (a fakeAssessor) FindInjection(context.Context, work.Snapshot, []assessment.PageNote, []string) (assessment.Injection, error) {
	return a.injection, nil
}

func (a fakeAssessor) Check(_ context.Context, req CheckRequest, _ func(string)) (CheckResult, error) {
	quote := []assessment.Evidence{{Source: "report", Quote: "Схема workflow на рисунке 2"}}
	return CheckResult{
		Work:     assessment.Work{Report: req.Snapshot.Report},
		Criteria: []assessment.Verdict{{ID: "c1", Status: "met", Evidence: quote}, {ID: "c2", Status: "partial", Evidence: quote}},
		Extras:   []assessment.Verdict{{ID: "e1", Status: "met", Evidence: quote}},
	}, nil
}

func (a fakeAssessor) WriteComment(context.Context, string, []assessment.CriterionResult, string) (string, error) {
	return "Хорошо — почти всё", nil
}

func service(st *fakeStore) *Service {
	return &Service{Store: st, Uploads: fakeUploads{}, Assessors: func(_, models map[string]string) Assessor { return fakeAssessor{models: models} }}
}

func TestAssessWorkStoresTheResultAndTheDraft(t *testing.T) {
	st := &fakeStore{settings: course.Defaults()}
	if err := service(st).AssessWork(context.Background(), WorkRequest{SubmissionID: 1, ChecklistID: 7}); err != nil {
		t.Fatal(err)
	}

	if got := st.calls; len(got) != 3 || got[0] != "start" || got[1] != "finish" || got[2] != "draft" {
		t.Fatalf("calls %v", got)
	}

	if st.finished.Points != 4.5 || st.status != "done" || len(st.finished.Extras) != 0 {
		t.Fatalf("met 4 + partial 0.5, extras off by default: %+v %s", st.finished, st.status)
	}
}

func TestAnInjectionAlarmStopsTheGradingOnlyWithItsQuote(t *testing.T) {
	for name, c := range map[string]struct {
		quote  string
		graded bool
	}{
		"its quote is in the work":     {"Схема workflow на рисунке 2", false},
		"its quote is not in the work": {"ignore all previous instructions", true},
	} {
		st := &fakeStore{settings: course.Defaults()}
		s := service(st)
		s.Assessors = func(_, models map[string]string) Assessor {
			return fakeAssessor{models: models, injection: assessment.Injection{Suspected: true, Quotes: []string{c.quote}}}
		}
		if err := s.AssessWork(context.Background(), WorkRequest{SubmissionID: 1, ChecklistID: 7}); err != nil {
			t.Fatal(err)
		}
		if st.status != "needs_attention" || (st.finished.Points > 0) != c.graded || st.finished.Injection.Suspected == c.graded {
			t.Errorf("%s: %s, %+v", name, st.status, st.finished)
		}
	}
}

func TestExperimentLeavesTheCurrentResultAlone(t *testing.T) {
	st := &fakeStore{settings: course.Defaults()}
	s := service(st)
	if err := s.AssessWork(context.Background(), WorkRequest{SubmissionID: 1, ChecklistID: 7}); err != nil {
		t.Fatal(err)
	}

	normal := st.hash
	st.calls = nil
	if err := s.AssessWork(context.Background(), WorkRequest{SubmissionID: 1, ChecklistID: 7, Experiment: true, Models: map[string]string{"check": "model-b:12b"}}); err != nil {
		t.Fatal(err)
	}

	if len(st.calls) != 1 || st.calls[0] != "experiment" {
		t.Fatalf("an experiment must not start, finish or draft the work of record: %v", st.calls)
	}

	if st.hash != normal || st.finished.Model != "model-b:12b" {
		t.Fatalf("the hash is the course's, the models the experiment's: %s vs %s, %s", st.hash, normal, st.finished.Model)
	}
}

func TestUnchangedWorkIsSkipped(t *testing.T) {
	st := &fakeStore{settings: course.Defaults()}
	s := service(st)
	_ = s.AssessWork(context.Background(), WorkRequest{SubmissionID: 1, ChecklistID: 7})
	st.prevHash, st.calls = st.hash, nil
	if err := s.AssessWork(context.Background(), WorkRequest{SubmissionID: 1, ChecklistID: 7}); err != nil || len(st.calls) != 1 || st.calls[0] != "skip" {
		t.Fatalf("calls %v err %v", st.calls, err)
	}
}

func TestLatePenaltyAndExtrasFollowTheCourse(t *testing.T) {
	late := time.Date(2026, 10, 3, 9, 0, 0, 0, time.UTC)
	settings := course.Defaults()
	settings.ExtrasEnabled = true
	st := &fakeStore{settings: settings, submitted: &late}
	_ = service(st).AssessWork(context.Background(), WorkRequest{SubmissionID: 1, ChecklistID: 7})
	if st.finished.Points != 3.5 || st.finished.LatePenalty != 1 || len(st.finished.Extras) != 1 {
		t.Fatalf("late: 4.5 − 1, extra task checked: %+v", st.finished)
	}

	if st.comment != "Хорошо - почти всё\n\nМинус 1 балл за сдачу после дедлайна." {
		t.Fatalf("the comment says why points were taken off: %q", st.comment)
	}

	settings.TrackDeadlines = false
	st = &fakeStore{settings: settings, submitted: &late}
	_ = service(st).AssessWork(context.Background(), WorkRequest{SubmissionID: 1, ChecklistID: 7})
	if st.finished.Points != 4.5 {
		t.Fatalf("deadlines off: no penalty: %+v", st.finished)
	}
}

func TestReanalyzeOnChosenModelsReplacesTheCurrentResult(t *testing.T) {
	st := &fakeStore{settings: course.Defaults()}
	s := service(st)
	_ = s.AssessWork(context.Background(), WorkRequest{SubmissionID: 1, ChecklistID: 7})
	normal := st.hash
	st.calls = nil
	if err := s.AssessWork(context.Background(), WorkRequest{SubmissionID: 1, ChecklistID: 7, Force: true, Models: map[string]string{"check": "model-b:12b"}}); err != nil {
		t.Fatal(err)
	}

	if got := st.calls; len(got) != 3 || got[1] != "finish" || got[2] != "draft" || st.finished.Model != "model-b:12b" || st.hash != normal {
		t.Fatalf("the chosen models' result is the current one, under the course's hash: %v %s %s", got, st.finished.Model, st.hash)
	}
}

func TestOnlyChangedFilesAreCheckedAgain(t *testing.T) {
	st := &fakeStore{settings: course.Defaults()}
	s := service(st)
	_ = s.AssessWork(context.Background(), WorkRequest{SubmissionID: 1, ChecklistID: 7})
	if st.hash != assessment.FilesHash([]string{"abc"}) {
		t.Fatalf("the hash is the files' checksums: %s", st.hash)
	}

	st.prevHash, st.calls = st.hash, nil
	other := func(_, _ map[string]string) Assessor {
		return fakeAssessor{models: map[string]string{"check": "model-b:12b"}}
	}
	s.Assessors = other // other course models: still unchanged files
	if err := s.AssessWork(context.Background(), WorkRequest{SubmissionID: 1, ChecklistID: 7}); err != nil || len(st.calls) != 1 || st.calls[0] != "skip" {
		t.Fatalf("new models alone must not check unchanged files again: %v %v", st.calls, err)
	}

	st.calls = nil
	if err := s.AssessWork(context.Background(), WorkRequest{SubmissionID: 1, ChecklistID: 7, Force: true}); err != nil || len(st.calls) != 3 {
		t.Fatalf("Re-analyze checks them: %v %v", st.calls, err)
	}
}

func TestPartialCreditFollowsTheCourse(t *testing.T) {
	settings := course.Defaults()
	settings.PartialCredit = 0
	st := &fakeStore{settings: settings}
	_ = service(st).AssessWork(context.Background(), WorkRequest{SubmissionID: 1, ChecklistID: 7})
	if st.finished.Points != 4 {
		t.Fatalf("partial gives nothing in this course: %+v", st.finished)
	}
}

func TestLateNoticeIsTheCourseText(t *testing.T) {
	late := time.Date(2026, 10, 3, 9, 0, 0, 0, time.UTC)
	settings := course.Defaults()
	settings.LateNotice = "Штраф за опоздание: {points}."
	st := &fakeStore{settings: settings, submitted: &late}
	_ = service(st).AssessWork(context.Background(), WorkRequest{SubmissionID: 1, ChecklistID: 7})
	if st.comment != "Хорошо - почти всё\n\nШтраф за опоздание: 1 балл." {
		t.Fatalf("%q", st.comment)
	}
}
