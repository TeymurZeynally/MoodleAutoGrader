package extract

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/color"
	"image/png"
	"slices"
	"strings"
	"testing"

	"moodle-autograder/internal/domain/work"
)

const wordNS = `xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"`

func docx(t *testing.T, body, styles string) []byte {
	t.Helper()
	files := map[string]string{"word/document.xml": `<?xml version="1.0"?><w:document ` + wordNS + `><w:body>` + body + `</w:body></w:document>`}
	if styles != "" {
		files["word/styles.xml"] = `<?xml version="1.0"?><w:styles ` + wordNS + `>` + styles + `</w:styles>`
	}

	return zipOf(t, files)
}

func para(props, text string) string {
	return `<w:p><w:r>` + props + `<w:t xml:space="preserve">` + text + `</w:t></w:r></w:p>`
}

func TestWordTextAPersonDoesNotSeeIsLeftOutAndFlagged(t *testing.T) {
	cell := func(fill, props, text string) string {
		return `<w:tbl><w:tr><w:tc><w:tcPr><w:shd w:val="clear" w:fill="` + fill + `"/></w:tcPr>` + para(props, text) + `</w:tc></w:tr></w:tbl>`
	}
	for name, c := range map[string]struct {
		body, styles string
		hidden       bool
	}{
		"plain text":                    {para("", "Visible words"), "", false},
		"hidden":                        {para(`<w:rPr><w:vanish/></w:rPr>`, "Secret words"), "", true},
		"white on the page":             {para(`<w:rPr><w:color w:val="FFFFFF"/></w:rPr>`, "Secret words"), "", true},
		"near white on the page":        {para(`<w:rPr><w:color w:val="FAFAFA"/></w:rPr>`, "Secret words"), "", true},
		"2pt":                           {para(`<w:rPr><w:sz w:val="2"/></w:rPr>`, "Secret words"), "", true},
		"code pasted on a dark shading": {para(`<w:rPr><w:color w:val="D4D4D4"/><w:shd w:val="clear" w:fill="1E1E1E"/></w:rPr>`, "Console.WriteLine"), "", false},
		"white on a dark highlight":     {para(`<w:rPr><w:color w:val="FFFFFF"/><w:highlight w:val="black"/></w:rPr>`, "Visible words"), "", false},
		"white in a dark table cell":    {cell("000000", `<w:rPr><w:color w:val="FFFFFF"/></w:rPr>`, "Visible words"), "", false},
		"black in a black table cell":   {cell("000000", `<w:rPr><w:color w:val="000000"/></w:rPr>`, "Secret words"), "", true},
		"hidden by a character style":   {para(`<w:rPr><w:rStyle w:val="Note"/></w:rPr>`, "Secret words"), `<w:style w:type="character" w:styleId="Note"><w:rPr><w:vanish/></w:rPr></w:style>`, true},
		"white by the document default": {para("", "Secret words"), `<w:docDefaults><w:rPrDefault><w:rPr><w:color w:val="FFFFFF"/></w:rPr></w:rPrDefault></w:docDefaults>`, true},
		"hidden switched off":           {para(`<w:rPr><w:vanish w:val="0"/></w:rPr>`, "Visible words"), "", false},
	} {
		var s work.Snapshot
		text := docxText(docx(t, para(`<w:rPr><w:color w:val="000000"/></w:rPr>`, "Report of the lab.")+c.body, c.styles), &s)
		flagged := slices.Contains(s.Flags, work.FlagHiddenText)
		if flagged != c.hidden || strings.Contains(text, "Secret") || !strings.Contains(text, "Report of the lab.") {
			t.Errorf("%s: flagged %v, text %q", name, flagged, text)
		}
	}
}

func TestPicturesAreDrawnAgainAndShrunkByAveraging(t *testing.T) {
	// Columns alternate black and white: a scaler that samples every other pixel shows one of them only, which is how
	// text is hidden for a model; averaging shows gray.
	src := image.NewGray(image.Rect(0, 0, 3200, 800))
	for y := range 800 {
		for x := range 3200 {
			src.SetGray(x, y, color.Gray{Y: uint8(255 * (x % 2))})
		}
	}

	var in bytes.Buffer
	if err := png.Encode(&in, src); err != nil {
		t.Fatal(err)
	}

	out, err := ModelImage(in.Bytes())
	if err != nil {
		t.Fatal(err)
	}

	img, err := png.Decode(bytes.NewReader(out))
	if err != nil {
		t.Fatal(err)
	}

	if b := img.Bounds(); b.Dx() != 1600 || b.Dy() != 400 {
		t.Fatalf("size %v", b)
	}

	if r, _, _, _ := img.At(800, 200).RGBA(); r>>8 < 120 || r>>8 > 135 {
		t.Fatalf("every source pixel counts: %d", r>>8)
	}
}

func TestAPictureWithTooManyPixelsIsRefusedBeforeDecoding(t *testing.T) {
	var in bytes.Buffer
	if err := png.Encode(&in, image.NewGray(image.Rect(0, 0, 1, 1))); err != nil {
		t.Fatal(err)
	}

	data := in.Bytes() // a 1×1 PNG that says 60000×60000: decoding it would take gigabytes
	binary.BigEndian.PutUint32(data[16:], 60000)
	binary.BigEndian.PutUint32(data[20:], 60000)
	binary.BigEndian.PutUint32(data[29:], crc32.ChecksumIEEE(data[12:29]))
	if _, err := ModelImage(data); err == nil || !strings.Contains(err.Error(), "too large") {
		t.Fatalf("%v", err)
	}

	if _, err := ModelImage([]byte("<svg onload=alert(1)>")); err == nil {
		t.Fatal("not a picture")
	}
}
