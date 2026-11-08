package courses

import (
	"context"
	"errors"
	"testing"
)

type fakeStore struct {
	Store
	list     []Course
	finished string
}

func (f *fakeStore) Courses(context.Context, int64, bool) ([]Course, error) { return f.list, nil }
func (f *fakeStore) FinishSync(_ context.Context, _ int64, syncErr string) error {
	f.finished = syncErr
	return nil
}

type fakeRuns struct {
	run   SyncRun
	err   error
	calls int
}

func (f *fakeRuns) SyncRun(context.Context, string) (SyncRun, error) {
	f.calls++
	return f.run, f.err
}

func running() []Course {
	id := "sync-course-1-1"
	return []Course{{ID: 1, SyncStatus: "running", SyncRunID: &id}, {ID: 2, SyncStatus: "idle"}}
}

func list(t *testing.T, st *fakeStore, r *fakeRuns) []Course {
	t.Helper()
	got, err := (&Service{Store: st, Runs: r}).List(context.Background(), 1, "token", false)
	if err != nil {
		t.Fatal(err)
	}

	return got
}

func TestListShowsTheProgressOfARunningSync(t *testing.T) {
	st, r := &fakeStore{list: running()}, &fakeRuns{run: SyncRun{Running: true, Stage: "DownloadFiles", Done: 2, Total: 5, Pending: 3, Retrying: []Retry{{Activity: "DownloadFiles", Attempt: 2}}}}
	got := list(t, st, r)
	p := got[0].Progress
	if p.Stage != "DownloadFiles" || p.Done != 2 || p.Total != 5 || p.Pending != 3 || len(p.Retrying) != 1 {
		t.Fatalf("%+v", p)
	}

	if r.calls != 1 || st.finished != "" {
		t.Fatalf("only the running course is asked about, and it is not finished: %d %q", r.calls, st.finished)
	}
}

func TestASyncWhoseRunIsGoneIsMarkedFailed(t *testing.T) {
	st := &fakeStore{list: running()}
	got := list(t, st, &fakeRuns{run: SyncRun{Closed: "Terminated"}})
	want := "sync stopped unexpectedly: Terminated"
	if st.finished != want || got[0].SyncStatus != "failed" || got[0].SyncError == nil || *got[0].SyncError != want {
		t.Fatalf("%q %+v", st.finished, got[0])
	}
}

func TestTheListIsShownWhenProgressCannotBeRead(t *testing.T) {
	st := &fakeStore{list: running()}
	got := list(t, st, &fakeRuns{err: errors.New("temporal is down")})
	if got[0].SyncStatus != "running" || st.finished != "" {
		t.Fatalf("an unknown state is not a failure: %+v %q", got[0], st.finished)
	}
}
