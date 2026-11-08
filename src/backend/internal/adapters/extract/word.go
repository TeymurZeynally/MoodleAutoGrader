package extract

import (
	"bytes"
	"encoding/xml"
	"math"
	"strconv"
	"strings"
	"unicode"
)

// Word text as a person sees it. A run is not seen when it is hidden (w:vanish), 2pt or smaller, or of the color of what
// is behind it: its shading or highlight, the paragraph's shading, the table cell, the page. White code pasted on a dark
// shading stays: it is seen. Direct formatting, the run's and the paragraph's style and the document defaults count.

var wordSpaces = map[string]bool{"http://schemas.openxmlformats.org/wordprocessingml/2006/main": true, "http://purl.oclc.org/ooxml/wordprocessingml/main": true}

const mathSpace = "http://schemas.openxmlformats.org/officeDocument/2006/math"

// look is what decides whether a person sees a run. Empty fields are not set at this level.
type look struct {
	vanish *bool
	color  string // hex or "auto"
	size   int    // half-points
	fill   string // hex of the shading or highlight behind the text
}

func (l look) over(o look) look {
	if o.vanish != nil {
		l.vanish = o.vanish
	}

	if o.color != "" {
		l.color = o.color
	}

	if o.size != 0 {
		l.size = o.size
	}

	if o.fill != "" {
		l.fill = o.fill
	}

	return l
}

// seen reports whether a person sees text of this look on the background behind it.
func (l look) seen(background string) bool {
	if (l.vanish != nil && *l.vanish) || (l.size > 0 && l.size <= 4) {
		return false
	}

	if l.fill != "" {
		background = l.fill
	}

	text, ok1 := luminance(l.color)
	back, ok2 := luminance(background)
	return !ok1 || !ok2 || math.Abs(text-back) >= 0.15
}

// luminance of a hex color, 0 (black) to 1 (white); false for "auto" and anything else that is not a color.
func luminance(hex string) (float64, bool) {
	v, err := strconv.ParseUint(strings.TrimPrefix(hex, "#"), 16, 32)
	if len(strings.TrimPrefix(hex, "#")) != 6 || err != nil {
		return 0, false
	}

	r, g, b := float64(v>>16&0xFF), float64(v>>8&0xFF), float64(v&0xFF)
	return (0.2126*r + 0.7152*g + 0.0722*b) / 255, true
}

var highlights = map[string]string{
	"black": "000000", "blue": "0000FF", "cyan": "00FFFF", "green": "00FF00", "magenta": "FF00FF", "red": "FF0000", "yellow": "FFFF00", "white": "FFFFFF",
	"darkBlue": "000080", "darkCyan": "008080", "darkGreen": "008000", "darkMagenta": "800080", "darkRed": "800000", "darkYellow": "808000", "darkGray": "808080", "lightGray": "C0C0C0",
}

func attr(e xml.StartElement, name string) string {
	for _, a := range e.Attr {
		if a.Name.Local == name {
			return a.Value
		}
	}

	return ""
}

// property applies one child of w:rPr to l.
func property(l *look, e xml.StartElement) {
	switch e.Name.Local {
	case "vanish":
		v := attr(e, "val")
		on := v == "" || v == "1" || v == "true" || v == "on"
		l.vanish = &on
	case "color":
		l.color = attr(e, "val")
	case "sz":
		l.size, _ = strconv.Atoi(attr(e, "val"))
	case "shd":
		if f := attr(e, "fill"); f != "" && f != "auto" {
			l.fill = f
		}
	case "highlight":
		if h, ok := highlights[attr(e, "val")]; ok {
			l.fill = h
		}
	}
}

type wordStyles struct {
	defaults look
	byID     map[string]look
}

