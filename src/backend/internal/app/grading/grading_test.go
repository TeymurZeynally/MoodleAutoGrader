package grading

import (
	"context"
	"errors"
	"slices"
	"testing"

	"moodle-autograder/internal/app"
)

type fakeStore struct {
	Store
	noPoints int64
	failed   []int64
}

func (f *fakeStore) Approve(_ context.Context, id, _ int64) (int64, error) {
	if id == f.noPoints {
		return 0, app.Fail(app.ErrInvalid, "submission %d has no points", id)
	}

	return id * 10, nil
}

func (f *fakeStore) MarkPublishFailed(ctx context.Context, id int64, _ string) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}

	f.failed = append(f.failed, id)
	return nil
}

type fakeRunner struct {
	job   Job
	calls int
	err   error
}

func (f *fakeRunner) Publish(_ context.Context, _ int64, job Job) error {
	f.job = job
	f.calls++
	return f.err
}

func TestStartPublishHandsTheApprovalsToTheRunner(t *testing.T) {
	st, r := &fakeStore{}, &fakeRunner{}
	got, err := (&Service{Store: st, Runner: r}).StartPublish(context.Background(), 1, []int64{1, 2}, "sealed")
	if err != nil || !slices.Equal(got, []int64{10, 20}) {
		t.Fatalf("%v %v", got, err)
	}

	if !slices.Equal(r.job.ApprovalIDs, got) || r.job.Session != "sealed" || st.failed != nil {
		t.Fatalf("%+v %v", r.job, st.failed)
	}
}

func TestDraftsDoNotStayApprovedWhenPublishingCannotStart(t *testing.T) {
	st, boom := &fakeStore{}, errors.New("temporal is down")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := (&Service{Store: st, Runner: &fakeRunner{err: boom}}).StartPublish(ctx, 1, []int64{1, 2}, "sealed")
	if !errors.Is(err, boom) || !slices.Equal(st.failed, []int64{1, 2}) {
		t.Fatalf("err %v, marked failed %v", err, st.failed)
	}
}

func TestAWorkWithoutPointsStopsThePublication(t *testing.T) {
	st, r := &fakeStore{noPoints: 2}, &fakeRunner{}
	_, err := (&Service{Store: st, Runner: r}).StartPublish(context.Background(), 1, []int64{1, 2, 3}, "sealed")
	if !errors.Is(err, app.ErrInvalid) || r.calls != 0 {
		t.Fatalf("err %v, runner called %d times", err, r.calls)
	}

	if msg, ok := app.Message(err); !ok || msg != "submission 2 has no points" {
		t.Fatalf("the teacher sees why: %q %v", msg, ok)
	}

	if !slices.Equal(st.failed, []int64{1}) {
		t.Fatalf("only the draft approved before the failure is marked: %v", st.failed)
	}
}

type sheetStore struct {
	Store
	students []Student
	rows     []Row
}

func (f sheetStore) Students(context.Context, int64) ([]Student, error) { return f.students, nil }

func (f sheetStore) StudentSubmissions(context.Context, int64, int64) ([]Row, error) {
	return f.rows, nil
}

func TestTheSheetOfAStudentLinksEachAssignmentInMoodle(t *testing.T) {
	st := sheetStore{students: []Student{{ID: 4, Name: "Anna"}}, rows: []Row{{AssignmentID: 1, CMID: 10, MoodleUserID: 77}, {AssignmentID: 2, CMID: 11, MoodleUserID: 77}}}
	s := &Service{Store: st, MoodleURL: "https://moodle.test"}
	sheet, err := s.StudentSheet(context.Background(), 3, 4)
	if err != nil || sheet.Student.Name != "Anna" || len(sheet.Submissions) != 2 || sheet.Submissions[1].GraderURL != "https://moodle.test/mod/assign/view.php?id=11&action=grader&userid=77" {
		t.Fatalf("%+v %v", sheet, err)
	}

	if _, err := s.StudentSheet(context.Background(), 3, 5); !errors.Is(err, app.ErrNotFound) {
		t.Fatalf("a student of another course: %v", err)
	}
}
