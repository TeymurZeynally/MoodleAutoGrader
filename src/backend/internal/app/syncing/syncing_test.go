package syncing

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"moodle-autograder/internal/app"
	"moodle-autograder/internal/domain/course"
	"moodle-autograder/internal/domain/work"
)

type fakeStore struct {
	Store
	busy      bool
	runID     string
	finished  string
	cancelled bool
}

func (f *fakeStore) StartSync(_ context.Context, _ int64, runID string) (bool, error) {
	f.runID = runID
	return !f.busy, nil
}

func (f *fakeStore) FinishSync(ctx context.Context, _ int64, syncErr string) error {
	f.finished, f.cancelled = syncErr, ctx.Err() != nil
	return nil
}

type fakeRunner struct {
	runID string
	job   Job
	err   error
	calls int
}

func (f *fakeRunner) Sync(_ context.Context, runID string, job Job) error {
	f.runID, f.job = runID, job
	f.calls++
	return f.err
}

func TestStartHandsTheSyncToTheRunner(t *testing.T) {
	st, r := &fakeStore{}, &fakeRunner{}
	if err := (&Service{Store: st, Runner: r}).Start(context.Background(), 5, "sealed"); err != nil {
		t.Fatal(err)
	}

	if r.job != (Job{CourseID: 5, Session: "sealed"}) || r.runID != st.runID || !strings.HasPrefix(r.runID, "sync-course-5-") {
		t.Fatalf("the run recorded on the course is the run started: %q %q %+v", st.runID, r.runID, r.job)
	}

	if st.finished != "" {
		t.Fatalf("a started sync is not finished: %q", st.finished)
	}
}

func TestSecondSyncOfACourseIsRefused(t *testing.T) {
	r := &fakeRunner{}
	err := (&Service{Store: &fakeStore{busy: true}, Runner: r}).Start(context.Background(), 5, "sealed")
	if !errors.Is(err, app.ErrConflict) || r.calls != 0 {
		t.Fatalf("err %v, runner called %d times", err, r.calls)
	}
}

func TestSyncThatCouldNotStartIsRecordedAsFailed(t *testing.T) {
	st, boom := &fakeStore{}, errors.New("temporal is down")
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // the browser tab was closed meanwhile: the course must not stay "running" because of that
	err := (&Service{Store: st, Runner: &fakeRunner{err: boom}}).Start(ctx, 5, "sealed")
	if !errors.Is(err, boom) || st.finished != "could not start sync" || st.cancelled {
		t.Fatalf("err %v, finished %q, cancelled %v", err, st.finished, st.cancelled)
	}
}

type fetchMoodle struct{ Moodle }

func (fetchMoodle) Course(context.Context, string, int64) (Course, bool, error) {
	return Course{ID: 77, Assignments: []Assignment{{ID: 501}}}, true, nil
}

func (fetchMoodle) GradebookComments(context.Context, string, int64) (map[int64]map[int64]string, error) {
	return nil, errors.New("the gradebook is not available")
}
func (fetchMoodle) Users(context.Context, string, int64) ([]User, error)   { return nil, nil }
func (fetchMoodle) Grades(context.Context, string, int64) ([]Grade, error) { return nil, nil }
func (fetchMoodle) Submissions(context.Context, string, int64) ([]Submission, error) {
	return nil, nil
}

type fetchStore struct {
	Store
	order   *[]string
	noFiles bool
}

func (f fetchStore) CourseSettings(context.Context, int64) (course.Settings, error) {
	c := course.Defaults()
	c.DownloadFiles = !f.noFiles
	return c, nil
}

func (f fetchStore) CourseMoodleID(context.Context, int64) (int64, error) { return 77, nil }
func (f fetchStore) SetMyGroups(context.Context, int64, int64, map[int64][]string) error {
	return nil
}
func (f fetchStore) SetRoster(context.Context, int64, []User) error        { return nil }
func (f fetchStore) SetMoodleGrades(context.Context, int64, []Grade) error { return nil }
func (f fetchStore) AttemptMarks(context.Context, int64) (map[int64]string, error) {
	return nil, nil
}
func (f fetchStore) RestoreDrafts(context.Context, int64) error { return nil }
func (f fetchStore) UpsertAssignment(context.Context, int64, Assignment) (int64, error) {
	*f.order = append(*f.order, "assignment")
	return 9, nil
}

type fakeSeeds struct {
	order *[]string
	err   error
}

func (f fakeSeeds) Apply(context.Context, int64) error {
	*f.order = append(*f.order, "seeds")
	return f.err
}

