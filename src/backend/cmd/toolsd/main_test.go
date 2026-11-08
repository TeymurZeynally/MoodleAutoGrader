package main

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"moodle-autograder/internal/adapters/tools"
)

// The client of the file viewer against these handlers, with what students really upload: Russian names, an archive in
// an archive, a Word document.
func TestTheViewersClientUnderstandsTheAnswers(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /archive/list", archiveList)
	mux.HandleFunc("POST /archive/open", archiveOpen)
	mux.HandleFunc("POST /docx/text", docxText)
	srv := httptest.NewServer(mux)
	defer srv.Close()
	a := tools.Archives{Client: tools.New(srv.URL)}
	inner := zipOf(t, map[string]string{"Отчёт.txt": "текст"})
	z := zipOf(t, map[string]string{"Лабораторная 1/Программа.cs": "class App {}", "Лабораторная 1/вложенный.zip": string(inner)})
	entries, err := a.List(t.Context(), "работа.zip", z, nil, []string{".cs"}, nil)
	if err != nil || len(entries) != 2 {
		t.Fatalf("%+v %v", entries, err)
	}

	kinds := map[string]string{}
	for _, e := range entries {
		kinds[e.Path] = e.Kind
	}

	if kinds["Лабораторная 1/Программа.cs"] != "code" || kinds["Лабораторная 1/вложенный.zip"] != "archive" {
		t.Fatalf("%+v", entries)
	}

	entries, err = a.List(t.Context(), "работа.zip", z, []string{"Лабораторная 1/вложенный.zip"}, nil, nil)
	if err != nil || len(entries) != 1 || entries[0].Path != "Отчёт.txt" {
		t.Fatalf("%+v %v", entries, err)
	}

	data, name, err := a.Open(t.Context(), "работа.zip", z, []string{"Лабораторная 1/вложенный.zip", "Отчёт.txt"})
	if err != nil || string(data) != "текст" || !strings.HasSuffix(name, "Отчёт.txt") {
		t.Fatalf("%q %q %v", data, name, err)
	}

	if _, _, err := a.Open(t.Context(), "работа.zip", z, []string{"нет такого файла"}); err == nil {
		t.Fatal("a missing file was opened")
	}

	if _, err := a.List(t.Context(), "работа.zip", []byte("not an archive"), nil, nil, nil); err == nil {
		t.Fatal("a broken archive was listed")
	}

	docx := zipOf(t, map[string]string{"word/document.xml": `<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body><w:p><w:r><w:t>Текст отчёта</w:t></w:r></w:p></w:body></w:document>`})
	if got := a.DocxText(t.Context(), docx); !strings.Contains(got, "Текст отчёта") {
		t.Fatalf("%q", got)
	}
}

func TestTheUploadItselfDoesNotGoToTools(t *testing.T) {
	a := tools.Archives{Client: tools.New("http://tools.invalid")}
	data, name, err := a.Open(t.Context(), "отчёт.pdf", []byte("%PDF"), nil)
	if err != nil || string(data) != "%PDF" || name != "отчёт.pdf" {
		t.Fatalf("%q %q %v", data, name, err)
	}
}

func zipOf(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var b bytes.Buffer
	w := zip.NewWriter(&b)
	for name, body := range files {
		f, err := w.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}

	if err := w.Close(); err != nil {
		t.Fatal(err)
	}

	return b.Bytes()
}

func TestTheViewerReadsArchivesHere(t *testing.T) {
	z := zipOf(t, map[string]string{"src/App.cs": "class App {}", "bin/App.dll": "MZ"})
	q := url.Values{"name": {"lab.zip"}, "code": {".cs"}, "ignored": {"bin"}}
	rec := httptest.NewRecorder()
	archiveList(rec, httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/archive/list?"+q.Encode(), bytes.NewReader(z)))
	var list struct {
		Entries []struct{ Path, Kind, Note string } `json:"entries"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil || rec.Code != http.StatusOK {
		t.Fatalf("%d %s %v", rec.Code, rec.Body, err)
	}

	kinds := map[string]string{}
	for _, e := range list.Entries {
		kinds[e.Path] = e.Kind
	}

	if kinds["src/App.cs"] != "code" || len(list.Entries) != 2 {
		t.Fatalf("%+v", list.Entries)
	}

	q = url.Values{"name": {"lab.zip"}, "p": {"src/App.cs"}}
	rec = httptest.NewRecorder()
	archiveOpen(rec, httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/archive/open?"+q.Encode(), bytes.NewReader(z)))
	if rec.Code != http.StatusOK || rec.Body.String() != "class App {}" || rec.Header().Get("X-File-Name") != url.QueryEscape("src/App.cs") {
		t.Fatalf("%d %q %v", rec.Code, rec.Body, rec.Header())
	}
}

func TestABrokenArchiveIsAnErrorForTheTeacher(t *testing.T) {
	rec := httptest.NewRecorder()
	archiveList(rec, httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/archive/list?name=lab.zip", strings.NewReader("not an archive")))
	if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), `"error"`) {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
}

func TestDocxTextOfABrokenFileIsEmpty(t *testing.T) {
	rec := httptest.NewRecorder()
	docxText(rec, httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/docx/text", strings.NewReader("not a document")))
	var out struct{ Text string }
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || rec.Code != http.StatusOK || out.Text != "" {
		t.Fatalf("%d %s %v", rec.Code, rec.Body, err)
	}
}
