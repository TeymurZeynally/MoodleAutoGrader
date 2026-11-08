package extract

import (
	"archive/zip"
	"bytes"
	"compress/flate"
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"moodle-autograder/internal/domain/work"
)

func TestViewerListsAndOpensNestedArchives(t *testing.T) {
	inner := zipOf(t, map[string]string{"src/App.cs": "class App {}"})
	outer := zipOf(t, map[string]string{"report.txt": "hello", "code/inner.zip": string(inner), "../evil.cs": "x"})
	entries, err := List(context.Background(), "lab.zip", outer, nil, DefaultProfile)
	if err != nil {
		t.Fatal(err)
	}

	got := map[string]Entry{}
	for _, e := range entries {
		got[e.Path] = e
	}

	if got["code/inner.zip"].Kind != "archive" || got["report.txt"].Kind != "text" || got["../evil.cs"].Note == "" {
		t.Fatalf("entries: %+v", entries)
	}

	nested, err := List(context.Background(), "lab.zip", outer, []string{"code/inner.zip"}, DefaultProfile)
	if err != nil || len(nested) != 1 || nested[0].Path != "src/App.cs" {
		t.Fatalf("nested: %+v %v", nested, err)
	}

	body, name, err := Open(context.Background(), "lab.zip", outer, []string{"code/inner.zip", "src/App.cs"})
	if err != nil || string(body) != "class App {}" || name != "src/App.cs" {
		t.Fatalf("open: %q %q %v", body, name, err)
	}

	if _, _, err := Open(context.Background(), "lab.zip", outer, []string{"../evil.cs"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unsafe entry must not open: %v", err)
	}
}

// Zip bombs inflate 64 MB of zeros from a few kilobytes. Whether the header tells the truth or lies about the size,
// nothing larger than the view limit is returned and it fails fast.
func TestViewerStopsZipBombs(t *testing.T) {
	var deflated bytes.Buffer
	fw, _ := flate.NewWriter(&deflated, flate.BestCompression)
	_, _ = fw.Write(make([]byte, 64<<20))
	_ = fw.Close()
	for _, declared := range []uint64{64 << 20, 100} {
		var buf bytes.Buffer
		zw := zip.NewWriter(&buf)
		w, err := zw.CreateRaw(&zip.FileHeader{Name: "bomb.cs", Method: zip.Deflate, CompressedSize64: uint64(deflated.Len()), UncompressedSize64: declared}) //nolint:gosec // G115: a length is never negative
		if err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write(deflated.Bytes())
		_ = zw.Close()
		start := time.Now()
		body, _, err := Open(context.Background(), "bomb.zip", buf.Bytes(), []string{"bomb.cs"})
		if err == nil || body != nil || time.Since(start) > 5*time.Second {
			t.Fatalf("declared %d: body %d bytes, err %v, %s", declared, len(body), err, time.Since(start))
		}
		if declared > MaxViewBytes && !errors.Is(err, ErrTooLarge) {
			t.Fatalf("honest header: want ErrTooLarge, got %v", err)
		}
	}
}

func TestDetectServesActiveContentAsText(t *testing.T) {
	for name, data := range map[string]string{"x.html": "<script>alert(1)</script>", "x.svg": "<svg onload=alert(1)>", "x.cs": "class A {}"} {
		if _, ctype := Detect(name, []byte(data)); !strings.HasPrefix(ctype, "text/plain") {
			t.Fatalf("%s served as %s", name, ctype)
		}
	}

	if kind, ctype := Detect("a.pdf", []byte("%PDF-1.7")); kind != "pdf" || ctype != "application/pdf" {
		t.Fatal(kind, ctype)
	}
}

func TestOtherTextFilesAreKeptForTheSimilarityCheck(t *testing.T) {
	z := zipOf(t, map[string]string{
		"api/App.cs": "class App {}", "api/appsettings.json": `{"a": 1}`, "Dockerfile": "FROM x", "docker-compose.yml": "services:",
		"web/package-lock.json": "{}", "web/logo.svg": "<svg/>", "bin/Debug/x.json": "{}", "notes.bin": "a\x00b",
	})
	var s work.Snapshot
	Add(context.Background(), &s, "lab.zip", z, DefaultLimits, DefaultProfile, noPDF)
	var text []string
	for _, f := range s.Text {
		text = append(text, f.Path)
	}

	slices.Sort(text)
	if len(s.Code) != 1 || strings.Join(text, ",") != "Dockerfile,api/appsettings.json,docker-compose.yml" {
		t.Fatalf("code %d, text %v", len(s.Code), text)
	}
}