type attemptMoodle struct {
	Moodle
	asked          []int64
	refuse, broken int64
	earlier        []Attempt
}

func (m *attemptMoodle) Attempts(_ context.Context, _ string, _, user int64) ([]Attempt, error) {
	m.asked = append(m.asked, user)
	switch user {
	case m.refuse:
		return nil, app.Fail(app.ErrForbidden, "not enrolled")
	case m.broken:
		return nil, errors.New("moodle is down")
	}

	return append([]Attempt{}, m.earlier...), nil
}

type attemptStore struct {
	Store
	marks    map[int64]string
	set      map[int64][]Attempt
	restored int
	stored   []int
	pending  bool
	writes   []int64
}

func (f *attemptStore) AttemptMarks(context.Context, int64) (map[int64]string, error) {
	return f.marks, nil
}

func (f *attemptStore) SetAttempts(_ context.Context, _, user int64, mark string, a []Attempt) error {
	f.set[user] = a
	f.marks[user] = mark
	f.writes = append(f.writes, user)
	return nil
}

func (f *attemptStore) RestoreDrafts(context.Context, int64) error {
	f.restored++
	return nil
}

func (f *attemptStore) SyncSubmission(_ context.Context, _, _ int64, sub Submission) (int64, bool, error) {
	f.stored = append(f.stored, sub.Attempt)
	return int64(100 + sub.Attempt), f.pending, nil
}

func newAttemptStore() *attemptStore {
	return &attemptStore{marks: map[int64]string{}, set: map[int64][]Attempt{}}
}

func TestOnlySeveralAttemptsTakeARequestAndOnlyWhenSomethingChanged(t *testing.T) {
	then, later := time.Unix(100, 0), time.Unix(200, 0)
	st := newAttemptStore()
	m := &attemptMoodle{}
	s := &Service{Store: st, Moodle: m}
	subs := []Submission{{UserID: 1, Status: "submitted"}, {UserID: 2, Status: "submitted"}, {UserID: 3, Attempt: 1, Status: "reopened"}, {UserID: 4, Attempt: 1, Status: "submitted", SubmittedAt: &then}}
	grades := []Grade{{UserID: 1, Value: 5, At: &then}}
	var plan Plan
	if err := s.copyAttempts(context.Background(), "token", 9, 501, grades, subs, nil, nil, &plan); err != nil {
		t.Fatal(err)
	}

	if !slices.Equal(m.asked, []int64{3, 4}) || st.restored != 1 {
		t.Fatalf("one request for each student with several attempts, none for the others: %v, restored %d", m.asked, st.restored)
	}

	m.asked = nil
	if err := s.copyAttempts(context.Background(), "token", 9, 501, grades, subs, nil, nil, &plan); err != nil || len(m.asked) != 0 {
		t.Fatalf("nothing changed, nothing to ask: %v %v", m.asked, err)
	}

	subs[3].SubmittedAt = &later
	if err := s.copyAttempts(context.Background(), "token", 9, 501, grades, subs, nil, nil, &plan); err != nil || !slices.Equal(m.asked, []int64{4}) {
		t.Fatalf("a new upload is a change: %v %v", m.asked, err)
	}
}

func TestAnOnlyAttemptComesFromTheListsWithoutARequest(t *testing.T) {
	at := time.Unix(100, 0)
	st := newAttemptStore()
	m := &attemptMoodle{}
	s := &Service{Store: st, Moodle: m}
	subs := []Submission{{UserID: 1, Status: "submitted", SubmittedAt: &at, Files: []File{{Name: "lab.zip"}}}, {UserID: 2, Status: "submitted"}, {UserID: 3, Status: "new"}}
	grades := []Grade{{UserID: 1, Value: 4, At: &at}}
	comments := map[int64]string{1: "Нет отчёта", 2: "Только комментарий", 3: ""}
	var plan Plan
	if err := s.copyAttempts(context.Background(), "token", 9, 501, grades, subs, comments, nil, &plan); err != nil {
		t.Fatal(err)
	}

	if len(m.asked) != 0 {
		t.Fatalf("no request about a student with one attempt: %v", m.asked)
	}

	got := st.set[1]
	if len(got) != 1 || got[0].Attempt != 0 || *got[0].Grade != 4 || !got[0].GradedAt.Equal(at) || got[0].Comment != "Нет отчёта" || got[0].Files[0].Name != "lab.zip" {
		t.Fatalf("the attempt: %+v", got)
	}

	if only := st.set[2]; len(only) != 1 || only[0].Grade != nil || only[0].Comment != "Только комментарий" {
		t.Fatalf("a comment without a grade: %+v", only)
	}

	if _, ok := st.set[3]; ok {
		t.Fatal("no grade and no comment: nothing to store")
	}

	writes := len(st.writes)
	if err := s.copyAttempts(context.Background(), "token", 9, 501, grades, subs, comments, nil, &plan); err != nil || len(st.writes) != writes {
		t.Fatalf("nothing changed, nothing to write: %d then %d, %v", writes, len(st.writes), err)
	}

	comments[1] = "Другой"
	if err := s.copyAttempts(context.Background(), "token", 9, 501, grades, subs, comments, nil, &plan); err != nil || st.set[1][0].Comment != "Другой" {
		t.Fatalf("a changed comment is stored: %+v %v", st.set[1], err)
	}
}

