package main

import (
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"image"
	"image/draw"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

// Text of a PDF that a person does not see is written for a model: white on white, under a shape of its own color,
// clipped, invisible (render mode 3) on an empty place, 2pt and smaller, or off the page. The first pages are rendered
// and every line of text is checked for ink: visible text leaves pixels unlike what is around them. White code on a
// dark block keeps its ink, and so does a scanned page with its recognized text over the scan.

const (
	hiddenPages = 30 // pages checked; the AI check reads less than this
	hiddenMax   = 100
	minInk      = 0.01 // share of the line's box that must differ from its background
)

type pdfLine struct {
	page                     int
	left, top, width, height int
	size                     float64
	text                     string
}

// hiddenText returns the lines of the PDF a person does not see on its first pages.
func hiddenText(ctx context.Context, pdf string) ([]string, error) {
	pages := strconv.Itoa(hiddenPages)
	out, err := exec.CommandContext(ctx, "pdftohtml", "-xml", "-hidden", "-i", "-q", "-stdout", "-enc", "UTF-8", "-zoom", "1", "-l", pages, pdf).Output() //nolint:gosec // G204: a fixed program; pdf is a path in this request's own temporary directory
	if err != nil {
		return nil, fmt.Errorf("pdftohtml: %w", err)
	}

	root := filepath.Join(filepath.Dir(pdf), "ink")
	if err := exec.CommandContext(ctx, "pdftoppm", "-r", "72", "-gray", "-png", "-l", pages, pdf, root).Run(); err != nil { //nolint:gosec // G204: as above
		return nil, fmt.Errorf("pdftoppm: %w", err)
	}

	images := map[int]*image.Gray{}
	files, _ := filepath.Glob(root + "-*.png")
	for _, f := range files {
		n, err := strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(filepath.Base(f), "ink-"), ".png"))
		if err != nil {
			continue
		}
		if img, err := grayPNG(f); err == nil {
			images[n] = img
		}
	}

	var hidden []string
	for _, l := range pdfLines(out) {
		if len(hidden) == hiddenMax {
			break
		}
		img, ok := images[l.page]
		if !ok || letters(l.text) < 3 { // a page that did not render tells nothing
			continue
		}
		if l.size <= 2 || !inked(img, image.Rect(l.left, l.top, l.left+l.width, l.top+l.height)) {
			hidden = append(hidden, l.text)
		}
	}

	return hidden, nil
}

func grayPNG(path string) (*image.Gray, error) {
	data, err := os.ReadFile(path) //nolint:gosec // G304: a file pdftoppm wrote into this request's own temporary directory
	if err != nil {
		return nil, fmt.Errorf("read page image: %w", err)
	}

	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("decode page image: %w", err)
	}

	if g, ok := img.(*image.Gray); ok {
		return g, nil
	}

	g := image.NewGray(img.Bounds())
	draw.Draw(g, g.Bounds(), img, img.Bounds().Min, draw.Src)
	return g, nil
}

// inked reports whether the box has ink: enough pixels unlike the most common shade in it. A box off the page has none.
func inked(img *image.Gray, box image.Rectangle) bool {
	box = box.Intersect(img.Bounds())
	if box.Empty() {
		return false
	}

	var hist [256]int
	for y := box.Min.Y; y < box.Max.Y; y++ {
		for x := box.Min.X; x < box.Max.X; x++ {
			hist[img.GrayAt(x, y).Y]++
		}
	}

	common := 0
	for v := range hist {
		if hist[v] > hist[common] {
			common = v
		}
	}

	ink, total := 0, box.Dx()*box.Dy()
	for v, n := range hist {
		if v-common > 48 || common-v > 48 {
			ink += n
		}
	}

	return float64(ink) >= minInk*float64(total)
}

// pdfLines reads the lines of text with their place and font size from the XML of pdftohtml.
func pdfLines(data []byte) []pdfLine {
	dec := xml.NewDecoder(bytes.NewReader(data))
	dec.Strict = false
	sizes := map[string]float64{}
	var lines []pdfLine
	page := 0
	var cur *pdfLine
	for {
		tok, err := dec.Token()
		if err != nil {
			return lines
		}
		switch t := tok.(type) {
		case xml.StartElement:
			a := map[string]string{}
			for _, at := range t.Attr {
				a[at.Name.Local] = at.Value
			}
			switch t.Name.Local {
			case "page":
				page, _ = strconv.Atoi(a["number"])
			case "fontspec":
				sizes[a["id"]], _ = strconv.ParseFloat(a["size"], 64)
			case "text":
				l := pdfLine{page: page, size: sizes[a["font"]]}
				l.left, _ = strconv.Atoi(a["left"])
				l.top, _ = strconv.Atoi(a["top"])
				l.width, _ = strconv.Atoi(a["width"])
				l.height, _ = strconv.Atoi(a["height"])
				cur = &l
			}
		case xml.CharData:
			if cur != nil {
				cur.text += string(t)
			}
		case xml.EndElement:
			if t.Name.Local == "text" && cur != nil {
				cur.text = strings.TrimSpace(cur.text)
				lines = append(lines, *cur)
				cur = nil
			}
		}
	}
}

// withoutLines removes each hidden line from the text once, whatever the spacing between its words. pdftotext leaves
// out what runs off the page, so the text may end the line early, even inside a word: three words up to the end of a
// text line are enough then.
func withoutLines(text string, lines []string) string {
	for _, l := range lines {
		if start, end := findWords(text, strings.Fields(l)); end > start {
			text = text[:start] + text[end:]
		}
	}

	return text
}

var word = regexp.MustCompile(`\S+`)

func findWords(text string, words []string) (int, int) {
	if len(words) == 0 {
		return 0, 0
	}

	at := word.FindAllStringIndex(text, -1)
	for i := range at {
		n := 0
		for n < len(words) && i+n < len(at) {
			w := text[at[i+n][0]:at[i+n][1]]
			if w != words[n] && !strings.HasPrefix(words[n], w) {
				break
			}
			n++
			if w != words[n-1] {
				break // the text stops inside this word
			}
		}
		if n == 0 {
			continue
		}
		end := at[i+n-1][1]
		if n == len(words) || (n >= 3 && (end == len(text) || text[end] == '\n' || text[end] == '\r')) {
			return at[i][0], end
		}
	}

	return 0, 0
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
