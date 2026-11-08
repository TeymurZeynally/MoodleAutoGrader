// Package seedfiles keeps the prepared data of an installation in a folder outside the repository (deploy/seed):
//
//	models.json                                     Ollama's options per model, "*" for every model
//	<Moodle course id>/settings.json                the course's rules; what it leaves out keeps the default
//	<Moodle course id>/prompts/check.system.md      a template: <step>.system.md or <step>.user.md
//	<Moodle course id>/prompts/check.model          the model of a step: <step>.model, one line
//	<Moodle course id>/assignments/<Moodle id>.json the notes for the grader and the approved checklist
//
// Example: deploy/seed.example. A course folder comes from `autograder seed-export <Moodle course id>`.
package seedfiles

import (
	"archive/tar"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"moodle-autograder/internal/app/seeding"
	"moodle-autograder/internal/domain/course"
)

// Dir is the folder with the prepared data; empty means there is none.
type Dir string

var _ seeding.Source = Dir("")

// ModelOptions reads Ollama's options per model; none without the file.
func (d Dir) ModelOptions() (map[string]map[string]any, error) {
	if d == "" {
		return nil, nil
	}

	b, err := os.ReadFile(filepath.Join(string(d), "models.json"))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}

	if err != nil {
		return nil, fmt.Errorf("read models.json: %w", err)
	}

	var out map[string]map[string]any
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, fmt.Errorf("decode models.json: %w", err)
	}

	return out, nil
}

// Course reads the folder of a course; an empty Course when there is none.
func (d Dir) Course(moodleCourseID int64) (seeding.Course, error) {
	var out seeding.Course
	if d == "" {
		return out, nil
	}

	dir := filepath.Join(string(d), strconv.FormatInt(moodleCourseID, 10))
	b, err := os.ReadFile(filepath.Join(dir, "settings.json")) //nolint:gosec // G304: a file of the installation's own folder
	switch {
	case err == nil:
		c := course.Defaults()
		if err := json.Unmarshal(b, &c); err != nil {
			return out, fmt.Errorf("decode settings.json: %w", err)
		}
		out.Settings = &c
	case !errors.Is(err, fs.ErrNotExist):
		return out, fmt.Errorf("read settings.json: %w", err)
	}

	if out.Prompts, err = readPrompts(filepath.Join(dir, "prompts")); err != nil {
		return out, err
	}

	out.Assignments, err = readAssignments(filepath.Join(dir, "assignments"))
	return out, err
}

func readPrompts(dir string) (map[string]string, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}

	if err != nil {
		return nil, fmt.Errorf("read prompts: %w", err)
	}

	out := map[string]string{}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || (!strings.HasSuffix(name, ".md") && !strings.HasSuffix(name, ".model")) {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, name)) //nolint:gosec // G304: a file of the installation's own folder
		if err != nil {
			return nil, fmt.Errorf("read prompt %s: %w", name, err)
		}
		if key, ok := strings.CutSuffix(name, ".md"); ok {
			out[key] = string(b)
		} else {
			out[name] = strings.TrimSpace(string(b))
		}
	}

	return out, nil
}

func readAssignments(dir string) (map[int64]seeding.Assignment, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}

	if err != nil {
		return nil, fmt.Errorf("read assignments: %w", err)
	}

	out := map[int64]seeding.Assignment{}
	for _, e := range entries {
		base, ok := strings.CutSuffix(e.Name(), ".json")
		if e.IsDir() || !ok {
			continue
		}
		id, err := strconv.ParseInt(base, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("assignments/%s: the name must be the assignment's id in Moodle: %w", e.Name(), err)
		}
		b, err := os.ReadFile(filepath.Join(dir, e.Name())) //nolint:gosec // G304: a file of the installation's own folder
		if err != nil {
			return nil, fmt.Errorf("read assignments/%s: %w", e.Name(), err)
		}
		var a seeding.Assignment
		if err := json.Unmarshal(b, &a); err != nil {
			return nil, fmt.Errorf("decode assignments/%s: %w", e.Name(), err)
		}
		out[id] = a
	}

	return out, nil
}

// WriteTar writes a course as a tar archive with the layout above, to unpack into the folder.
func WriteTar(w io.Writer, moodleCourseID int64, c seeding.Course) error {
	tw := tar.NewWriter(w)
	now := time.Now().Truncate(time.Second) // tar keeps whole seconds; rounding up would date the files in the future
	root := strconv.FormatInt(moodleCourseID, 10)
	put := func(name string, body []byte) error {
		if err := tw.WriteHeader(&tar.Header{Name: root + "/" + name, Mode: 0o644, Size: int64(len(body)), ModTime: now}); err != nil {
			return fmt.Errorf("write %s: %w", name, err)
		}
		if _, err := tw.Write(body); err != nil {
			return fmt.Errorf("write %s: %w", name, err)
		}
		return nil
	}
	putJSON := func(name string, v any) error {
		b, err := json.MarshalIndent(v, "", "  ")
		if err != nil {
			return fmt.Errorf("encode %s: %w", name, err)
		}
		return put(name, append(b, '\n'))
	}

	if c.Settings != nil {
		if err := putJSON("settings.json", c.Settings); err != nil {
			return err
		}
	}

	for key, body := range c.Prompts {
		name := key + ".md"
		if strings.HasSuffix(key, ".model") {
			name, body = key, body+"\n"
		}
		if err := put("prompts/"+name, []byte(body)); err != nil {
			return err
		}
	}

	for id, a := range c.Assignments {
		if err := putJSON("assignments/"+strconv.FormatInt(id, 10)+".json", a); err != nil {
			return err
		}
	}

	if err := tw.Close(); err != nil {
		return fmt.Errorf("finish the archive: %w", err)
	}

	return nil
}
