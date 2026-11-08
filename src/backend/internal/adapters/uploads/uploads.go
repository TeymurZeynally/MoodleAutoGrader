// Package uploads keeps what students uploaded in the object store: the original files as opaque bytes (bucket raw)
// and the snapshots read from them (bucket snapshots). It implements the upload ports of syncing, comparing,
// assessing and viewing; reading the untrusted bytes is the job of adapters/extract.
package uploads

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"moodle-autograder/internal/adapters/extract"
	"moodle-autograder/internal/adapters/filestore"
	"moodle-autograder/internal/adapters/tools"
	"moodle-autograder/internal/app"
	"moodle-autograder/internal/app/assessing"
	"moodle-autograder/internal/domain/work"
)

const (
	maxFileBytes = 50 << 20 // the largest stored object read back
	maxViewBytes = 60 << 20 // the largest original the viewer opens
)

type Store struct {
	Files *filestore.Store
	Tools *tools.Client // reads PDFs: their text and the pages with pictures
}

// A work's snapshot is two objects: snapshot.json is what the model sees, text.json is the other text files, for the
// similarity check only. Keeping them apart means new kinds of text files never change what the model saw.
func snapshotKey(id int64) string { return fmt.Sprintf("%d/snapshot.json", id) }
func textKey(id int64) string     { return fmt.Sprintf("%d/text.json", id) }

// PutRaw stores a downloaded file under course/assignment/submission/file-name.
func (s *Store) PutRaw(ctx context.Context, sub work.Submission, f work.File, data []byte) (string, error) {
	key := fmt.Sprintf("%d/%d/%d/%d-%s", sub.CourseID, sub.AssignmentID, sub.ID, f.ID, safeName(f.Name))
	return key, s.Files.Put(ctx, filestore.BucketRaw, key, data)
}

// Raw returns the original bytes of an uploaded file.
func (s *Store) Raw(ctx context.Context, f work.File) ([]byte, error) {
	return s.Files.Get(ctx, filestore.BucketRaw, f.Key, maxViewBytes)
}

// Read reads a work's downloaded files into a snapshot with the course's file rules.
func (s *Store) Read(ctx context.Context, files []work.File, codeExtensions, ignoredDirs []string) (work.Snapshot, error) {
	var snap work.Snapshot
	prof := extract.NewProfile(codeExtensions, ignoredDirs)
	for _, f := range files {
		if f.Key == "" {
			if f.Size > maxFileBytes {
				snap.Flag(work.FlagArchiveLimit) // too large to download
			}
			continue
		}
		data, err := s.Files.Get(ctx, filestore.BucketRaw, f.Key, maxFileBytes)
		if err != nil {
			return snap, err
		}
		extract.Add(ctx, &snap, f.Name, data, extract.DefaultLimits, prof, s.Tools.PDFText)
	}

	extract.Scan(&snap)
	return snap, nil
}

// SaveSnapshot stores a work's snapshot and its other text files.
func (s *Store) SaveSnapshot(ctx context.Context, id int64, snap work.Snapshot) error {
	raw, err := json.Marshal(snap) // without Text
	if err != nil {
		return fmt.Errorf("encode snapshot: %w", err)
	}

	text, err := json.Marshal(snap.Text)
	if err != nil {
		return fmt.Errorf("encode text files: %w", err)
	}

	if err := s.Files.Put(ctx, filestore.BucketSnapshots, snapshotKey(id), raw); err != nil {
		return err
	}

	return s.Files.Put(ctx, filestore.BucketSnapshots, textKey(id), text)
}

// Snapshot returns what the model sees of a work: the snapshot without the other text files.
func (s *Store) Snapshot(ctx context.Context, id int64) (work.Snapshot, error) {
	var snap work.Snapshot
	raw, err := s.Files.Get(ctx, filestore.BucketSnapshots, snapshotKey(id), maxFileBytes)
	if errors.Is(err, filestore.ErrTooLarge) {
		return snap, app.Fail(app.ErrPermanent, "snapshot %d is too large", id)
	}

	if err != nil {
		return snap, err
	}

	if err := json.Unmarshal(raw, &snap); err != nil {
		return snap, app.Fail(app.ErrPermanent, "decode snapshot %d: %s", id, err.Error())
	}

	return snap, nil
}

