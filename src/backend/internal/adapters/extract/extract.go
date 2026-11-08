// Package extract turns raw submission files into a snapshot: source code files and report text.
// Everything here handles untrusted input: types come from content, not extensions, and archives are size-limited.
package extract

import (
	"bytes"
	"context"
	"errors"
	"io"
	"path"
	"regexp"
	"strings"
	"unicode/utf8"

	"moodle-autograder/internal/domain/course"
	"moodle-autograder/internal/domain/work"

	"github.com/mholt/archives"
)

type Limits struct {
	MaxEntries    int   // files inside one archive
	MaxTotalBytes int64 // uncompressed bytes per archive
	MaxFileBytes  int64 // one extracted file
	MaxTextBytes  int64 // the text kept from one work: a snapshot has to stay readable
}

var DefaultLimits = Limits{MaxEntries: 5000, MaxTotalBytes: 200 << 20, MaxFileBytes: 20 << 20, MaxTextBytes: 30 << 20}

// PDFText reads the text of a PDF. It runs outside this process: a crafted PDF of a few hundred bytes hangs or overflows
// the stack of a parser, and here that would stop the whole application.
type PDFText func(ctx context.Context, data []byte) (text string, hidden bool, err error)

// Text files that are not the student's work: lock files and generated data are alike in every work.
var (
	notText     = map[string]bool{".svg": true, ".map": true, ".lock": true, ".sum": true, ".log": true, ".min.js": true, ".min.css": true, ".resx": true, ".pfx": true, ".pem": true, ".key": true}
	notTextName = map[string]bool{"package-lock.json": true, "yarn.lock": true, "pnpm-lock.yaml": true, "go.sum": true, "packages.lock.json": true, "poetry.lock": true, "cargo.lock": true, "composer.lock": true}
)

const maxTextBytes = 512 << 10 // larger text files are data dumps, not code

// plainText reports a text file worth comparing as plain text (the caller checked it is valid UTF-8).
func plainText(name string, data []byte) bool {
	base := strings.ToLower(path.Base(name))
	ext := path.Ext(base)
	if strings.HasSuffix(base, ".min.js") || strings.HasSuffix(base, ".min.css") {
		ext = ".min.js"
	}

	return len(data) <= maxTextBytes && len(bytes.TrimSpace(data)) > 0 && !notText[ext] && !notTextName[base]
}

// Profile is what a course counts as source code and which folders it leaves out (course settings, "Files").
type Profile struct{ code, ignored map[string]bool }

func NewProfile(codeExtensions, ignoredDirs []string) Profile {
	p := Profile{code: map[string]bool{}, ignored: map[string]bool{}}
	for _, e := range codeExtensions {
		p.code[strings.ToLower(e)] = true
	}

	for _, d := range ignoredDirs {
		p.ignored[strings.ToLower(d)] = true
	}

	return p
}

// DefaultProfile is the profile of a course without settings; the viewer also uses it to choose how to show a file.
var DefaultProfile = NewProfile(course.Defaults().CodeExtensions, course.Defaults().IgnoredDirs)

// IsCode reports whether a file is read as source code.
func (p Profile) IsCode(name string) bool { return p.code[strings.ToLower(path.Ext(name))] }

// Skipped reports whether a path is inside build output or dependencies.
func (p Profile) Skipped(name string) bool {
	for part := range strings.SplitSeq(strings.ToLower(name), "/") {
		if p.ignored[part] {
			return true
		}
	}

	return false
}

// Add merges one raw file into the snapshot.
func Add(ctx context.Context, s *work.Snapshot, name string, data []byte, lim Limits, prof Profile, pdf PDFText) {
	kind := sniff(data)
	ext := strings.ToLower(path.Ext(name))
	if (ext == ".pdf" && kind != "pdf") || (ext == ".zip" && kind != "zip" && kind != "docx") {
		s.Flag(work.FlagTypeMismatch)
	}

	r := reading{s, lim, prof, pdf}
	switch kind {
	case "zip", "rar", "7z":
		r.archive(ctx, name, data)
	default:
		r.file(ctx, name, kind, data)
	}
}

// reading is one work being read.
type reading struct {
	s    *work.Snapshot
	lim  Limits
	prof Profile
	pdf  PDFText
}

// fits counts the text kept from the work; past the limit the rest is left out and flagged.
func (r reading) fits(n int) bool {
	r.s.TextBytes += int64(n)
	if r.s.TextBytes > r.lim.MaxTextBytes {
		r.s.Flag(work.FlagArchiveLimit)
		return false
	}

	return true
}

func (r reading) file(ctx context.Context, name, kind string, data []byte) {
	s := r.s
	switch kind {
	case "pdf":
		text, hidden, err := r.pdf(ctx, data)
		if err != nil {
			s.Flag(work.FlagUnreadable)
			return
		}
		if hidden {
			s.Flag(work.FlagHiddenText)
		}
		if r.fits(len(text)) {
			s.Report += text + "\n"
		}
	case "docx":
		if text := docxText(data, s); r.fits(len(text)) {
			s.Report += text + "\n"
		}
	case "exe":
		s.Flag(work.FlagExecutable)
	default:
		if !utf8.Valid(data) {
			return
		}
		if r.prof.IsCode(name) {
			if r.fits(len(data)) {
				s.Code = append(s.Code, work.CodeFile{Path: name, Text: string(data)})
			}
		} else if bytes.IndexByte(data, 0) < 0 && plainText(name, data) && r.fits(len(data)) {
			s.Text = append(s.Text, work.CodeFile{Path: name, Text: string(data)})
		}
	}
}

