package httpapi

import (
	"fmt"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"moodle-autograder/internal/app/grading"
	"moodle-autograder/internal/domain/assessment"
)

// exportSheet sends the sheet of an assignment as an Excel workbook, for a teacher to read and annotate away from the
// application.
func (s *Server) exportSheet(w http.ResponseWriter, r *http.Request, a authed) {
	id, ok := s.assignment(w, r, a)
	if !ok {
		return
	}

	e, err := s.Grading.Export(r.Context(), id)
	if err != nil {
		s.fail(w, err)
		return
	}

	data, err := workbook(exportSheets(e))
	if err != nil {
		s.fail(w, err)
		return
	}

	h := w.Header()
	h.Set("Content-Type", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet")
	h.Set("Content-Disposition", `attachment; filename="sheet.xlsx"; filename*=UTF-8''`+url.PathEscape(fileName(e.Assignment.Name)+".xlsx"))
	h.Set("Cache-Control", "private, no-store")
	_, _ = w.Write(data)
}

// exportSheets lays an export out as two sheets: the works with their originality and a column per criterion, and the
// AI's reason for each verdict, a block of rows per student, which also says what each criterion is.
func exportSheets(e grading.Export) []sheet {
	head := []cell{hdr("Group"), hdr("Student"), hdr("Work"), hdr("Submitted"), hdr("Late"), hdr("Originality"), hdr("Similar works")}
	widths := []float64{16, 30, 18, 16, 8, 16, 40}
	for i, c := range e.Criteria {
		head = append(head, hdr(fmt.Sprintf("Criterion %d\n%s pts", i+1, points(c.Points))))
		widths = append(widths, 10)
	}

	head = append(head, hdr("AI points"), hdr("Penalty"), hdr("Total"), hdr("Comment for the student"), hdr("Status"))
	widths = append(widths, 10, 9, 8, 70, 20)
	works := [][]cell{head}
	reasons := sheet{name: "Reasons", widths: []float64{16, 30, 5, 50, 28, 8, 6, 55, 55}, freezeCols: 2, rows: [][]cell{
		{hdr("Group"), hdr("Student"), hdr("No."), hdr("Criterion"), hdr("Decision"), hdr("Points"), hdr("Of"), hdr("Note of the model"), hdr("Quote from the work")},
	}}
	for _, x := range e.Works {
		group := strings.Join(x.Groups, ", ")
		row := []cell{txt(group), txt(x.Student), {text: "Open in Moodle", link: x.GraderURL, style: styleLink}, txt(when(x)), txt(yes(x.Late)), originality(x.Row), txt(matches(x.Matches))}
		verdicts := map[string]assessment.CriterionResult{}
		if x.AI != nil {
			for _, c := range x.AI.Criteria {
				verdicts[c.ID] = c
			}
		}
		for _, c := range e.Criteria {
			if v, ok := verdicts[c.ID]; ok {
				row = append(row, verdict(v))
			} else {
				row = append(row, txt(""))
			}
		}
		var score, penalty *float64
		if x.AI != nil {
			score = &x.AI.Score
			if x.AI.LatePenalty > 0 {
				penalty = &x.AI.LatePenalty
			}
		}
		works = append(works, append(row, num(score), num(penalty), num(x.Points), txt(x.Comment), txt(status(x.Row))))
		if x.AI == nil || len(x.AI.Criteria) == 0 {
			continue
		}
		first := len(reasons.rows) + 1 // the row number of the student's first criterion
		for i, c := range x.AI.Criteria {
			reasons.rows = append(reasons.rows, []cell{txt(group), txt(x.Student), num(new(float64(i + 1))), txt(c.Text), decision(c), num(&c.Awarded), num(&c.Points), txt(c.Note), txt(proof(c))})
		}
		for i := range reasons.rows[first-1] {
			reasons.rows[first-1][i].top = true // a thick line between students
		}
		if last := len(reasons.rows); last > first {
			reasons.merges = append(reasons.merges, fmt.Sprintf("A%d:A%d", first, last), fmt.Sprintf("B%d:B%d", first, last))
		}
	}

	return []sheet{{name: "Sheet", widths: widths, rows: works, freezeCols: 2}, reasons}
}

func hdr(s string) cell { return cell{text: s, style: styleHeader} }
func txt(s string) cell { return cell{text: s, style: styleText} }
func num(v *float64) cell {
	if v == nil {
		return txt("")
	}

	return cell{num: v, style: styleNumber}
}

func points(v float64) string { return strconv.FormatFloat(v, 'f', -1, 64) }

func yes(b bool) string {
	if b {
		return "yes"
	}

	return ""
}

func when(x grading.ExportWork) string {
	if x.SubmittedAt == nil {
		return ""
	}

	return x.SubmittedAt.Local().Format("2006-01-02 15:04")
}

// originality is the verdict of the similarity check, as the sheet shows it.
func originality(r grading.Row) cell {
	switch r.Integrity {
	case "high":
		for _, m := range r.Matches {
			if m.Severity == "high" {
				return cell{text: "Copy", style: styleNotMet}
			}
		}
		return cell{text: "Check manually", style: styleNotMet}
	case "medium":
		return cell{text: "Similar", style: stylePartial}
	default:
		return cell{text: "Original", style: styleMet}
	}
}

// matches lists the works most like this one, a line each, as the sheet shows them.
func matches(ms []grading.Match) string {
	var lines []string
	for _, m := range ms[:min(len(ms), 3)] {
		name := m.ShortName
		if name == "" {
			name = m.Student
		}
		if len(m.Groups) > 0 {
			name += " (" + strings.Join(m.Groups, ", ") + ")"
		}
		line := fmt.Sprintf("%s: code %d%%, report %d%%", name, int(math.Round(m.CodeSimilarity*100)), int(math.Round(m.ReportSimilarity*100)))
		if m.IdenticalFiles > 0 {
			line += fmt.Sprintf(", %d identical files", m.IdenticalFiles)
		}
		lines = append(lines, line)
	}

	return strings.Join(lines, "\n")
}

// verdict is the cell of one criterion: what was awarded of its points, coloured.
func verdict(c assessment.CriterionResult) cell {
	switch {
	case c.Points > 0 && c.Awarded >= c.Points:
		return cell{text: "✓ " + points(c.Awarded), style: styleMet}
	case c.Awarded > 0:
		return cell{text: "½ " + points(c.Awarded), style: stylePartial}
	default:
		return cell{text: "✗ 0", style: styleNotMet}
	}
}

// decision is the model's verdict on a criterion. Without a quote found in the work it still counts, as in the
// application, and says so: the teacher is asked to look.
func decision(c assessment.CriterionResult) cell {
	switch {
	case c.Status == "met" && c.NoQuote:
		return cell{text: "met, no quote found: check", style: stylePartial}
	case c.Status == "partial" && c.NoQuote:
		return cell{text: "partly, no quote found: check", style: stylePartial}
	case c.Status == "met":
		return cell{text: "met", style: styleMet}
	case c.Status == "partial":
		return cell{text: "partly", style: stylePartial}
	default:
		return cell{text: "not met", style: styleNotMet}
	}
}

// status says where a work stands, as the sheet does.
func status(r grading.Row) string {
	switch {
	case r.State == "published":
		return "Published"
	case r.State == "approved":
		return "Publishing"
	case r.Integrity != "ok" || len(r.Flags) > 0 || (r.AI != nil && r.AI.Status == "needs_attention"):
		return "Needs attention"
	case r.Points != nil:
		return "Ready to publish"
	default:
		return "Not graded"
	}
}

// proof is the first quote of the work that was found in it.
func proof(c assessment.CriterionResult) string {
	for _, e := range c.Evidence {
		if e.Verified && e.Quote != "" {
			return e.Quote
		}
	}

	return ""
}

// fileName keeps an assignment's name usable as a file name.
func fileName(name string) string {
	name = strings.Map(func(r rune) rune {
		if strings.ContainsRune(`/\:*?"<>|`, r) || r < 32 {
			return '_'
		}
		return r
	}, strings.TrimSpace(name))
	if name == "" {
		return "sheet"
	}

	return name
}