// FullSnapshot returns the snapshot with its other text files; works read before those were kept have none.
func (s *Store) FullSnapshot(ctx context.Context, id int64) (work.Snapshot, error) {
	snap, err := s.Snapshot(ctx, id)
	if err != nil {
		return snap, err
	}

	text, err := s.Files.Get(ctx, filestore.BucketSnapshots, textKey(id), maxFileBytes)
	if errors.Is(err, filestore.ErrNotFound) {
		return snap, nil
	}

	if errors.Is(err, filestore.ErrTooLarge) {
		return snap, app.Fail(app.ErrPermanent, "text files of %d are too large", id)
	}

	if err != nil {
		return snap, err
	}

	if err := json.Unmarshal(text, &snap.Text); err != nil {
		return snap, app.Fail(app.ErrPermanent, "decode text files %d: %s", id, err.Error())
	}

	return snap, nil
}

// FileList lists what was uploaded, with the paths inside archives (from their headers, nothing is unpacked), so
// requirements like "the .proto file is included" can be checked. The course's ignored folders and binaries are left out.
func (s *Store) FileList(ctx context.Context, files []work.File, codeExtensions, ignoredDirs []string) []string {
	prof := extract.NewProfile(codeExtensions, ignoredDirs)
	var out []string
	for _, f := range files {
		name := strings.TrimPrefix(f.Name, "/")
		out = append(out, name)
		if f.Key == "" || extract.KindByName(name) != "archive" {
			continue
		}
		data, err := s.Files.Get(ctx, filestore.BucketRaw, f.Key, maxFileBytes)
		if err != nil {
			continue
		}
		entries, err := extract.List(ctx, name, data, nil, prof)
		if err != nil {
			out = append(out, name+": the archive could not be read")
			continue
		}
		for _, e := range entries {
			if e.Note != "" || e.Kind == "exe" { // build output, dependencies, unsafe paths and binaries say nothing about the work
				continue
			}
			out = append(out, name+"/"+e.Path)
		}
	}

	return out
}

// Images gathers up to limit pictures: PDF pages with images (rendered by toolsd), DOCX media, archive images.
func (s *Store) Images(ctx context.Context, files []work.File, limit int) []assessing.Image {
	var out []assessing.Image
	for _, f := range files {
		if len(out) >= limit || f.Key == "" {
			continue
		}
		data, err := s.Files.Get(ctx, filestore.BucketRaw, f.Key, maxFileBytes)
		if err != nil {
			continue
		}
		name := strings.TrimPrefix(f.Name, "/")
		if kind, _ := extract.Detect(name, data); kind == "pdf" {
			pages, _, err := s.Tools.PDFImagePages(ctx, data, limit-len(out), 1280) // small text on screenshots stays readable
			if err != nil {
				continue
			}
			for _, p := range pages {
				out = append(out, modelImage(fmt.Sprintf("%s p.%d", name, p.Page), p.PNG))
			}
			continue
		}
		for _, img := range extract.Images(ctx, name, data, limit-len(out)) {
			out = append(out, modelImage(img.Source, img.Data))
		}
	}

	return out
}

// modelImage is a picture as the model gets it: decoded and encoded again here, never the student's own bytes.
func modelImage(source string, data []byte) assessing.Image {
	img, err := extract.ModelImage(data)
	if err != nil {
		return assessing.Image{Source: source, Err: err.Error()}
	}

	return assessing.Image{Source: source, Data: img}
}

func safeName(name string) string {
	return strings.Map(func(r rune) rune {
		if r == '/' || r == '\\' || r < 32 {
			return '_'
		}
		return r
	}, name)
}