func (r reading) archive(ctx context.Context, name string, data []byte) {
	s := r.s
	format, _, err := archives.Identify(ctx, name, bytes.NewReader(data))
	ex, ok := format.(archives.Extractor)
	if err != nil || !ok {
		s.Flag(work.FlagUnreadable)
		return
	}

	var entries int
	var total int64
	err = ex.Extract(ctx, bytes.NewReader(data), func(_ context.Context, f archives.FileInfo) error {
		if f.IsDir() {
			return nil
		}
		if entries++; entries > r.lim.MaxEntries {
			return errLimit
		}
		clean := path.Clean("/" + strings.ReplaceAll(f.NameInArchive, `\`, "/"))[1:]
		if f.LinkTarget != "" || f.Mode().Type() != 0 || clean != strings.TrimPrefix(strings.ReplaceAll(f.NameInArchive, `\`, "/"), "./") || strings.HasPrefix(f.NameInArchive, "/") {
			s.Flag(work.FlagUnsafePath)
			return nil
		}
		if r.prof.Skipped(clean) || f.Size() > r.lim.MaxFileBytes {
			return nil
		}
		rc, err := f.Open()
		if err != nil {
			return nil
		}
		defer rc.Close()
		body, err := io.ReadAll(io.LimitReader(rc, r.lim.MaxFileBytes+1))
		if err != nil || int64(len(body)) > r.lim.MaxFileBytes {
			return nil
		}
		// The bytes really read: an archive may not state the size of a file, or state a wrong one.
		if total += int64(len(body)); total > r.lim.MaxTotalBytes {
			return errLimit
		}
		switch kind := sniff(body); kind {
		case "zip", "rar", "7z":
			s.Flag(work.FlagNestedArchive) // not unpacked: limits nesting bombs
		default:
			r.file(ctx, clean, kind, body)
		}
		return nil
	})
	if errors.Is(err, errLimit) {
		s.Flag(work.FlagArchiveLimit)
	} else if err != nil {
		s.Flag(work.FlagUnreadable)
	}
}

var errLimit = errors.New("archive limit exceeded")

func sniff(b []byte) string {
	switch {
	case bytes.HasPrefix(b, []byte("%PDF")):
		return "pdf"
	case bytes.HasPrefix(b, []byte("PK\x03\x04")):
		if bytes.Contains(b, []byte("word/document.xml")) {
			return "docx"
		}
		return "zip"
	case bytes.HasPrefix(b, []byte("Rar!\x1a\x07")):
		return "rar"
	case bytes.HasPrefix(b, []byte("7z\xbc\xaf\x27\x1c")):
		return "7z"
	case bytes.HasPrefix(b, []byte("MZ")), bytes.HasPrefix(b, []byte("\x7fELF")):
		return "exe"
	}

	return "text"
}

// docxText reads the paragraph text of word/document.xml a person sees; macros are flagged, never executed. Text a person
// does not see is left out and flagged (see wordText).
func docxText(data []byte, s *work.Snapshot) string {
	ctx := context.Background()
	format, _, err := archives.Identify(ctx, "x.zip", bytes.NewReader(data))
	ex, ok := format.(archives.Extractor)
	if err != nil || !ok {
		s.Flag(work.FlagUnreadable)
		return ""
	}

	parts := map[string][]byte{}
	_ = ex.Extract(ctx, bytes.NewReader(data), func(_ context.Context, f archives.FileInfo) error {
		switch name := strings.ToLower(f.NameInArchive); {
		case strings.HasSuffix(name, "vbaproject.bin"):
			s.Flag(work.FlagMacros)
		case (name == "word/document.xml" || name == "word/styles.xml") && f.Size() < 20<<20:
			rc, err := f.Open()
			if err != nil {
				return nil
			}
			defer rc.Close()
			parts[name], _ = io.ReadAll(io.LimitReader(rc, 20<<20))
		}
		return nil
	})
	text, hidden := wordText(parts["word/document.xml"], readStyles(parts["word/styles.xml"]))
	if hidden {
		s.Flag(work.FlagHiddenText)
	}

	return text
}

// Zero-width and direction marks; a BOM at the very start of a file is normal (Visual Studio writes one) and ignored.
// Tag characters and the variation selectors supplement carry whole texts invisible to people and readable by models
// ("ASCII smuggling"); nobody types them.
var (
	zeroWidth = regexp.MustCompile(`[\x{200B}-\x{200F}\x{202A}-\x{202E}\x{2060}-\x{2069}\x{FEFF}]`)
	tagChars  = regexp.MustCompile(`[\x{E0000}-\x{E007F}\x{E0100}-\x{E01EF}]`)
)

const minZeroWidth = 3 // one or two come from copying text off web pages

func hiddenText(t string) bool {
	t = strings.TrimPrefix(t, "\uFEFF")
	return tagChars.MatchString(t) || len(zeroWidth.FindAllStringIndex(t, minZeroWidth)) >= minZeroWidth
}

// Scan runs the deterministic detectors over the report and code. Text that tries to talk to the
// grader is found by the AI check's injection detector, not by phrase lists.
func Scan(s *work.Snapshot) {
	if len(s.Code) == 0 {
		s.Flag(work.FlagNoCode)
	}

	if strings.TrimSpace(s.Report) == "" {
		s.Flag(work.FlagNoReport)
	}

	texts := []string{s.Report}
	for _, c := range s.Code {
		texts = append(texts, c.Text)
	}

	for _, t := range texts {
		if hiddenText(t) {
			s.Flag(work.FlagInvisibleChars)
		}
	}
}
