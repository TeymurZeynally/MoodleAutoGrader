package assessment

import (
	"path"
	"sort"
	"strings"
	"unicode"

	"moodle-autograder/internal/domain/integrity"
	"moodle-autograder/internal/domain/work"
)

// minQuote is the shortest quote, in letters and digits, that proves anything.
const minQuote = 8

// VerifyInjection keeps only quotes found in the student's text: a confirmed alarm stops the grading. An alarm whose
// quotes are not found is not dropped: the work is graded, and the teacher is asked to look (Unverified), because text
// written to be misread (mixed alphabets, invisible characters) is exactly what the model quotes differently.
func VerifyInjection(in Injection, haystack string) Injection {
	h := loose(haystack)
	var kept []string
	for _, q := range in.Quotes {
		if nq := loose(q); len([]rune(nq)) >= minQuote && mostlyFound(h, nq) {
			kept = append(kept, q)
		}
	}

	if in.Suspected && len(kept) == 0 {
		in.Suspected, in.Unverified = false, true // the model's quotes stay for the teacher to see
		return in
	}

	in.Quotes = kept
	return in
}

// verify looks for the quote where the criterion's place allows, ignoring case, spacing, punctuation, ё/е and letters of
// other alphabets that look the same:
//   - archive: only the list of uploaded files (a code listing in the report is not an archive);
//   - code: the code outside its comments (a comment saying what the code does is no proof), or the code listing in the report;
//   - screenshots: only the text read off the pictures; neither the report saying what a screenshot shows nor the
//     model's own description of a picture proves anything (a picture can tell the model how to describe it);
//   - report: the report text and the text read off the pictures (a title page is often a picture).
//
// The source the model named is a hint, not a requirement.
func verify(e Evidence, where string, in Work, report, shots string) (bool, int, string) {
	q := loose(e.Quote)
	if len([]rune(q)) < minQuote {
		return false, 0, e.File
	}

	in2 := func(hay string) bool { return containsLoose(hay, q) }
	if where == "" { // a checklist without places: follow the model's source
		where = map[string]string{"screenshot": "screenshots"}[e.Source]
		if where == "" {
			where = e.Source
		}
	}

	switch where {
	case "archive":
		return in2(loose(strings.Join(in.Files, "\n"))), 0, e.File
	case "code":
		files := append([]work.CodeFile(nil), in.Code...)
		sort.SliceStable(files, func(i, j int) bool { return sameFile(files[i].Path, e.File) && !sameFile(files[j].Path, e.File) })
		for _, f := range files {
			if in2(loose(integrity.StripComments(f.Text))) {
				return true, lineOf(f.Text, e.Quote), f.Path
			}
		}
		return in2(report), 0, e.File
	case "screenshots":
		return in2(shots), 0, e.File
	default: // report
		return in2(report) || in2(shots), 0, e.File
	}
}

// loose keeps only letters and digits, lowercased, with ё as е and look-alike letters of Cyrillic and Greek as Latin, and
// styled letters (fullwidth, mathematical bold or italic) as plain ones: quotes survive the model's small changes in
// punctuation, spacing and line breaks, PDF text extraction quirks, and text written in mixed alphabets to be misread.
func loose(s string) string {
	var b strings.Builder
	for _, r := range s {
		r = plainLetter(r)
		if l, ok := lookalike[r]; ok { // capitals first: Cyrillic Н is Latin H, while its lowercase н is not h
			b.WriteRune(l)
			continue
		}
		if r = unicode.ToLower(r); unicode.IsLetter(r) || unicode.IsDigit(r) {
			if l, ok := lookalike[r]; ok {
				r = l
			}
			b.WriteRune(r)
		}
	}

	return b.String()
}

// lookalike maps Cyrillic and Greek letters to the Latin letters they look like.
var lookalike = map[rune]rune{
	'А': 'a', 'В': 'b', 'Е': 'e', 'К': 'k', 'М': 'm', 'Н': 'h', 'О': 'o', 'Р': 'p', 'С': 'c', 'Т': 't', 'У': 'y', 'Х': 'x', 'Ѕ': 's', 'І': 'i', 'Ј': 'j',
	'а': 'a', 'е': 'e', 'ё': 'e', 'о': 'o', 'р': 'p', 'с': 'c', 'у': 'y', 'х': 'x', 'к': 'k', 'ѕ': 's', 'і': 'i', 'ј': 'j', 'ԁ': 'd', 'һ': 'h', 'ԛ': 'q', 'ԝ': 'w', 'ӏ': 'l',
	'Α': 'a', 'Β': 'b', 'Ε': 'e', 'Ζ': 'z', 'Η': 'h', 'Ι': 'i', 'Κ': 'k', 'Μ': 'm', 'Ν': 'n', 'Ο': 'o', 'Ρ': 'p', 'Τ': 't', 'Υ': 'y', 'Χ': 'x',
	'α': 'a', 'ο': 'o', 'ι': 'i', 'κ': 'k', 'ν': 'v', 'ρ': 'p', 'υ': 'u', 'χ': 'x',
}

// plainLetter turns fullwidth and mathematical alphanumeric letters and digits into plain ASCII ones.
func plainLetter(r rune) rune {
	switch {
	case r >= 0xFF01 && r <= 0xFF5E: // fullwidth ASCII
		return r - 0xFEE0
	case r >= 0x1D400 && r <= 0x1D6A3: // mathematical bold, italic, script, ... A-Z a-z, 52 letters per style
		if n := (r - 0x1D400) % 52; n < 26 {
			return 'A' + n
		}
		return 'a' + (r-0x1D400)%52 - 26
	case r >= 0x1D7CE && r <= 0x1D7FF: // mathematical digits, 10 per style
		return '0' + (r-0x1D7CE)%10
	}

	return r
}

// containsLoose accepts a quote found as is, or, for a long quote the model may have shortened or ended freely, its
// first 80 characters.
func containsLoose(hay, q string) bool {
	if strings.Contains(hay, q) {
		return true
	}

	r := []rune(q)
	return len(r) > 120 && strings.Contains(hay, string(r[:80]))
}

// mostlyFound accepts an alarm's quote of which two thirds of the 20-letter pieces are in the text: copying text written
// to be misread, a model makes small mistakes, and an alarm must not be lost to them.
func mostlyFound(hay, q string) bool {
	if containsLoose(hay, q) {
		return true
	}

	const piece = 20
	r := []rune(q)
	if len(r) < 2*piece {
		return false
	}

	found, total := 0, 0
	for i := 0; i+piece <= len(r); i += piece {
		total++
		if strings.Contains(hay, string(r[i:i+piece])) {
			found++
		}
	}

	return 3*found >= 2*total
}

func sameFile(p, name string) bool {
	return name != "" && (p == name || path.Base(p) == path.Base(name) || strings.HasSuffix(p, "/"+strings.TrimPrefix(name, "/")))
}

func lineOf(text, quote string) int {
	first := loose(strings.SplitN(strings.TrimSpace(quote), "\n", 2)[0])
	for i, l := range strings.Split(text, "\n") {
		if first != "" && strings.Contains(loose(l), first) {
			return i + 1
		}
	}

	return 0
}

func anyVerified(ev []Evidence) bool {
	for _, e := range ev {
		if e.Verified {
			return true
		}
	}

	return false
}
