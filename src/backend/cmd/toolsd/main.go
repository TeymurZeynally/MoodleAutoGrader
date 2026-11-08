// toolsd runs document utilities (poppler) for the autograder in an isolated container: no network, no secrets,
// read-only filesystem, a fresh temp dir and a deadline per request.
//
// POST /pdf/pages?max=N&size=PX  body: a PDF
//
//	-> {"pages": [{"page": 3, "png": "<base64>"}], "total": 12}
//
// Only pages that contain images are rendered: text pages are read by the text extractor instead.
//
// POST /pdf/text  body: a PDF
//
//	-> {"text": "..."}
//
// A crafted PDF can hang or crash a parser: here it costs one request, not the application.
//
// For the file viewer, whose process holds the session key and must not read these files itself:
//
// POST /archive/list?name=N&p=INNER&code=.EXT&ignored=DIR  body: an archive  -> {"entries": [...]}
// POST /archive/open?name=N&p=INNER                        body: an archive  -> the file, its name in X-File-Name
// POST /docx/text                                          body: a document  -> {"text": "..."}
package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"moodle-autograder/internal/adapters/extract"
)

const (
	maxBody    = 60 << 20
	maxText    = 5 << 20
	maxPages   = 40
	defaultMax = 15
	timeout    = 90 * time.Second
)

type page struct {
	Page int    `json:"page"`
	PNG  []byte `json:"png"`
}

func main() {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) })
	mux.HandleFunc("POST /pdf/pages", pages)
	mux.HandleFunc("POST /pdf/text", pdfText)
	mux.HandleFunc("POST /archive/list", archiveList)
	mux.HandleFunc("POST /archive/open", archiveOpen)
	mux.HandleFunc("POST /docx/text", docxText)
	slog.Info("tools listening", "addr", ":8080")
	srv := &http.Server{Addr: ":8080", Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	if err := srv.ListenAndServe(); err != nil {
		slog.Error("tools stopped", "err", err)
		os.Exit(1)
	}
}

// readBody reads the file of a request; on failure it answers and returns false.
func readBody(w http.ResponseWriter, r *http.Request) ([]byte, bool) {
	body, err := io.ReadAll(io.LimitReader(r.Body, maxBody+1))
	if err == nil && len(body) > maxBody {
		err = fmt.Errorf("the file is larger than %d MB", maxBody>>20)
	}

	if err != nil {
		fail(w, http.StatusBadRequest, err)
		return nil, false
	}

	return body, true
}

// savePDF stores the body in a fresh temporary directory; on failure it answers and returns false.
func savePDF(w http.ResponseWriter, r *http.Request) (string, func(), bool) {
	body, ok := readBody(w, r)
	if !ok {
		return "", nil, false
	}

	dir, err := os.MkdirTemp("", "pdf")
	if err != nil {
		fail(w, http.StatusInternalServerError, err)
		return "", nil, false
	}

	in := filepath.Join(dir, "in.pdf")
	if err := os.WriteFile(in, body, 0o600); err != nil {
		_ = os.RemoveAll(dir)
		fail(w, http.StatusInternalServerError, err)
		return "", nil, false
	}

	return in, func() { _ = os.RemoveAll(dir) }, true
}

// archiveList lists an archive, or an archive inside it, for the viewer.
func archiveList(w http.ResponseWriter, r *http.Request) {
	body, ok := readBody(w, r)
	if !ok {
		return
	}

	q := r.URL.Query()
	entries, err := extract.List(r.Context(), q.Get("name"), body, q["p"], extract.NewProfile(q["code"], q["ignored"]))
	if err != nil {
		fail(w, http.StatusUnprocessableEntity, err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"entries": entries})
}

// archiveOpen returns one file from inside an archive, or the upload itself, for the viewer.
func archiveOpen(w http.ResponseWriter, r *http.Request) {
	body, ok := readBody(w, r)
	if !ok {
		return
	}

	q := r.URL.Query()
	data, name, err := extract.Open(r.Context(), q.Get("name"), body, q["p"])
	if err != nil {
		fail(w, http.StatusUnprocessableEntity, err)
		return
	}

	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("X-File-Name", url.QueryEscape(name))
	_, _ = w.Write(data) //nolint:gosec // G705: sent as octet-stream to the web process, which decides how to show it
}

// docxText returns the text of a Word document for the viewer.
func docxText(w http.ResponseWriter, r *http.Request) {
	body, ok := readBody(w, r)
	if !ok {
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"text": extract.DocxText(body)})
}

