package httpapi

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"fmt"
	"strconv"
	"strings"
)

// A small Office Open XML workbook writer: text, numbers, links (HYPERLINK formulas), a few fixed styles with a thick
// line above as a variant, merged cells, a frozen header row, column widths and a filter. Enough for a sheet a teacher
// opens in Excel or LibreOffice.

type cellStyle int

// The styles of cells; 0 is the default.
const (
	styleText    cellStyle     = iota + 1 // wrapped, at the top, bordered
	styleHeader                           // bold on grey
	styleNumber                           // centered, up to two decimals
	styleLink                             // blue and underlined
	styleMet                              // green
	stylePartial                          // yellow
	styleNotMet                           // red
	styles       = styleNotMet            // each has a variant with a thick line above: style + styles
)

type cell struct {
	text  string
	num   *float64 // a number instead of text
	link  string   // with text: a link that shows the text
	style cellStyle
	top   bool // a thick line above: a new block of rows starts here
}

type sheet struct {
	name       string
	widths     []float64 // of the columns, in characters
	rows       [][]cell  // the first one is the header: kept in view and filtered by
	freezeCols int       // columns kept in view on the left
	merges     []string  // ranges shown as one cell, e.g. "A2:A10"
}

const maxCellText = 32000 // Excel keeps at most 32767 characters in a cell

// workbook writes the sheets as an .xlsx file.
func workbook(sheets []sheet) ([]byte, error) {
	var buf bytes.Buffer
	z := zip.NewWriter(&buf)
	parts := [][2]string{
		{"[Content_Types].xml", contentTypes(len(sheets))},
		{"_rels/.rels", `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="xl/workbook.xml"/></Relationships>`},
		{"xl/workbook.xml", workbookXML(sheets)},
		{"xl/_rels/workbook.xml.rels", workbookRels(len(sheets))},
		{"xl/styles.xml", stylesXML},
	}
	for i, sh := range sheets {
		parts = append(parts, [2]string{fmt.Sprintf("xl/worksheets/sheet%d.xml", i+1), sheetXML(sh)})
	}

	for _, part := range parts {
		if err := put(z, part[0], part[1]); err != nil {
			return nil, err
		}
	}

	if err := z.Close(); err != nil {
		return nil, fmt.Errorf("close the workbook: %w", err)
	}

	return buf.Bytes(), nil
}

func put(z *zip.Writer, name, body string) error {
	w, err := z.Create(name)
	if err != nil {
		return fmt.Errorf("add %s: %w", name, err)
	}

	if _, err := w.Write([]byte(body)); err != nil {
		return fmt.Errorf("write %s: %w", name, err)
	}

	return nil
}

func contentTypes(n int) string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">`)
	b.WriteString(`<Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Default Extension="xml" ContentType="application/xml"/>`)
	b.WriteString(`<Override PartName="/xl/workbook.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.sheet.main+xml"/>`)
	b.WriteString(`<Override PartName="/xl/styles.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.styles+xml"/>`)
	for i := range n {
		fmt.Fprintf(&b, `<Override PartName="/xl/worksheets/sheet%d.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.worksheet+xml"/>`, i+1)
	}

	b.WriteString(`</Types>`)
	return b.String()
}

func workbookXML(sheets []sheet) string {
	var b, names strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?><workbook xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"><sheets>`)
	for i, sh := range sheets {
		fmt.Fprintf(&b, `<sheet name="%s" sheetId="%d" r:id="rId%d"/>`, esc(sh.name), i+1, i+1)
		if len(sh.rows) > 0 {
			fmt.Fprintf(&names, `<definedName name="_xlnm._FilterDatabase" localSheetId="%d" hidden="1">%s</definedName>`, i, esc(fmt.Sprintf("'%s'!$A$1:$%s$%d", sh.name, colName(len(sh.rows[0])-1), len(sh.rows))))
		}
	}

	b.WriteString(`</sheets>`)
	if names.Len() > 0 {
		b.WriteString(`<definedNames>` + names.String() + `</definedNames>`)
	}

	b.WriteString(`</workbook>`)
	return b.String()
}

func workbookRels(n int) string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">`)
	for i := range n {
		fmt.Fprintf(&b, `<Relationship Id="rId%d" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/worksheet" Target="worksheets/sheet%d.xml"/>`, i+1, i+1)
	}

	fmt.Fprintf(&b, `<Relationship Id="rId%d" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/styles" Target="styles.xml"/></Relationships>`, n+1)
	return b.String()
}

func sheetXML(sh sheet) string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?><worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships">`)
	if sh.freezeCols > 0 {
		fmt.Fprintf(&b, `<sheetViews><sheetView workbookViewId="0"><pane xSplit="%d" ySplit="1" topLeftCell="%s2" activePane="bottomRight" state="frozen"/></sheetView></sheetViews>`, sh.freezeCols, colName(sh.freezeCols))
	} else {
		b.WriteString(`<sheetViews><sheetView workbookViewId="0"><pane ySplit="1" topLeftCell="A2" activePane="bottomLeft" state="frozen"/></sheetView></sheetViews>`)
	}

	if len(sh.widths) > 0 {
		b.WriteString(`<cols>`)
		for i, w := range sh.widths {
			fmt.Fprintf(&b, `<col min="%d" max="%d" width="%g" customWidth="1"/>`, i+1, i+1, w)
		}
		b.WriteString(`</cols>`)
	}

	b.WriteString(`<sheetData>`)
	for r, row := range sh.rows {
		fmt.Fprintf(&b, `<row r="%d">`, r+1)
		for c, v := range row {
			writeCell(&b, fmt.Sprintf("%s%d", colName(c), r+1), v)
		}
		b.WriteString(`</row>`)
	}

	b.WriteString(`</sheetData>`)
	if len(sh.rows) > 0 {
		fmt.Fprintf(&b, `<autoFilter ref="A1:%s%d"/>`, colName(len(sh.rows[0])-1), len(sh.rows))
	}

	if len(sh.merges) > 0 { // after the filter: the schema fixes the order
		fmt.Fprintf(&b, `<mergeCells count="%d">`, len(sh.merges))
		for _, m := range sh.merges {
			fmt.Fprintf(&b, `<mergeCell ref="%s"/>`, m)
		}
		b.WriteString(`</mergeCells>`)
	}

	b.WriteString(`</worksheet>`)
	return b.String()
}

