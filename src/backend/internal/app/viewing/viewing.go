// Package viewing shows the teacher what a student uploaded: the file list, archives as a tree, one file at a time.
// Everything comes from storage, never again from Moodle; the files are untrusted, so reading them is the job of the
// Archives adapter with its limits, and delivery serves them so that nothing can run in the app.
package viewing

import (
	"context"
	"strings"

	"moodle-autograder/internal/app"
	"moodle-autograder/internal/domain/course"
	"moodle-autograder/internal/domain/work"
)

type Store interface {
	Submission(ctx context.Context, id int64) (work.Submission, error)
	Files(ctx context.Context, submissionID int64, pendingOnly bool) ([]work.File, error)
	CourseSettings(ctx context.Context, courseID int64) (course.Settings, error)
}

// Uploads returns the stored bytes of an uploaded file.
type Uploads interface {
	Raw(ctx context.Context, f work.File) ([]byte, error)
}

// Archives reads untrusted files with limits; errors are for the teacher (too large, nested too deep, unreadable).
type Archives interface {
	KindByName(name string) string
	// List returns the entries of the archive at the end of inner (paths of nested archives); entries in ignored
	// folders get a note.
	List(ctx context.Context, name string, data []byte, inner, codeExtensions, ignoredDirs []string) ([]Entry, error)
	// Open returns the file at the end of inner, or the upload itself.
	Open(ctx context.Context, name string, data []byte, inner []string) ([]byte, string, error)
	// Detect decides by content how the browser may show bytes: the kind and the Content-Type.
	Detect(name string, data []byte) (kind, contentType string)
	DocxText(ctx context.Context, data []byte) string
	// DocPDF lays out a Word document as a PDF.
	DocPDF(ctx context.Context, data []byte) ([]byte, error)
	MaxViewBytes() int
}

type Entry struct {
	Path string `json:"path"`
	Size int64  `json:"size"` // as declared in the archive header
	Kind string `json:"kind"` // pdf, docx, image, archive, code, text, exe, other
	Note string `json:"note,omitempty"`
}

type Service struct {
	Store    Store
	Uploads  Uploads
	Archives Archives
}

// File is one uploaded file in the list.
type File struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
	Size int64  `json:"size"`
	Kind string `json:"kind"` // by name, or "missing" before it is downloaded; the content decides how it is served
}

// Files lists what the student uploaded, as stored.
func (s *Service) Files(ctx context.Context, submissionID int64) ([]File, error) {
	files, err := s.Store.Files(ctx, submissionID, false)
	if err != nil {
		return nil, err
	}

	out := []File{}
	for _, f := range files {
		v := File{ID: f.ID, Name: strings.TrimPrefix(f.Name, "/"), Size: f.Size, Kind: s.Archives.KindByName(f.Name)}
		if f.Key == "" {
			v.Kind = "missing"
		}
		out = append(out, v)
	}

	return out, nil
}

// List returns the entries of an uploaded archive, or of an archive inside it.
func (s *Service) List(ctx context.Context, submissionID, fileID int64, inner []string) ([]Entry, error) {
	f, data, err := s.uploaded(ctx, submissionID, fileID)
	if err != nil {
		return nil, err
	}

	sub, err := s.Store.Submission(ctx, submissionID)
	if err != nil {
		return nil, err
	}

	settings, err := s.Store.CourseSettings(ctx, sub.CourseID)
	if err != nil {
		return nil, err
	}

	entries, err := s.Archives.List(ctx, f.Name, data, inner, settings.CodeExtensions, settings.IgnoredDirs)
	if err != nil {
		return nil, app.Fail(app.ErrInvalid, "%s", err.Error())
	}

	return entries, nil
}

// Content is one file to show: its bytes and how to serve them.
type Content struct {
	Name, Type string
	Data       []byte
	Attachment bool // not something a browser shows: download it
}

// Open returns one file for the viewer; as "text" turns a Word document into its text, as "pdf" into its pages.
func (s *Service) Open(ctx context.Context, submissionID, fileID int64, inner []string, as string) (Content, error) {
	f, data, err := s.uploaded(ctx, submissionID, fileID)
	if err != nil {
		return Content{}, err
	}

	data, name, err := s.Archives.Open(ctx, f.Name, data, inner)
	if err != nil {
		return Content{}, app.Fail(app.ErrInvalid, "%s", err.Error())
	}

	kind, ctype := s.Archives.Detect(name, data)
	switch {
	case as == "text" && kind == "docx":
		data, ctype = []byte(s.Archives.DocxText(ctx, data)), "text/plain; charset=utf-8"
	case as == "pdf" && kind == "docx":
		pdf, err := s.Archives.DocPDF(ctx, data)
		if err != nil {
			return Content{}, app.Fail(app.ErrInvalid, "%s", err.Error())
		}
		data, ctype = pdf, "application/pdf"
	}

	if limit := s.Archives.MaxViewBytes(); len(data) > limit {
		return Content{}, app.Fail(app.ErrInvalid, "file is larger than %d MB and is not shown", limit>>20)
	}

	return Content{Name: name, Type: ctype, Data: data, Attachment: ctype == "application/octet-stream"}, nil
}

// uploaded loads one stored file of a submission.
func (s *Service) uploaded(ctx context.Context, submissionID, fileID int64) (work.File, []byte, error) {
	files, err := s.Store.Files(ctx, submissionID, false)
	if err != nil {
		return work.File{}, nil, err
	}

	for _, f := range files {
		if f.ID != fileID {
			continue
		}
		if f.Key == "" {
			return f, nil, app.Fail(app.ErrNotFound, "the file has not been downloaded yet")
		}
		data, err := s.Uploads.Raw(ctx, f)
		return f, data, err
	}

	return work.File{}, nil, app.Fail(app.ErrNotFound, "not found")
}
