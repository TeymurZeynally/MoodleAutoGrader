package llm

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode"

	"moodle-autograder/internal/domain/assessment"
	"moodle-autograder/internal/domain/integrity"
	"moodle-autograder/internal/domain/work"
)

const (
	codeBudget   = 45000 // characters of code sent to the model (about 15k tokens)
	fileBudget   = 8000  // per file
	reportBudget = 16000
	notesBudget  = 12000
)

const maxFileList = 1500

func PrepareInput(snap work.Snapshot, pages []assessment.PageNote, upload []string) assessment.Work {
	return PrepareInputShare(snap, pages, upload, 1)
}

// PrepareInputShare is PrepareInput with the code and report budgets scaled by share (0 < share <= 1), for a second
// try when the work did not fit into the model context. Smaller files are kept first.
func PrepareInputShare(snap work.Snapshot, pages []assessment.PageNote, upload []string, share float64) assessment.Work {
	codeBudget, reportBudget := int(float64(codeBudget)*share), int(float64(reportBudget)*share)
	if len(upload) > maxFileList {
		upload = append(upload[:maxFileList:maxFileList], fmt.Sprintf("… and %d more files", len(upload)-maxFileList))
	}

	files := append([]work.CodeFile(nil), snap.Code...)
	if share < 1 { // keep the smaller files: the large ones are usually generated or vendored
		sort.Slice(files, func(i, j int) bool { return len(files[i].Text) < len(files[j].Text) })
	} else {
		sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	}

	in := assessment.Work{Report: clip(snap.Report, reportBudget), Pages: pages, Files: upload, Truncated: len(snap.Report) > reportBudget || share < 1}
	used := 0
	for _, f := range files {
		if integrity.Generated(f.Path) || strings.HasSuffix(strings.ToLower(f.Path), ".csproj") {
			continue
		}
		text := blankLines.ReplaceAllString(f.Text, "\n") // as written: the injection check reads the same text
		if len(text) > fileBudget {
			text, in.Truncated = text[:fileBudget]+"\n… (file truncated)", true
		}
		if used+len(text) > codeBudget {
			in.Truncated = true
			continue
		}
		used += len(text)
		in.Code = append(in.Code, work.CodeFile{Path: f.Path, Text: text})
	}

	return in
}

var blankLines = regexp.MustCompile(`\n[ \t]*(\n[ \t]*)+\n`)

// ChecklistText is the checklist as the model reads it, extra tasks listed apart.
func ChecklistText(criteria, extras []assessment.Criterion) string {
	var list strings.Builder
	for _, c := range criteria {
		fmt.Fprintf(&list, "%s [%s] (%g points): %s\n", c.ID, c.Where, c.Points, c.Text)
	}

	if len(extras) > 0 {
		list.WriteString("Extra tasks (not scored, report them in \"extras\"):\n")
		for _, c := range extras {
			fmt.Fprintf(&list, "%s [%s]: %s\n", c.ID, c.Where, c.Text)
		}
	}

	return strings.TrimSpace(list.String())
}

// WorkText is the student's work as the checklist call sees it.
func WorkText(in assessment.Work) string {
	var work strings.Builder
	for _, f := range in.Code {
		fmt.Fprintf(&work, "=== file: %s\n%s\n", f.Path, f.Text)
	}

	fmt.Fprintf(&work, "=== report text\n%s\n", in.Report)
	fmt.Fprintf(&work, "=== files in the upload\n%s\n", strings.Join(in.Files, "\n"))
	for _, pg := range in.Pages {
		fmt.Fprintf(&work, "=== screenshot %s\n%s\n%s\n", pg.Source, pg.Description, clip(pg.Transcript, 1500))
		for _, a := range pg.Addressed {
			fmt.Fprintf(&work, "text on this picture addressed to its reader: %s\n", clip(a, 500))
		}
	}

	return work.String()
}

// wrap marks the student's text as data with a boundary the student cannot know. The model gets the text as a person
// reads it: without characters nobody sees.
func wrap(boundary, text string) string {
	text = strings.ReplaceAll(invisible.ReplaceAllString(text, ""), boundary, "")
	return fmt.Sprintf("<<<STUDENT-DATA %s>>>\n%s\n<<<END STUDENT-DATA %s>>>", boundary, text, boundary)
}

// invisible are characters a person does not see but a model may read: zero-width and direction marks, tag characters
// ("ASCII smuggling"), variation selectors, fillers and the soft hyphen. Their use is flagged when the work is read.
var invisible = regexp.MustCompile(`[\x{00AD}\x{034F}\x{115F}\x{1160}\x{17B4}\x{17B5}\x{180B}-\x{180F}\x{200B}-\x{200F}\x{202A}-\x{202E}\x{2060}-\x{206F}\x{3164}\x{FE00}-\x{FE0F}\x{FEFF}\x{FFA0}\x{E0000}-\x{E007F}\x{E0100}-\x{E01EF}]`)

func nonce() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

var htmlTag = regexp.MustCompile(`<[^>]*>`)

func plain(html string) string {
	return strings.TrimSpace(strings.NewReplacer("&nbsp;", " ", "&amp;", "&", "&lt;", "<", "&gt;", ">", "&quot;", `"`).Replace(htmlTag.ReplaceAllString(html, " ")))
}

// StudentText is who submitted the work, from Moodle: the model uses it to judge whether the screenshots show the
// student's own name and group.
func StudentText(st assessment.Student) string {
	return fmt.Sprintf("Student: %s; group: %s.", label(st.FullName), label(strings.Join(st.Groups, ", ")))
}

// label keeps a name short and on one line: a student may be able to edit it in Moodle.
func label(s string) string {
	s = strings.Map(func(r rune) rune {
		if invisible.MatchString(string(r)) {
			return -1
		}
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, s)
	return clip(strings.TrimSpace(s), 120)
}

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}

	for n > 0 && s[n]&0xC0 == 0x80 {
		n--
	}

	return s[:n] + " …"
}