func TestAStudentTheGradebookDoesNotListKeepsWhatIsKnown(t *testing.T) {
	at := time.Unix(100, 0)
	st := newAttemptStore()
	s := &Service{Store: st, Moodle: &attemptMoodle{}}
	subs := []Submission{{UserID: 1, Status: "submitted"}}
	grades := []Grade{{UserID: 1, Value: 4, At: &at}}
	var plan Plan
	if err := s.copyAttempts(context.Background(), "token", 9, 501, grades, subs, map[int64]string{1: "Нет отчёта"}, nil, &plan); err != nil {
		t.Fatal(err)
	}

	for name, comments := range map[string]map[int64]string{"left the course": {}, "the gradebook failed": nil} {
		if err := s.copyAttempts(context.Background(), "token", 9, 501, grades, subs, comments, nil, &plan); err != nil || st.set[1][0].Comment != "Нет отчёта" {
			t.Fatalf("%s: %+v %v", name, st.set[1], err)
		}
	}

	st = newAttemptStore()
	s.Store = st
	if err := s.copyAttempts(context.Background(), "token", 9, 501, grades, subs, nil, nil, &plan); err != nil || *st.set[1][0].Grade != 4 || st.set[1][0].Comment != "" {
		t.Fatalf("the grade comes back even without the gradebook: %+v %v", st.set[1], err)
	}
}

func TestEarlierAttemptsComeBackWithTheirFiles(t *testing.T) {
	st := newAttemptStore()
	st.pending = true
	m := &attemptMoodle{earlier: []Attempt{
		{ID: 10, Attempt: 0, Status: "submitted"},
		{ID: 20, Attempt: 1, Status: "submitted"},
	}}
	s := &Service{Store: st, Moodle: m}
	var plan Plan
	subs := []Submission{{UserID: 4, Attempt: 1, Status: "submitted"}}
	if err := s.copyAttempts(context.Background(), "token", 9, 501, nil, subs, nil, map[int64]int64{4: 40}, &plan); err != nil {
		t.Fatal(err)
	}

	if !slices.Equal(st.stored, []int{0}) || !slices.Equal(plan.Download, []int64{100}) || !slices.Equal(plan.Process, []int64{100}) {
		t.Fatalf("the latest attempt is stored by the sync itself: stored %v, plan %+v", st.stored, plan)
	}
}

func TestARefusalAboutOneStudentDoesNotStopTheOthers(t *testing.T) {
	st := newAttemptStore()
	m := &attemptMoodle{refuse: 1, earlier: []Attempt{{Attempt: 0}}}
	s := &Service{Store: st, Moodle: m}
	subs := []Submission{{UserID: 1, Attempt: 1}, {UserID: 2, Attempt: 1}}
	var plan Plan
	if err := s.copyAttempts(context.Background(), "token", 9, 501, nil, subs, nil, nil, &plan); err != nil {
		t.Fatal(err)
	}

	if a, ok := st.set[1]; !ok || a != nil {
		t.Fatalf("a refusal is remembered and keeps what is known: %v %v", a, ok)
	}

	if len(st.set[2]) != 1 {
		t.Fatalf("the next student is read: %v", st.set)
	}

	m.asked = nil
	if err := s.copyAttempts(context.Background(), "token", 9, 501, nil, subs, nil, nil, &plan); err != nil || len(m.asked) != 0 {
		t.Fatalf("not asked again until something changes: %v %v", m.asked, err)
	}
}