// readStyles reads the run formatting of the document defaults and of every style in word/styles.xml.
func readStyles(data []byte) wordStyles {
	st := wordStyles{byID: map[string]look{}}
	dec := xml.NewDecoder(bytes.NewReader(data))
	var stack []string
	id := ""
	for {
		tok, err := dec.Token()
		if err != nil {
			return st
		}
		switch t := tok.(type) {
		case xml.StartElement:
			parent := ""
			if len(stack) > 0 {
				parent = stack[len(stack)-1]
			}
			stack = append(stack, t.Name.Local)
			switch {
			case t.Name.Local == "style":
				id = attr(t, "styleId")
			case parent == "rPr" && len(stack) >= 3 && stack[len(stack)-3] == "rPrDefault":
				property(&st.defaults, t)
			case parent == "rPr" && id != "" && len(stack) >= 3 && stack[len(stack)-3] == "style":
				l := st.byID[id]
				property(&l, t)
				st.byID[id] = l
			}
		case xml.EndElement:
			stack = stack[:len(stack)-1]
			if t.Name.Local == "style" {
				id = ""
			}
		}
	}
}

// wordText returns the text of word/document.xml a person sees, one line per paragraph, and whether text was hidden.
func wordText(data []byte, st wordStyles) (string, bool) {
	r := wordReader{styles: st}
	dec := xml.NewDecoder(bytes.NewReader(data))
	for {
		tok, err := dec.Token()
		if err != nil {
			break
		}

		switch t := tok.(type) {
		case xml.StartElement:
			r.start(t)
		case xml.CharData:
			r.text(t)
		case xml.EndElement:
			r.stack = r.stack[:len(r.stack)-1]
			if wordSpaces[t.Name.Space] && t.Name.Local == "p" {
				r.out.WriteByte('\n')
			}
		}
	}

	return r.out.String(), r.hidden >= 3
}

// wordReader follows where in the document it is: the page, table cell, paragraph and run around the text.
type wordReader struct {
	styles     wordStyles
	stack      []xml.Name
	page, cell string // backgrounds
	para, run  look   // the paragraph's style and shading; the run's style and direct formatting
	out        strings.Builder
	hidden     int // letters a person does not see
}

func (r *wordReader) start(t xml.StartElement) {
	parent := xml.Name{}
	if len(r.stack) > 0 {
		parent = r.stack[len(r.stack)-1]
	}

	r.stack = append(r.stack, t.Name)
	if !wordSpaces[t.Name.Space] {
		return
	}

	switch name := t.Name.Local; {
	case name == "background":
		r.page = attr(t, "color")
	case name == "tc":
		r.cell = ""
	case name == "shd" && parent.Local == "tcPr":
		r.cell = attr(t, "fill")
	case name == "p":
		r.para = look{}
	case name == "pStyle" && parent.Local == "pPr":
		r.para = r.styles.byID[attr(t, "val")].over(look{fill: r.para.fill})
	case name == "shd" && parent.Local == "pPr":
		if f := attr(t, "fill"); f != "" && f != "auto" {
			r.para.fill = f
		}
	case name == "r":
		r.run = look{}
	case name == "rStyle" && parent.Local == "rPr":
		r.run = r.styles.byID[attr(t, "val")].over(r.run)
	case parent.Local == "rPr" && len(r.stack) >= 3 && r.stack[len(r.stack)-3].Local == "r":
		property(&r.run, t)
	}
}

func (r *wordReader) text(t xml.CharData) {
	if len(r.stack) == 0 {
		return
	}

	if el := r.stack[len(r.stack)-1]; el.Local != "t" || (!wordSpaces[el.Space] && el.Space != mathSpace) {
		return
	}

	background := r.page
	if r.cell != "" && r.cell != "auto" {
		background = r.cell
	}

	if background == "" || background == "auto" {
		background = "FFFFFF"
	}

	if r.styles.defaults.over(r.para).over(r.run).seen(background) {
		r.out.Write(t)
	} else {
		r.hidden += letters(string(t))
	}
}

func letters(s string) int {
	n := 0
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			n++
		}
	}

	return n
}