func writeCell(b *strings.Builder, ref string, v cell) {
	s := v.style
	if v.top {
		s = max(s, styleText) + styles
	}

	style := ""
	if s != 0 {
		style = fmt.Sprintf(` s="%d"`, s)
	}

	switch {
	case v.num != nil:
		fmt.Fprintf(b, `<c r="%s"%s><v>%s</v></c>`, ref, style, strconv.FormatFloat(*v.num, 'f', -1, 64))
	case v.link != "":
		formula := fmt.Sprintf(`HYPERLINK("%s","%s")`, strings.ReplaceAll(v.link, `"`, `""`), strings.ReplaceAll(v.text, `"`, `""`))
		fmt.Fprintf(b, `<c r="%s"%s t="str"><f>%s</f><v>%s</v></c>`, ref, style, esc(formula), esc(v.text))
	case v.text != "":
		text := v.text
		if len([]rune(text)) > maxCellText {
			text = string([]rune(text)[:maxCellText]) + "…"
		}
		fmt.Fprintf(b, `<c r="%s"%s t="inlineStr"><is><t xml:space="preserve">%s</t></is></c>`, ref, style, esc(text))
	default:
		fmt.Fprintf(b, `<c r="%s"%s/>`, ref, style)
	}
}

// esc escapes text for XML; characters XML does not allow become U+FFFD.
func esc(s string) string {
	var b strings.Builder
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}

// colName is the letter name of a column from 0: A, B, ..., Z, AA.
func colName(i int) string {
	name := ""
	for i++; i > 0; i = (i - 1) / 26 {
		name = string(rune('A'+(i-1)%26)) + name
	}

	return name
}

// cellFormats are the styles in the order of cellStyle, without their border: each is written with the thin border
// and then, as its variant, with the thick line above.
var cellFormats = []string{
	`numFmtId="0" fontId="0" fillId="0" applyAlignment="1"><alignment vertical="top" wrapText="1"/>`,
	`numFmtId="0" fontId="1" fillId="2" applyFont="1" applyFill="1" applyAlignment="1"><alignment vertical="center" wrapText="1"/>`,
	`numFmtId="164" fontId="0" fillId="0" applyNumberFormat="1" applyAlignment="1"><alignment horizontal="center" vertical="top"/>`,
	`numFmtId="0" fontId="2" fillId="0" applyFont="1" applyAlignment="1"><alignment vertical="top"/>`,
	`numFmtId="0" fontId="0" fillId="3" applyFill="1" applyAlignment="1"><alignment horizontal="center" vertical="top" wrapText="1"/>`,
	`numFmtId="0" fontId="0" fillId="4" applyFill="1" applyAlignment="1"><alignment horizontal="center" vertical="top" wrapText="1"/>`,
	`numFmtId="0" fontId="0" fillId="5" applyFill="1" applyAlignment="1"><alignment horizontal="center" vertical="top" wrapText="1"/>`,
}

var stylesXML = func() string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?><styleSheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main">`)
	b.WriteString(`<numFmts count="1"><numFmt numFmtId="164" formatCode="0.##"/></numFmts>`)
	b.WriteString(`<fonts count="3"><font><sz val="11"/><name val="Calibri"/></font><font><b/><sz val="11"/><name val="Calibri"/></font><font><u/><sz val="11"/><color rgb="FF0563C1"/><name val="Calibri"/></font></fonts>`)
	b.WriteString(`<fills count="6"><fill><patternFill patternType="none"/></fill><fill><patternFill patternType="gray125"/></fill>`)
	for _, c := range []string{"FFEEF1F5", "FFDDF2DD", "FFFFF2CC", "FFF8DADA"} { // header, met, partial, not met
		fmt.Fprintf(&b, `<fill><patternFill patternType="solid"><fgColor rgb="%s"/></patternFill></fill>`, c)
	}

	thin := `style="thin"><color rgb="FFC9CED6"/>`
	fmt.Fprintf(&b, `</fills><borders count="3"><border><left/><right/><top/><bottom/><diagonal/></border>`+
		`<border><left %[1]s</left><right %[1]s</right><top %[1]s</top><bottom %[1]s</bottom><diagonal/></border>`+
		`<border><left %[1]s</left><right %[1]s</right><top style="medium"><color rgb="FF5A6270"/></top><bottom %[1]s</bottom><diagonal/></border></borders>`, thin)
	b.WriteString(`<cellStyleXfs count="1"><xf numFmtId="0" fontId="0" fillId="0" borderId="0"/></cellStyleXfs>`)
	fmt.Fprintf(&b, `<cellXfs count="%d"><xf numFmtId="0" fontId="0" fillId="0" borderId="0" xfId="0"/>`, 1+2*len(cellFormats))
	for _, border := range []int{1, 2} {
		for _, f := range cellFormats {
			fmt.Fprintf(&b, `<xf borderId="%d" xfId="0" applyBorder="1" %s</xf>`, border, f)
		}
	}

	b.WriteString(`</cellXfs><cellStyles count="1"><cellStyle name="Normal" xfId="0" builtinId="0"/></cellStyles></styleSheet>`)
	return b.String()
}()
