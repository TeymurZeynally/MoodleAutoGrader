package integrity

import (
	"path"
	"sort"
	"strings"

	"moodle-autograder/internal/domain/work"
)

// Bundle is a work's code in one language, concatenated: Dolos compares files, so each work is one file; Spans map bundle rows
// back to the student's files.
type Bundle struct {
	Language string
	Text     string
	Spans    []Span
}

type Span struct {
	Path         string
	Start, Lines int // first 0-based row in Text, number of rows
}

// dolosLanguage maps file extensions to Dolos (tree-sitter) language names.
var dolosLanguage = map[string]string{
	".cs": "c-sharp", ".java": "java", ".py": "python", ".js": "javascript", ".jsx": "javascript", ".ts": "typescript", ".tsx": "tsx",
	".go": "go", ".php": "php", ".c": "c", ".cpp": "cpp", ".cc": "cpp", ".hpp": "cpp", ".h": "cpp", ".rs": "rust", ".scala": "scala", ".sql": "sql",
}

// Generated reports code that tools write, not students: EF migrations, designer files, assembly info. It would make
// every work look alike.
func Generated(p string) bool {
	lower := strings.ToLower(p)
	base := path.Base(lower)
	return strings.Contains("/"+lower, "/migrations/") || strings.HasSuffix(base, ".designer.cs") || strings.HasSuffix(base, ".g.cs") ||
		strings.HasSuffix(base, ".g.i.cs") || base == "assemblyinfo.cs" || strings.HasSuffix(base, "modelsnapshot.cs") || strings.HasSuffix(base, ".min.js")
}

// PlainText is the Dolos language for files without a parser: protos, JSON, YAML, Dockerfiles, other languages. They
// are compared character by character.
const PlainText = "char"

// Language is the Dolos language a file is compared in.
func Language(p string) string {
	if lang, ok := dolosLanguage[strings.ToLower(path.Ext(p))]; ok {
		return lang
	}

	return PlainText
}

// Comparable lists a work's files the similarity check compares: code and other text files, without generated code.
func Comparable(s work.Snapshot) []work.CodeFile {
	var out []work.CodeFile
	for _, f := range append(append([]work.CodeFile(nil), s.Code...), s.Text...) {
		if !Generated(f.Path) {
			out = append(out, f)
		}
	}

	return out
}

// Bundles groups a work's comparable files by language, in path order.
func Bundles(s work.Snapshot) map[string]*Bundle {
	files := Comparable(s)
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	out := map[string]*Bundle{}
	text := map[string]*strings.Builder{}
	for _, f := range files {
		lang := Language(f.Path)
		b := out[lang]
		if b == nil {
			b, text[lang] = &Bundle{Language: lang}, &strings.Builder{}
			out[lang] = b
		}
		body := strings.TrimRight(strings.ReplaceAll(f.Text, "\r\n", "\n"), "\n") + "\n"
		lines := strings.Count(body, "\n")
		start := 0
		if n := len(b.Spans); n > 0 {
			start = b.Spans[n-1].Start + b.Spans[n-1].Lines
		}
		b.Spans = append(b.Spans, Span{Path: f.Path, Start: start, Lines: lines})
		_, _ = text[lang].WriteString(body) // a strings.Builder never fails
	}

	for lang, b := range out {
		b.Text = text[lang].String()
	}

	return out
}

// Lines is the number of rows in the bundle.
func (b *Bundle) Lines() int {
	if n := len(b.Spans); n > 0 {
		return b.Spans[n-1].Start + b.Spans[n-1].Lines
	}

	return 0
}

// Locate maps a bundle row to a file and its 0-based line.
func (b *Bundle) Locate(row int) (string, int) {
	i := sort.Search(len(b.Spans), func(i int) bool { return b.Spans[i].Start+b.Spans[i].Lines > row })
	if i == len(b.Spans) {
		if i == 0 {
			return "", 0
		}
		i--
	}

	s := b.Spans[i]
	return s.Path, max(0, min(row-s.Start, s.Lines-1))
}

// Region is a matching range of lines in one file of each work (0-based, inclusive).
type Region struct {
	FileA        string
	StartA, EndA int
	FileB        string
	StartB, EndB int
}

// Regions maps fragments of two bundles back to files. A fragment that crosses file boundaries is split into one
// region per pair of file pieces, keeping the rows aligned: row k of the fragment in A matches row k in B.
func Regions(a, b *Bundle, fragments [][4]int) []Region {
	var out []Region
	for _, f := range fragments {
		sa, ea, sb, eb := f[0], f[1], f[2], f[3]
		n := min(ea-sa, eb-sb) // aligned length - 1
		for _, x := range a.pieces(sa, sa+n) {
			for _, y := range b.pieces(sb+(x.from-sa), sb+(x.to-sa)) {
				off := y.from - sb - (x.from - sa) // rows of the A piece skipped before this B piece
				out = append(out, Region{x.path, x.line + off, x.line + off + (y.to - y.from), y.path, y.line, y.line + (y.to - y.from)})
			}
		}
	}

	return out
}

type piece struct {
	path     string
	from, to int // bundle rows, inclusive
	line     int // file line of from
}

// pieces splits bundle rows [from, to] at file boundaries.
func (b *Bundle) pieces(from, to int) []piece {
	var out []piece
	for _, s := range b.Spans {
		lo, hi := max(from, s.Start), min(to, s.Start+s.Lines-1)
		if lo <= hi {
			out = append(out, piece{s.Path, lo, hi, lo - s.Start})
		}
	}

	return out
}

// PlainShare is the part of two works' compared lines that is plain text. Plain-text similarity counts only in that
// share: a template docker-compose.yml that two works share must not make their code a copy.
func PlainShare(a, b map[string]*Bundle) float64 {
	plain, all := 0, 0
	for _, w := range []map[string]*Bundle{a, b} {
		for lang, x := range w {
			all += x.Lines()
			if lang == PlainText {
				plain += x.Lines()
			}
		}
	}

	if all == 0 {
		return 0
	}

	return float64(plain) / float64(all)
}
