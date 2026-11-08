// Package integrity finds copied work within an assignment: identical files, code structure that survives
// renaming, and report text with all student names removed. Results are signals for the teacher, not verdicts.
package integrity

import (
	"crypto/sha256"
	"encoding/hex"
	"hash/fnv"
	"regexp"
	"strings"

	"moodle-autograder/internal/domain/work"
)

const (
	codeK         = 12 // tokens per code shingle
	reportK       = 6  // words per report shingle
	maxShingles   = 20000
	minHashedFile = 200 // bytes; tiny files (configs, usings) are not evidence
)

type Features struct {
	FileHashes     []string
	CodeShingles   []int64
	ReportShingles []int64
	CodeFiles      int
	ReportChars    int
}

type Match struct {
	CodeSimilarity, ReportSimilarity float64
	IdenticalFiles                   int
	Severity                         string // "high" or "medium"
}

var (
	lineComment  = regexp.MustCompile(`(?m)(//|#).*$`)
	blockComment = regexp.MustCompile(`(?s)/\*.*?\*/`)
	codeToken    = regexp.MustCompile(`[A-Za-z_][A-Za-z0-9_]*|\d+|"(?:[^"\\]|\\.)*"|\S`)
	word         = regexp.MustCompile(`[\p{L}\p{N}]+`)
)

// keywords keep their identity; every other identifier becomes "v", so renaming variables does not hide a copy.
var keywords = toSet("abstract as async await base bool break case catch char class const continue decimal default delegate do double else enum event explicit extern false finally fixed float for foreach goto if implicit in int interface internal is lock long namespace new null object operator out override params private protected public readonly ref return sbyte sealed short sizeof static string struct switch this throw true try typeof uint ulong unsafe ushort using var virtual void volatile while yield record init get set value def import from func package type map chan go defer select range let function export extends implements final throws elif lambda pass raise with match fn mut impl pub use")

// Extract fingerprints a work. Report text that repeats the assignment description (intro) is not evidence of copying
// and is dropped.
func Extract(s work.Snapshot, studentNames []string, intro string) Features {
	f := Features{CodeFiles: len(s.Code), ReportChars: len(s.Report)}
	code := map[int64]bool{}
	for _, c := range s.Code {
		norm := stripComments(c.Text)
		if compact := strings.Join(strings.Fields(norm), ""); len(compact) >= minHashedFile {
			sum := sha256.Sum256([]byte(compact))
			f.FileHashes = append(f.FileHashes, hex.EncodeToString(sum[:]))
		}
		toks, _ := codeTokens(norm)
		addShingles(code, toks, codeK)
	}

	f.CodeShingles = keys(code)
	report, task := map[int64]bool{}, map[int64]bool{}
	words, _ := reportWords(s.Report, studentNames)
	addShingles(report, words, reportK)
	iw, _ := reportWords(stripTags(intro), nil)
	addShingles(task, iw, reportK)
	for h := range task {
		delete(report, h)
	}

	f.ReportShingles = keys(report)
	return f
}

// StripComments blanks comments out but keeps every newline, so token offsets still map to source lines.
func StripComments(src string) string { return stripComments(src) }

func stripComments(src string) string {
	blank := func(m string) string {
		return strings.Map(func(r rune) rune {
			if r == '\n' {
				return r
			}
			return ' '
		}, m)
	}
	return lineComment.ReplaceAllStringFunc(blockComment.ReplaceAllStringFunc(src, blank), blank)
}

// codeTokens normalizes code: keywords keep their identity, identifiers become "v", literals collapse.
// It also returns the 0-based source line of every token.
func codeTokens(norm string) ([]string, []int) {
	var toks []string
	var lines []int
	line, last := 0, 0
	for _, loc := range codeToken.FindAllStringIndex(norm, -1) {
		line += strings.Count(norm[last:loc[0]], "\n")
		last = loc[0]
		t := norm[loc[0]:loc[1]]
		switch {
		case keywords[t]:
		case t[0] == '"':
			t = `"s"`
		case t[0] >= '0' && t[0] <= '9':
			t = "0"
		case isIdent(t):
			t = "v"
		}
		toks, lines = append(toks, t), append(lines, line)
	}

	return toks, lines
}