func TestAFailureDoesNotStopTheSyncAndTheNextOneGoesOn(t *testing.T) {
	at := time.Unix(100, 0)
	st := newAttemptStore()
	m := &attemptMoodle{broken: 2}
	s := &Service{Store: st, Moodle: m}
	subs := []Submission{{UserID: 1, Attempt: 1}, {UserID: 2, Attempt: 1}, {UserID: 3, Attempt: 1}, {UserID: 4}}
	grades := []Grade{{UserID: 4, Value: 5, At: &at}}
	var plan Plan
	if err := s.copyAttempts(context.Background(), "token", 9, 501, grades, subs, nil, nil, &plan); err != nil {
		t.Fatalf("the sync goes on: %v", err)
	}

	if !slices.Equal(m.asked, []int64{1, 2}) || st.restored != 1 {
		t.Fatalf("stops asking at the failure, what was read still goes to the drafts: %v, restored %d", m.asked, st.restored)
	}

	if _, ok := st.set[4]; !ok {
		t.Fatal("an only attempt needs no request and is still stored")
	}

	m.broken, m.asked = 0, nil
	if err := s.copyAttempts(context.Background(), "token", 9, 501, grades, subs, nil, nil, &plan); err != nil || !slices.Equal(m.asked, []int64{2, 3}) {
		t.Fatalf("goes on where it ended: %v %v", m.asked, err)
	}
}

func TestFetchLoadsPreparedDataOnceTheAssignmentsAreStored(t *testing.T) {
	order := &[]string{}
	s := &Service{Store: fetchStore{order: order}, Moodle: fetchMoodle{}, Seeds: fakeSeeds{order: order}}
	plan, err := s.Fetch(context.Background(), "token", 1, 5)
	if err != nil || len(plan.Assignments) != 1 {
		t.Fatalf("%+v %v", plan, err)
	}

	if len(*order) != 2 || (*order)[0] != "assignment" || (*order)[1] != "seeds" {
		t.Fatalf("a checklist needs its assignment: %v", *order)
	}

	boom := errors.New("database down")
	s.Seeds = fakeSeeds{order: order, err: boom}
	if _, err := s.Fetch(context.Background(), "token", 1, 5); !errors.Is(err, boom) {
		t.Fatalf("the sync retries a failed step: %v", err)
	}
}

func TestACourseWithoutFilesKeepsItsGradesAndPlansNoFile(t *testing.T) {
	order := &[]string{}
	s := &Service{Store: fetchStore{order: order, noFiles: true}, Moodle: fetchMoodle{}, Seeds: fakeSeeds{order: order}}
	plan, err := s.Fetch(context.Background(), "token", 1, 5)
	if err != nil || plan.Assignments != nil || plan.Download != nil || plan.Process != nil {
		t.Fatalf("nothing is downloaded, read or checked: %+v %v", plan, err)
	}

	if len(*order) != 2 || (*order)[0] != "assignment" {
		t.Fatalf("the assignment and its grades are still copied: %v", *order)
	}
}

type downloadStore struct {
	Store
	files      []work.File
	downloaded []int64
}

func (f *downloadStore) Submission(context.Context, int64) (work.Submission, error) {
	return work.Submission{ID: 5}, nil
}

func (f *downloadStore) Files(context.Context, int64, bool) ([]work.File, error) {
	return f.files, nil
}

func (f *downloadStore) MarkDownloaded(_ context.Context, id int64, _, _ string) error {
	f.downloaded = append(f.downloaded, id)
	return nil
}

type downloadMoodle struct {
	Moodle
	urls []string
}

func (m *downloadMoodle) Download(_ context.Context, _, url string, _ int64) ([]byte, error) {
	m.urls = append(m.urls, url)
	return []byte("data"), nil
}

type fakeUploads struct{ Uploads }

func (fakeUploads) PutRaw(context.Context, work.Submission, work.File, []byte) (string, error) {
	return "key", nil
}

func TestAFileOverTheLimitIsNotRequested(t *testing.T) {
	st := &downloadStore{files: []work.File{{ID: 1, URL: "big", Size: maxFileBytes + 1}, {ID: 2, URL: "small", Size: 10}}}
	m := &downloadMoodle{}
	s := &Service{Store: st, Moodle: m, Uploads: fakeUploads{}}
	if err := s.Download(context.Background(), "token", 5); err != nil {
		t.Fatalf("one large file must not stop the sync: %v", err)
	}

	if !slices.Equal(m.urls, []string{"small"}) || !slices.Equal(st.downloaded, []int64{2}) {
		t.Fatalf("requested %v, stored %v", m.urls, st.downloaded)
	}
}
