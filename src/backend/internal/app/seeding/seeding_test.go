package seeding

import (
	"context"
	"errors"
	"testing"

	"moodle-autograder/internal/app"
	"moodle-autograder/internal/domain/assessment"
	"moodle-autograder/internal/domain/course"
)

type fakeSource struct {
	seed Course
	err  error
}

func (f fakeSource) Course(int64) (Course, error) { return f.seed, f.err }

type fakePrompts struct{}

func (fakePrompts) Validate(key, body string) error {
	if body == "broken" {
		return errors.New("template error in " + key)
	}

	return nil
}

type fakeStore struct {
	Store
	targets    []Target
	settings   []course.Settings
	prompts    map[string]string
	checklists map[int64][]assessment.Criterion
	notes      map[int64]string
	latest     map[int64]assessment.Checklist
}

func newStore() *fakeStore {
	return &fakeStore{prompts: map[string]string{}, checklists: map[int64][]assessment.Criterion{}, notes: map[int64]string{}, latest: map[int64]assessment.Checklist{}}
}

func (f *fakeStore) CourseMoodleID(context.Context, int64) (int64, error) { return 900, nil }
func (f *fakeStore) CourseByMoodleID(context.Context, string, int64) (int64, error) {
	return 5, nil
}

func (f *fakeStore) AssignmentTargets(context.Context, int64) ([]Target, error) {
	return f.targets, nil
}

func (f *fakeStore) SeedSettings(_ context.Context, _ int64, c course.Settings) error {
	f.settings = append(f.settings, c)
	return nil
}

func (f *fakeStore) SeedPrompt(_ context.Context, _ int64, key, body string) error {
	f.prompts[key] = body
	return nil
}

func (f *fakeStore) SeedChecklist(_ context.Context, id int64, criteria, _ []assessment.Criterion) error {
	f.checklists[id] = criteria
	return nil
}

func (f *fakeStore) SeedNotes(_ context.Context, id int64, notes string) error {
	f.notes[id] = notes
	return nil
}

func (f *fakeStore) CourseSettings(context.Context, int64) (course.Settings, error) {
	return course.Defaults(), nil
}

func (f *fakeStore) PromptOverrides(context.Context, int64) (map[string]string, error) {
	return f.prompts, nil
}

func (f *fakeStore) LatestChecklist(_ context.Context, id int64) (assessment.Checklist, bool, error) {
	c, ok := f.latest[id]
	return c, ok, nil
}

func TestApplyLoadsEverythingByMoodleIDs(t *testing.T) {
	st, grade := newStore(), 5.0
	st.targets = []Target{{ID: 1, MoodleID: 9001, MaxGrade: &grade}, {ID: 2, MoodleID: 9002, MaxGrade: &grade}}
	settings := course.Defaults()
	settings.LatePenalty = 2
	seed := Course{
		Settings:    &settings,
		Prompts:     map[string]string{"check.system": "Be strict", "check.model": "some:1b"},
		Assignments: map[int64]Assignment{9001: {Notes: "Look at the proto", Criteria: []assessment.Criterion{{Text: "Report", Where: "report", Points: 5}}}, 7777: {Notes: "another course"}},
	}
	s := &Service{Store: st, Source: fakeSource{seed: seed}, Prompts: fakePrompts{}}
	if err := s.Apply(context.Background(), 5); err != nil {
		t.Fatal(err)
	}

	if len(st.settings) != 1 || st.settings[0].LatePenalty != 2 || st.prompts["check.system"] != "Be strict" || st.prompts["check.model"] != "some:1b" {
		t.Fatalf("settings %+v, prompts %v", st.settings, st.prompts)
	}

	if c := st.checklists[1]; len(c) != 1 || c[0].ID != "c1" || st.notes[1] != "Look at the proto" {
		t.Fatalf("the checklist is checked and numbered like a teacher's: %+v, notes %v", c, st.notes)
	}

	if len(st.checklists) != 1 || len(st.notes) != 1 {
		t.Fatalf("assignments without data stay empty: %v %v", st.checklists, st.notes)
	}
}

func TestWhatDoesNotMakeSenseIsLeftOutWithoutStoppingTheSync(t *testing.T) {
	st, grade := newStore(), 5.0
	st.targets = []Target{{ID: 1, MoodleID: 9001, MaxGrade: &grade}, {ID: 2, MoodleID: 9002, MaxGrade: &grade}}
	settings := course.Defaults()
	settings.CopyPercent = 500
	seed := Course{
		Settings: &settings,
		Prompts:  map[string]string{"check.system": "broken", "check.user": "fine"},
		Assignments: map[int64]Assignment{
			9001: {Criteria: []assessment.Criterion{{Text: "Grade changed in Moodle since", Where: "report", Points: 3}}},
			9002: {Criteria: []assessment.Criterion{{Text: "Fits", Where: "code", Points: 5}}},
		},
	}
	s := &Service{Store: st, Source: fakeSource{seed: seed}, Prompts: fakePrompts{}}
	if err := s.Apply(context.Background(), 5); err != nil {
		t.Fatal(err)
	}

	if len(st.settings) != 0 || len(st.prompts) != 1 || st.prompts["check.user"] != "fine" {
		t.Fatalf("settings %+v, prompts %v", st.settings, st.prompts)
	}

	if _, ok := st.checklists[1]; ok || len(st.checklists[2]) != 1 {
		t.Fatalf("the points must add up to the grade: %v", st.checklists)
	}

	s.Source = fakeSource{err: errors.New("bad json")}
	if err := s.Apply(context.Background(), 5); err != nil {
		t.Fatalf("an unreadable folder must not stop a sync: %v", err)
	}
}

func TestExportTakesOnlyApprovedChecklists(t *testing.T) {
	st := newStore()
	st.targets = []Target{{ID: 1, MoodleID: 9001, Name: "Lab 1"}, {ID: 2, MoodleID: 9002, Name: "Lab 2", Notes: "Careful"}, {ID: 3, MoodleID: 9003}}
	st.latest[1] = assessment.Checklist{Approved: true, Criteria: []assessment.Criterion{{ID: "c1", Text: "x", Where: "code", Points: 5}}}
	st.latest[2] = assessment.Checklist{Criteria: []assessment.Criterion{{ID: "c1", Text: "draft", Where: "code", Points: 5}}}
	s := &Service{Store: st}
	out, err := s.Export(context.Background(), 900)
	if err != nil {
		t.Fatal(err)
	}

	if a := out.Assignments[9001]; len(a.Criteria) != 1 || a.Name != "Lab 1" {
		t.Fatalf("%+v", a)
	}

	if a := out.Assignments[9002]; len(a.Criteria) != 0 || a.Notes != "Careful" {
		t.Fatalf("not approved stays out: %+v", a)
	}

	if _, ok := out.Assignments[9003]; ok {
		t.Fatal("an assignment with nothing prepared has no file")
	}

	st.latest, st.targets = map[int64]assessment.Checklist{}, nil
	if _, err := s.Export(context.Background(), 900); !errors.Is(err, app.ErrNotFound) {
		t.Fatalf("nothing to export: %v", err)
	}
}
