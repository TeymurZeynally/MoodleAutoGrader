package comparing

import (
	"context"
	"errors"
	"testing"

	"moodle-autograder/internal/app"
	"moodle-autograder/internal/domain/course"
	"moodle-autograder/internal/domain/integrity"
	"moodle-autograder/internal/domain/work"
)

type fakeStore struct {
	Store
	subs    []Features
	matches []Match
	levels  map[int64]string
}

func (f *fakeStore) AssignmentCourse(context.Context, int64) (int64, error) { return 1, nil }
func (f *fakeStore) CourseSettings(context.Context, int64) (course.Settings, error) {
	return course.Defaults(), nil
}

func (f *fakeStore) AssignmentFeatures(context.Context, int64) ([]Features, error) {
	return f.subs, nil
}

func (f *fakeStore) ReplaceMatches(_ context.Context, _ int64, m []Match) error {
	f.matches = m
	return nil
}
func (f *fakeStore) ReplaceFragments(context.Context, int64, []Fragment) error { return nil }
func (f *fakeStore) SetIntegrity(_ context.Context, id int64, level string) error {
	f.levels[id] = level
	return nil
}

func TestAttemptsOfOneStudentAreNotCopies(t *testing.T) {
	same := integrity.Features{FileHashes: []string{"a", "b", "c"}, CodeFiles: 3}
	st := &fakeStore{levels: map[int64]string{}, subs: []Features{{SubmissionID: 1, StudentID: 7, Features: same}, {SubmissionID: 2, StudentID: 7, Features: same}}}
	if err := (&Service{Store: st}).Check(context.Background(), 1); err != nil {
		t.Fatal(err)
	}

	if len(st.matches) != 0 || st.levels[1] != "ok" || st.levels[2] != "ok" {
		t.Fatalf("%+v %v", st.matches, st.levels)
	}

	st.subs[1].StudentID = 8
	if err := (&Service{Store: st}).Check(context.Background(), 1); err != nil {
		t.Fatal(err)
	}

	if len(st.matches) == 0 || st.levels[1] != "high" {
		t.Fatalf("two students with the same files are: %+v %v", st.matches, st.levels)
	}
}

type fakeSnapshots struct {
	Snapshots
	bad, broken int64
}

func (f fakeSnapshots) FullSnapshot(_ context.Context, id int64) (work.Snapshot, error) {
	switch id {
	case f.bad:
		return work.Snapshot{}, app.Fail(app.ErrPermanent, "snapshot %d is too large", id)
	case f.broken:
		return work.Snapshot{}, errors.New("storage is down")
	}

	return work.Snapshot{Code: []work.CodeFile{{Path: "A.cs", Text: "class A {}"}}}, nil
}

type fakeMatcher struct{ works []int }

func (f *fakeMatcher) Analyze(_ context.Context, _ string, files []CodeFile, _ float64, _ int) ([]CodePair, error) {
	f.works = append(f.works, len(files))
	return nil, nil
}

func TestAnUnreadableWorkIsLeftOutAndTheOthersAreCompared(t *testing.T) {
	st := &fakeStore{levels: map[int64]string{}, subs: []Features{{SubmissionID: 1, StudentID: 1}, {SubmissionID: 2, StudentID: 2}, {SubmissionID: 3, StudentID: 3}}}
	m := &fakeMatcher{}
	s := &Service{Store: st, Snapshots: fakeSnapshots{bad: 2}, Code: m}
	if err := s.Check(context.Background(), 1); err != nil {
		t.Fatalf("one student's work must not stop the assignment: %v", err)
	}

	if len(m.works) != 1 || m.works[0] != 2 {
		t.Fatalf("the two readable works are compared: %v", m.works)
	}

	s.Snapshots = fakeSnapshots{broken: 2}
	if err := s.Check(context.Background(), 1); err == nil {
		t.Fatal("a failure of the storage is retried, not skipped")
	}
}
