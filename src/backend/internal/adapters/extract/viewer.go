package extract

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"
	"time"
	"unicode/utf8"

	"moodle-autograder/internal/domain/work"

	"github.com/mholt/archives"
)

// The viewer lets the teacher browse everything a student uploaded. Archives are listed from their headers without
// unpacking; a single entry is unpacked on request, with a byte limit and a deadline, so a zip bomb cannot expand.

const (
	MaxViewBytes = 20 << 20 // one file shown in the browser
	maxDepth     = 2        // archive inside archive inside the upload
	viewTimeout  = 15 * time.Second
)

var (
	ErrNotFound = errors.New("no such file in the archive")
	ErrTooLarge = fmt.Errorf("file is larger than %d MB and is not shown", MaxViewBytes>>20)
	ErrDepth    = errors.New("archive nesting is too deep to open")
	errFound    = errors.New("found")
)

type Entry struct {
	Path string `json:"path"`
	Size int64  `json:"size"` // as declared in the archive header
	Kind string `json:"kind"` // pdf, docx, image, archive, code, text, exe, other
	Note string `json:"note,omitempty"`
}

// IsArchive reports whether the bytes are an archive the viewer can list.
func IsArchive(data []byte) bool {
	switch sniff(data) {
	case "zip", "rar", "7z", "docx":
		return true
	}

	return false
}