// reportWords lowercases the report and drops every part of every student name; it returns words and their lines.
func reportWords(report string, studentNames []string) ([]string, []int) {
	text := strings.ToLower(report)
	for _, n := range studentNames {
		for part := range strings.FieldsSeq(strings.ToLower(n)) {
			text = strings.ReplaceAll(text, part, strings.Repeat(" ", len(part)))
		}
	}

	var words []string
	var lines []int
	line, last := 0, 0
	for _, loc := range word.FindAllStringIndex(text, -1) {
		line += strings.Count(text[last:loc[0]], "\n")
		last = loc[0]
		words, lines = append(words, text[loc[0]:loc[1]]), append(lines, line)
	}

	return words, lines
}

// Thresholds decide when a pair of works is worth showing: Similar and Copy are shares (0..1) of code or report
// similarity, CopyFiles is how many identical files make a copy when they are at least half of the smaller work.
// They come from the course settings.
type Thresholds struct {
	Similar, Copy float64
	CopyFiles     int
}

// Template says when shared code is a template rather than evidence: in a cohort of at least MinWorks works, shingles
// found in more than Share of them. It comes from the course settings.
type Template struct {
	MinWorks int
	Share    float64
}

// Compare scores two submissions with the built-in fingerprints (used when Dolos is not configured).
func Compare(a, b Features, t Thresholds) (Match, bool) {
	m := Score(a, b)
	return m, m.Classify(t, len(a.FileHashes), len(b.FileHashes))
}

// Score measures overlap symmetrically (Dice: shared / average size), so a small work that is mostly template code
// does not get a high score against a large one. CodeSimilarity may later be replaced by Dolos.
func Score(a, b Features) Match {
	m := Match{CodeSimilarity: dice(a.CodeShingles, b.CodeShingles), ReportSimilarity: dice(a.ReportShingles, b.ReportShingles)}
	hb := toSet(strings.Join(b.FileHashes, " "))
	for _, h := range a.FileHashes {
		if hb[h] {
			m.IdenticalFiles++
		}
	}

	return m
}

// Classify sets Severity and reports whether the pair is worth showing. filesA and filesB count hashed files.
func (m *Match) Classify(t Thresholds, filesA, filesB int) bool {
	switch {
	case m.CodeSimilarity >= t.Copy || m.ReportSimilarity >= t.Copy || (m.IdenticalFiles >= t.CopyFiles && m.IdenticalFiles*2 >= min(filesA, filesB)):
		m.Severity = "high"
	case m.CodeSimilarity >= t.Similar || m.ReportSimilarity >= t.Similar || m.IdenticalFiles >= 1:
		m.Severity = "medium"
	default:
		m.Severity = ""
		return false
	}

	return true
}

// DropCommon removes template shingles (see Template): shared templates are not evidence of copying.
func DropCommon(all []Features, t Template) {
	if len(all) < t.MinWorks {
		return
	}

	for _, get := range []func(*Features) *[]int64{func(f *Features) *[]int64 { return &f.CodeShingles }, func(f *Features) *[]int64 { return &f.ReportShingles }} {
		df := map[int64]int{}
		for i := range all {
			for _, s := range *get(&all[i]) {
				df[s]++
			}
		}
		for i := range all {
			sh := get(&all[i])
			kept := (*sh)[:0]
			for _, s := range *sh {
				if float64(df[s]) <= t.Share*float64(len(all)) {
					kept = append(kept, s)
				}
			}
			*sh = kept
		}
	}
}

func addShingles(set map[int64]bool, toks []string, k int) {
	for i := 0; i+k <= len(toks) && len(set) < maxShingles; i++ {
		set[shingle(toks[i:i+k])] = true
	}
}

func shingle(toks []string) int64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(strings.Join(toks, "\x00"))) // a hash never fails
	return int64(h.Sum64())                            //nolint:gosec // G115: the bits of the hash, not a quantity: wrapping around is harmless
}

func dice(a, b []int64) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}

	in := make(map[int64]bool, len(b))
	for _, x := range b {
		in[x] = true
	}

	var common int
	for _, x := range a {
		if in[x] {
			common++
		}
	}

	return 2 * float64(common) / float64(len(a)+len(b))
}

var tag = regexp.MustCompile(`<[^>]*>`)

func stripTags(html string) string { return tag.ReplaceAllString(html, " ") }

func keys(m map[int64]bool) []int64 {
	out := make([]int64, 0, len(m))
	for k := range m {
		out = append(out, k)
	}

	return out
}

func toSet(s string) map[string]bool {
	set := map[string]bool{}
	for w := range strings.FieldsSeq(s) {
		set[w] = true
	}

	return set
}

func isIdent(t string) bool {
	c := t[0]
	return c == '_' || (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z')
}
