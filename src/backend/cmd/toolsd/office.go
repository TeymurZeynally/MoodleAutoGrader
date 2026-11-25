package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"moodle-autograder/internal/adapters/extract"
)

const convertTimeout = 90 * time.Second

// oleMagic starts the older binary Word format (.doc).
var oleMagic = []byte("\xd0\xcf\x11\xe0\xa1\xb1\x1a\xe1")

// office is LibreOffice: all conversions share one profile, so they run one at a time.
var office sync.Mutex

// docPDF lays out a Word document (DOCX or the older DOC) as a PDF, as Word shows it: pictures, drawings made of
// shapes and embedded diagrams included, which the document's own parts do not give as pictures.
func docPDF(w http.ResponseWriter, r *http.Request) {
	body, ok := readBody(w, r)
	if !ok {
		return
	}

	// Only a Word document by its content: LibreOffice would lay out any bytes, a random file as pages of text.
	ext := ".docx"
	if bytes.HasPrefix(body, oleMagic) {
		ext = ".doc"
	}

	if kind, _ := extract.Detect("in"+ext, body); kind != "docx" {
		fail(w, http.StatusUnprocessableEntity, errors.New("not a Word document"))
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), convertTimeout)
	defer cancel()
	pdf, err := convertToPDF(ctx, body, ext)
	if err != nil {
		fail(w, http.StatusUnprocessableEntity, err)
		return
	}

	w.Header().Set("Content-Type", "application/pdf")
	_, _ = w.Write(pdf)
}

// convertToPDF runs LibreOffice on the document with an empty environment. Every process a run started is killed when
// it ends, or when it outlives ctx, and a run that fails takes the profile with it: the next one starts afresh. The
// helpers it leaves behind as zombies are reaped by the container's init (compose: init: true).
func convertToPDF(ctx context.Context, data []byte, ext string) ([]byte, error) {
	office.Lock()
	defer office.Unlock()
	dir, err := os.MkdirTemp("", "doc")
	if err != nil {
		return nil, fmt.Errorf("temporary directory: %w", err)
	}

	defer func() { _ = os.RemoveAll(dir) }()
	in := filepath.Join(dir, "in"+ext)
	if err := os.WriteFile(in, data, 0o600); err != nil {
		return nil, fmt.Errorf("write the document: %w", err)
	}

	profile := filepath.Join(os.TempDir(), "office-profile")
	//nolint:gosec // G204: a fixed program; the arguments are paths in this container's own temporary directory
	cmd := exec.CommandContext(ctx, "soffice", "--headless", "--norestore", "--nologo", "--nodefault", "--nolockcheck", "--nofirststartwizard",
		"-env:UserInstallation=file://"+profile, "--convert-to", "pdf", "--outdir", dir, in)
	cmd.Env = []string{"HOME=" + os.TempDir(), "PATH=/usr/bin:/bin", "LANG=C.UTF-8"}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = 5 * time.Second
	msg, runErr := cmd.CombinedOutput()
	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)       // whatever it started and left running goes with it
	pdf, readErr := os.ReadFile(filepath.Join(dir, "in.pdf")) //nolint:gosec // G304: a file LibreOffice wrote into this request's own temporary directory
	if runErr != nil || readErr != nil || !bytes.HasPrefix(pdf, []byte("%PDF")) {
		_ = os.RemoveAll(profile)
		if ctx.Err() != nil {
			return nil, fmt.Errorf("the document took longer than %s to lay out", convertTimeout)
		}
		return nil, fmt.Errorf("the document could not be laid out: %s", bytes.TrimSpace(msg))
	}

	return pdf, nil
}