// List returns the entries of the archive found by following inner (paths of nested archives) from the uploaded file;
// entries in the course's ignored folders get a note.
func List(ctx context.Context, name string, data []byte, inner []string, prof Profile) ([]Entry, error) {
	ctx, cancel := context.WithTimeout(ctx, viewTimeout)
	defer cancel()
	data, name, err := descend(ctx, name, data, inner)
	if err != nil {
		return nil, err
	}

	ex, err := extractor(ctx, name, data)
	if err != nil {
		return nil, err
	}

	var out []Entry
	err = ex.Extract(ctx, bytes.NewReader(data), func(_ context.Context, f archives.FileInfo) error {
		if f.IsDir() {
			return nil
		}
		if len(out) >= DefaultLimits.MaxEntries {
			return errLimit
		}
		e := Entry{Path: strings.ReplaceAll(f.NameInArchive, `\`, "/"), Size: f.Size(), Kind: KindByName(f.NameInArchive)}
		switch clean, ok := safePath(f); {
		case !ok:
			e.Kind, e.Note = "other", "unsafe path or link: not opened"
		default:
			e.Path = clean
			if prof.Skipped(clean) {
				e.Note = "build output or dependencies: not checked"
			}
		}
		out = append(out, e)
		return nil
	})
	if errors.Is(err, errLimit) {
		return out, nil // the first MaxEntries are shown
	}

	if err != nil && len(out) == 0 {
		return nil, fmt.Errorf("read archive: %w", err)
	}

	return out, nil
}

// Open returns one file: the upload itself (no inner path) or an entry at the end of the inner path.
func Open(ctx context.Context, name string, data []byte, inner []string) ([]byte, string, error) {
	ctx, cancel := context.WithTimeout(ctx, viewTimeout)
	defer cancel()
	data, name, err := descend(ctx, name, data, inner)
	return data, name, err
}

// descend follows inner archive paths; each step unpacks exactly one entry with the size limit.
func descend(ctx context.Context, name string, data []byte, inner []string) ([]byte, string, error) {
	if len(inner) > maxDepth+1 {
		return nil, "", ErrDepth
	}

	for _, p := range inner {
		next, err := entry(ctx, name, data, p)
		if err != nil {
			return nil, "", err
		}
		data, name = next, p
	}

	return data, name, nil
}

func entry(ctx context.Context, name string, data []byte, want string) ([]byte, error) {
	ex, err := extractor(ctx, name, data)
	if err != nil {
		return nil, err
	}

	var body []byte
	var openErr error
	err = ex.Extract(ctx, bytes.NewReader(data), func(_ context.Context, f archives.FileInfo) error {
		clean, ok := safePath(f)
		if f.IsDir() || !ok || clean != want {
			return nil
		}
		if f.Size() > MaxViewBytes {
			openErr = ErrTooLarge
			return errFound
		}
		rc, err := f.Open()
		if err != nil {
			openErr = fmt.Errorf("open entry: %w", err)
			return errFound
		}
		defer rc.Close()
		body, err = io.ReadAll(io.LimitReader(rc, MaxViewBytes+1)) // the declared size may lie
		switch {
		case err != nil:
			openErr = fmt.Errorf("unpack entry: %w", err)
		case len(body) > MaxViewBytes:
			body, openErr = nil, ErrTooLarge
		}
		return errFound
	})
	switch {
	case errors.Is(err, errFound):
		return body, openErr
	case ctx.Err() != nil:
		return nil, errors.New("archive took too long to read")
	case err != nil:
		return nil, fmt.Errorf("read archive: %w", err)
	}

	return nil, ErrNotFound
}

func extractor(ctx context.Context, name string, data []byte) (archives.Extractor, error) {
	if !IsArchive(data) {
		return nil, errors.New("not an archive")
	}

	format, _, err := archives.Identify(ctx, name, bytes.NewReader(data))
	ex, ok := format.(archives.Extractor)
	if err != nil || !ok {
		return nil, errors.New("unsupported archive format")
	}

	return ex, nil
}

func safePath(f archives.FileInfo) (string, bool) {
	raw := strings.ReplaceAll(f.NameInArchive, `\`, "/")
	clean := path.Clean("/" + raw)[1:]
	ok := f.LinkTarget == "" && f.Mode().Type() == 0 && clean == strings.TrimPrefix(raw, "./") && !strings.HasPrefix(raw, "/")
	return clean, ok
}

var (
	kindOfExt = map[string]string{
		".pdf": "pdf", ".docx": "docx", ".png": "image", ".jpg": "image", ".jpeg": "image", ".gif": "image", ".webp": "image", ".bmp": "image",
		".zip": "archive", ".rar": "archive", ".7z": "archive", ".exe": "exe", ".dll": "exe", ".so": "exe",
	}
	textExt = map[string]bool{".txt": true, ".md": true, ".json": true, ".xml": true, ".yml": true, ".yaml": true, ".config": true, ".sln": true, ".gitignore": true, ".env": true, ".http": true, ".csv": true, ".html": true, ".css": true, ".svg": true, ".dockerfile": true}
)

// KindByName tells what a file is by its name alone, for the file tree.
func KindByName(name string) string {
	ext := strings.ToLower(path.Ext(name))
	switch kind, ok := kindOfExt[ext]; {
	case ok:
		return kind
	case DefaultProfile.code[ext]:
		return "code"
	case textExt[ext]:
		return "text"
	}

	return "other"
}

// Detect decides how the browser may show bytes: by content, never by the student's file name alone.
// It returns the kind and the Content-Type to serve. Anything active (HTML, SVG, scripts) is served as plain text.
func Detect(name string, data []byte) (string, string) {
	switch sniff(data) {
	case "pdf":
		return "pdf", "application/pdf"
	case "docx":
		return "docx", "application/octet-stream"
	case "zip", "rar", "7z":
		return "archive", "application/octet-stream"
	case "exe":
		return "exe", "application/octet-stream"
	}

	switch {
	case bytes.HasPrefix(data, []byte("\x89PNG\r\n\x1a\n")):
		return "image", "image/png"
	case bytes.HasPrefix(data, []byte("\xff\xd8\xff")):
		return "image", "image/jpeg"
	case bytes.HasPrefix(data, []byte("GIF8")):
		return "image", "image/gif"
	case len(data) > 12 && string(data[:4]) == "RIFF" && string(data[8:12]) == "WEBP":
		return "image", "image/webp"
	case bytes.HasPrefix(data, []byte("BM")):
		return "image", "image/bmp"
	}

	probe := data[:min(len(data), 8192)]
	for i := 0; i < 3 && len(probe) < len(data) && !utf8.Valid(probe); i++ {
		probe = probe[:len(probe)-1] // the cut may split a multi-byte character
	}

	if utf8.Valid(probe) && !bytes.ContainsRune(probe, 0) {
		if DefaultProfile.IsCode(name) {
			return "code", "text/plain; charset=utf-8"
		}
		return "text", "text/plain; charset=utf-8"
	}

	return "other", "application/octet-stream"
}

// DocxText returns the text of a Word document for the viewer.
func DocxText(data []byte) string {
	var s work.Snapshot
	return docxText(data, &s)
}

const maxImageBytes = 6 << 20

type Image struct {
	Source string // file name inside the upload
	Data   []byte
}

// Images collects pictures a student uploaded, for the vision model: the upload itself if it is an image, images
// embedded in a DOCX (word/media) and images inside an archive. PDFs are rendered by toolsd instead. Each image is
// unpacked alone, with the same limits as the viewer.
func Images(ctx context.Context, name string, data []byte, limit int) []Image {
	kind, _ := Detect(name, data)
	switch kind {
	case "image":
		if len(data) <= maxImageBytes {
			return []Image{{name, data}}
		}
	case "docx", "archive":
		entries, err := List(ctx, name, data, nil, DefaultProfile)
		if err != nil {
			return nil
		}
		var out []Image
		for _, e := range entries {
			if len(out) >= limit {
				break
			}
			if e.Kind != "image" || e.Note != "" || e.Size > maxImageBytes || (kind == "docx" && !strings.HasPrefix(e.Path, "word/media/")) {
				continue
			}
			body, _, err := Open(ctx, name, data, []string{e.Path})
			if err != nil {
				continue
			}
			if k, _ := Detect(e.Path, body); k == "image" {
				out = append(out, Image{name + " › " + e.Path, body})
			}
		}
		return out
	}

	return nil
}