// pdfText reads the text of a PDF a person sees; what does not fit into maxText is cut. "hidden" counts the lines left
// out because a person does not see them (see hiddenText).
func pdfText(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), timeout)
	defer cancel()
	in, done, ok := savePDF(w, r)
	if !ok {
		return
	}

	defer done()
	cmd := exec.CommandContext(ctx, "pdftotext", "-enc", "UTF-8", "-nopgbrk", in, "-") //nolint:gosec // G204: a fixed program; in is a path in this request's own temporary directory
	out, err := cmd.StdoutPipe()
	if err != nil {
		fail(w, http.StatusInternalServerError, err)
		return
	}

	if err := cmd.Start(); err != nil {
		fail(w, http.StatusInternalServerError, err)
		return
	}

	text, _ := io.ReadAll(io.LimitReader(out, maxText))
	cancel() // a longer text stops here
	if err := cmd.Wait(); err != nil && len(text) == 0 {
		fail(w, http.StatusUnprocessableEntity, fmt.Errorf("not a readable pdf: %w", err))
		return
	}

	check, stop := context.WithTimeout(r.Context(), timeout)
	defer stop()
	visible := strings.ToValidUTF8(string(text), "")
	hidden, err := hiddenText(check, in)
	if err == nil {
		visible = withoutLines(visible, hidden)
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"text": visible, "hidden": len(hidden)})
}

func pages(w http.ResponseWriter, r *http.Request) {
	limit := intParam(r, "max", defaultMax, 1, maxPages)
	size := intParam(r, "size", 1024, 256, 2048)
	ctx, cancel := context.WithTimeout(r.Context(), timeout)
	defer cancel()
	in, done, ok := savePDF(w, r)
	if !ok {
		return
	}

	defer done()
	dir := filepath.Dir(in)
	withImages, total, err := imagePages(ctx, in)
	if err != nil {
		fail(w, http.StatusUnprocessableEntity, err)
		return
	}

	out := struct {
		Pages []page `json:"pages"`
		Total int    `json:"total"`
	}{Pages: []page{}, Total: total}
	for _, p := range withImages[:min(limit, len(withImages))] {
		prefix := filepath.Join(dir, fmt.Sprintf("p%d", p))
		n := strconv.Itoa(p)
		//nolint:gosec // G204: a fixed program; the arguments are numbers and paths in this request's own temporary directory
		cmd := exec.CommandContext(ctx, "pdftoppm", "-png", "-singlefile", "-f", n, "-l", n, "-scale-to", strconv.Itoa(size), in, prefix)
		if msg, err := cmd.CombinedOutput(); err != nil {
			fail(w, http.StatusUnprocessableEntity, fmt.Errorf("render page %d: %w: %s", p, err, bytes.TrimSpace(msg)))
			return
		}
		png, err := os.ReadFile(prefix + ".png") //nolint:gosec // G304: a file this request created in its own temporary directory
		if err != nil {
			fail(w, http.StatusInternalServerError, err)
			return
		}
		out.Pages = append(out.Pages, page{Page: p, PNG: png})
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

// imagePages lists pages that contain at least one image of a useful size (pdfimages -list), and the page count.
func imagePages(ctx context.Context, pdf string) ([]int, int, error) {
	info, err := exec.CommandContext(ctx, "pdfinfo", pdf).Output() //nolint:gosec // G204: a fixed program; pdf is a path in this request's own temporary directory
	if err != nil {
		return nil, 0, fmt.Errorf("not a readable pdf: %w", err)
	}

	total := 0
	for l := range strings.SplitSeq(string(info), "\n") {
		if v, ok := strings.CutPrefix(l, "Pages:"); ok {
			total, _ = strconv.Atoi(strings.TrimSpace(v))
		}
	}

	list, err := exec.CommandContext(ctx, "pdfimages", "-list", pdf).Output() //nolint:gosec // G204: a fixed program; pdf is a path in this request's own temporary directory
	if err != nil {
		return nil, total, fmt.Errorf("list images: %w", err)
	}

	seen := map[int]bool{}
	sc := bufio.NewScanner(bytes.NewReader(list))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) < 5 {
			continue
		}
		p, err1 := strconv.Atoi(f[0])
		wpx, err2 := strconv.Atoi(f[3])
		hpx, err3 := strconv.Atoi(f[4])
		if err1 == nil && err2 == nil && err3 == nil && wpx >= 120 && hpx >= 80 { // icons and bullets are not screenshots
			seen[p] = true
		}
	}

	out := make([]int, 0, len(seen))
	for p := range seen {
		out = append(out, p)
	}

	sort.Ints(out)
	return out, total, nil
}

func intParam(r *http.Request, name string, def, lo, hi int) int {
	v, err := strconv.Atoi(r.URL.Query().Get(name))
	if err != nil {
		return def
	}

	return max(lo, min(v, hi))
}

func fail(w http.ResponseWriter, code int, err error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
}
