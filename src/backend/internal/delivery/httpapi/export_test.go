package httpapi

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"errors"
	"io"
	"slices"
	"strings"
	"testing"
	"time"

	"moodle-autograder/internal/app/grading"
	"moodle-autograder/internal/domain/assessment"
)

// The workbook is well-formed XML in every part, holds the link to the work in Moodle and keeps what students wrote,
// however odd, as text.
func TestTheExportIsAWorkbookWithALinkToEachWork(t *testing.T) {
	three := 3.0
	at := time.Date(2026, 11, 19, 18, 30, 0, 0, time.Local)
	e := grading.Export{
		Assignment: grading.Header{Name: "Лабораторная работа №1"},
		Criteria:   []assessment.Criterion{{ID: "c1", Text: "Титульный лист", Points: 5}, {ID: "c2", Text: "Схема", Points: 5}},
		Works: []grading.ExportWork{{
			ID: 8, Student: "Иванов Иван", Groups: []string{"ГР-1"}, SubmittedAt: &at, Points: &three, State: "draft", Integrity: "medium",
			Matches:   []grading.Match{{ShortName: "Петров П.", Groups: []string{"ГР-2"}, ReportSimilarity: 0.62, IdenticalFiles: 1, Severity: "medium"}},
			GraderURL: "https://moodle.example/mod/assign/view.php?id=30&action=grader&userid=12", Comment: "=1+1 <b>\"&\" \x0b конец\nвторая строка",
			AI: &assessment.Result{Score: 7.5, Criteria: []assessment.CriterionResult{
				{ID: "c1", Text: "Титульный лист", Points: 5, Awarded: 5, Status: "met", Note: "Есть.", Evidence: []assessment.Evidence{{Quote: "Иванов", Verified: true}}},
				{ID: "c2", Text: "Схема", Points: 5, Awarded: 2.5, Status: "partial"},
			}},
		}},
	}
	data, err := workbook(exportSheets(e))
	if err != nil {
		t.Fatal(err)
	}

	z, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}

	all := map[string]string{}
	for _, f := range z.File {
		r, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(r)
		_ = r.Close()
		d := xml.NewDecoder(bytes.NewReader(body))
		for {
			if _, err := d.Token(); errors.Is(err, io.EOF) {
				break
			} else if err != nil {
				t.Fatalf("%s is not well-formed: %v", f.Name, err)
			}
		}
		all[f.Name] = string(body)
	}

	for _, part := range []string{"[Content_Types].xml", "_rels/.rels", "xl/workbook.xml", "xl/_rels/workbook.xml.rels", "xl/styles.xml", "xl/worksheets/sheet1.xml", "xl/worksheets/sheet2.xml"} {
		if _, ok := all[part]; !ok {
			t.Fatalf("no %s", part)
		}
	}

	works := all["xl/worksheets/sheet1.xml"]
	for _, want := range []string{
		`HYPERLINK(&#34;https://moodle.example/mod/assign/view.php?id=30&amp;action=grader&amp;userid=12&#34;,&#34;Open in Moodle&#34;)`,
		"Иванов Иван", "2026-11-19 18:30", "✓ 5", "½ 2.5", "Needs attention", "Similar", "Петров П. (ГР-2): code 0%, report 62%, 1 identical files",
		`t="inlineStr"><is><t xml:space="preserve">=1+1 &lt;b&gt;&#34;&amp;&#34; ` + "\uFFFD" + ` конец&#xA;вторая строка</t>`,
	} {
		if !strings.Contains(works, want) {
			t.Fatalf("the sheet of works lacks %q:\n%s", want, works)
		}
	}

	if !strings.Contains(all["xl/worksheets/sheet2.xml"], "Иванов") || !strings.Contains(all["xl/worksheets/sheet2.xml"], "Титульный лист") || len(z.File) != 7 {
		t.Fatal("the reasons, with what each criterion is, and no third sheet")
	}

	if !strings.Contains(all["xl/workbook.xml"], `name="Sheet"`) || colName(0) != "A" || colName(25) != "Z" || colName(26) != "AA" || colName(27) != "AB" {
		t.Fatal("sheet names and column letters")
	}
}

// The reasons of one student are one block: a merged name, a thick line above.
func TestTheExportKeepsTheReasonsOfAStudentTogether(t *testing.T) {
	e := grading.Export{
		Criteria: []assessment.Criterion{{ID: "c1", Points: 2.5}},
		Works: []grading.ExportWork{{
			ID: 8, Student: "Ivan Ivanov", Integrity: "ok", GraderURL: "https://moodle.example/x",
			AI: &assessment.Result{Criteria: []assessment.CriterionResult{{ID: "c1", Points: 2.5, Awarded: 2.5, Status: "met"}, {ID: "c2", Points: 1, Status: "not_met"}}},
		}},
	}
	sheets := exportSheets(e)
	if sheets[0].name != "Sheet" || sheets[0].rows[0][7].text != "Criterion 1\n2.5 pts" || sheets[0].rows[1][2].text != "Open in Moodle" || sheets[0].rows[1][5].text != "Original" {
		t.Fatalf("English: %+v", sheets[0].rows)
	}

	if d := decision(assessment.CriterionResult{Status: "met", NoQuote: true, Points: 5, Awarded: 5}); d.text != "met, no quote found: check" || d.style != stylePartial {
		t.Fatalf("a verdict without a quote counts and asks to look: %+v", d)
	}

	reasons := sheets[1]
	if len(reasons.rows) != 3 || !reasons.rows[1][0].top || reasons.rows[2][0].top || !slices.Equal(reasons.merges, []string{"A2:A3", "B2:B3"}) {
		t.Fatalf("a block per student: %+v %v", reasons.rows, reasons.merges)
	}

	if len(cellFormats) != int(styles) || !strings.Contains(sheetXML(reasons), `<mergeCell ref="A2:A3"/>`) || !strings.Contains(sheetXML(reasons), `s="8"`) {
		t.Fatalf("styles and their thick variants: %d formats for %d styles", len(cellFormats), styles)
	}
}
