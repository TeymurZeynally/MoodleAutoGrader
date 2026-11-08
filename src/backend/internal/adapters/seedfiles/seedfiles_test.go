package seedfiles

import (
	"archive/tar"
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"moodle-autograder/internal/adapters/llm"
	"moodle-autograder/internal/app/seeding"
	"moodle-autograder/internal/domain/assessment"
	"moodle-autograder/internal/domain/course"
)

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestReadACourseFolder(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "900/settings.json"), `{"late_penalty": 2}`)
	write(t, filepath.Join(dir, "900/prompts/check.system.md"), "Be strict\n")
	write(t, filepath.Join(dir, "900/prompts/check.model"), "some:1b\n")
	write(t, filepath.Join(dir, "900/prompts/notes.txt"), "not a prompt")
	write(t, filepath.Join(dir, "900/assignments/9001.json"), `{"name": "Lab 1", "notes": "Careful", "criteria": [{"text": "Report", "where": "report", "points": 5}]}`)

	c, err := Dir(dir).Course(900)
	if err != nil {
		t.Fatal(err)
	}

	if c.Settings == nil || c.Settings.LatePenalty != 2 || c.Settings.CopyPercent != course.Defaults().CopyPercent {
		t.Fatalf("what the file leaves out keeps the default: %+v", c.Settings)
	}

	if len(c.Prompts) != 2 || c.Prompts["check.system"] != "Be strict\n" || c.Prompts["check.model"] != "some:1b" {
		t.Fatalf("%v", c.Prompts)
	}

	if a := c.Assignments[9001]; a.Notes != "Careful" || len(a.Criteria) != 1 || a.Criteria[0].Points != 5 {
		t.Fatalf("%+v", a)
	}
}

func TestNoFolderMeansNoData(t *testing.T) {
	for _, d := range []Dir{"", Dir(t.TempDir())} {
		c, err := d.Course(900)
		if err != nil || c.Settings != nil || len(c.Prompts) != 0 || len(c.Assignments) != 0 {
			t.Fatalf("%q: %+v %v", d, c, err)
		}
		opts, err := d.ModelOptions()
		if err != nil || opts != nil {
			t.Fatalf("%q: %v %v", d, opts, err)
		}
	}
}

func TestBrokenFilesAreReported(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "1/settings.json"), `{`)
	write(t, filepath.Join(dir, "2/assignments/lab.json"), `{}`)
	write(t, filepath.Join(dir, "3/assignments/9001.json"), `{`)
	write(t, filepath.Join(dir, "models.json"), `[]`)
	for _, id := range []int64{1, 2, 3} {
		if _, err := Dir(dir).Course(id); err == nil {
			t.Errorf("course %d: broken data is accepted", id)
		}
	}

	if _, err := Dir(dir).ModelOptions(); err == nil {
		t.Error("broken models.json is accepted")
	}
}

func TestModelOptions(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "models.json"), `{"*": {"num_ctx": 65536}, "some:1b": {"temperature": 0.7}}`)
	opts, err := Dir(dir).ModelOptions()
	if err != nil || opts["*"]["num_ctx"] != 65536.0 || opts["some:1b"]["temperature"] != 0.7 {
		t.Fatalf("%v %v", opts, err)
	}
}

// unpack writes a tar archive into a folder, as `tar -x` does.
func unpack(t *testing.T, data []byte, dir string) {
	t.Helper()
	r := tar.NewReader(bytes.NewReader(data))
	for {
		h, err := r.Next()
		if errors.Is(err, io.EOF) {
			return
		}
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(r)
		if err != nil {
			t.Fatal(err)
		}
		write(t, filepath.Join(dir, h.Name), string(body)) //nolint:gosec // G305: the archive this test wrote itself
	}
}

func TestAnExportReadsBackTheSame(t *testing.T) {
	settings := course.Defaults()
	settings.LatePenalty = 3
	in := seeding.Course{
		Settings:    &settings,
		Prompts:     map[string]string{"check.system": "Be strict\n", "check.model": "some:1b"},
		Assignments: map[int64]seeding.Assignment{9001: {Name: "Lab 1", Notes: "Careful", Criteria: []assessment.Criterion{{ID: "c1", Text: "Report", Where: "report", Points: 5}}}},
	}
	var b bytes.Buffer
	if err := WriteTar(&b, 900, in); err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	unpack(t, b.Bytes(), dir)
	out, err := Dir(dir).Course(900)
	if err != nil {
		t.Fatal(err)
	}

	if out.Settings == nil || out.Settings.LatePenalty != 3 || out.Prompts["check.system"] != "Be strict\n" || out.Prompts["check.model"] != "some:1b" {
		t.Fatalf("%+v", out)
	}

	if a := out.Assignments[9001]; a.Name != "Lab 1" || a.Notes != "Careful" || len(a.Criteria) != 1 || a.Criteria[0].Text != "Report" {
		t.Fatalf("%+v", a)
	}
}

// TestTheExampleIsValid keeps deploy/seed.example, which people copy, in step with the code. It needs the whole
// repository, so it is skipped where only the backend is mounted.
func TestTheExampleIsValid(t *testing.T) {
	dir := Dir("../../../../../deploy/seed.example")
	if _, err := os.Stat(string(dir)); err != nil {
		t.Skip("deploy/seed.example is not here")
	}

	opts, err := dir.ModelOptions()
	if err != nil || opts["*"]["num_ctx"] == nil {
		t.Fatalf("models.json: %v %v", opts, err)
	}

	c, err := dir.Course(12345)
	if err != nil {
		t.Fatal(err)
	}

	if c.Settings == nil || len(c.Prompts) == 0 || len(c.Assignments) == 0 {
		t.Fatalf("the example shows every part: %+v", c)
	}

	if err := c.Settings.Normalize().Validate(); err != nil {
		t.Fatal(err)
	}

	for key, body := range c.Prompts {
		if err := llm.Validate(key, body); err != nil {
			t.Errorf("%s: %v", key, err)
		}
	}

	grade := 5.0
	for id, a := range c.Assignments {
		if _, err := assessment.TeacherChecklist(a.Criteria, &grade); err != nil || a.Notes == "" {
			t.Errorf("assignment %d: %v", id, err)
		}
	}
}
