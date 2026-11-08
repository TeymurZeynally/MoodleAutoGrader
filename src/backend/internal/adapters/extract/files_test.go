package extract

import (
	"archive/zip"
	"bytes"
	"context"
	"sort"
	"testing"
)

var pdfBytes = []byte("%PDF-1.4 a report")

// noPDF stands for the tool that reads PDFs.
func noPDF(context.Context, []byte) (string, bool, error) {
	return "Report text with a hidden\u200bcharacter.", false, nil
}

// zipOf packs files (path -> content) into a zip archive with stable ordering.
func zipOf(t *testing.T, files map[string]string) []byte {
	t.Helper()
	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}

	sort.Strings(names)
	var b bytes.Buffer
	w := zip.NewWriter(&b)
	for _, n := range names {
		f, err := w.Create(n)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.Write([]byte(files[n])); err != nil {
			t.Fatal(err)
		}
	}

	if err := w.Close(); err != nil {
		t.Fatal(err)
	}

	return b.Bytes()
}
