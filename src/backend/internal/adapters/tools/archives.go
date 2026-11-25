package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"

	"moodle-autograder/internal/adapters/extract"
	"moodle-autograder/internal/app/viewing"
)

// Archives is the file viewer's side of toolsd (viewing.Archives). Archives and Word documents of students are read in
// its container: the process that serves the web UI holds the session key and must not parse them. What is asked of the
// bytes here only looks at their first bytes.
type Archives struct{ Client *Client }

var _ viewing.Archives = Archives{}

func (Archives) KindByName(name string) string { return extract.KindByName(name) }
func (Archives) Detect(name string, data []byte) (kind, ctype string) {
	return extract.Detect(name, data)
}
func (Archives) MaxViewBytes() int { return extract.MaxViewBytes }

// post sends a file to toolsd and returns the answer; an answer other than 200 is an error for the teacher.
func (c *Client) post(ctx context.Context, path string, q url.Values, data []byte) ([]byte, http.Header, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url+path+"?"+q.Encode(), bytes.NewReader(data))
	if err != nil {
		return nil, nil, fmt.Errorf("tools request: %w", err)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, nil, fmt.Errorf("tools: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 200<<20))
	if err != nil {
		return nil, nil, fmt.Errorf("read tools response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		var out struct{ Error string }
		if json.Unmarshal(raw, &out) != nil || out.Error == "" {
			out.Error = "the file could not be read, status " + strconv.Itoa(resp.StatusCode)
		}
		return nil, nil, errors.New(out.Error)
	}

	return raw, resp.Header, nil
}

// List returns the entries of the archive at the end of inner.
func (a Archives) List(ctx context.Context, name string, data []byte, inner, codeExtensions, ignoredDirs []string) ([]viewing.Entry, error) {
	raw, _, err := a.Client.post(ctx, "/archive/list", url.Values{"name": {name}, "p": inner, "code": codeExtensions, "ignored": ignoredDirs}, data)
	if err != nil {
		return nil, err
	}

	var out struct {
		Entries []viewing.Entry `json:"entries"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("decode tools response: %w", err)
	}

	return out.Entries, nil
}

// Open returns the file at the end of inner, with its name.
func (a Archives) Open(ctx context.Context, name string, data []byte, inner []string) ([]byte, string, error) {
	if len(inner) == 0 {
		return data, name, nil // the upload itself: nothing to read in it
	}

	raw, head, err := a.Client.post(ctx, "/archive/open", url.Values{"name": {name}, "p": inner}, data)
	if err != nil {
		return nil, "", err
	}

	opened, err := url.QueryUnescape(head.Get("X-File-Name"))
	if err != nil {
		return nil, "", fmt.Errorf("decode file name: %w", err)
	}

	return raw, opened, nil
}

// DocPDF returns a Word document (DOCX or DOC) laid out as a PDF.
func (c *Client) DocPDF(ctx context.Context, data []byte) ([]byte, error) {
	pdf, _, err := c.post(ctx, "/doc/pdf", nil, data)
	return pdf, err
}

// DocPDF returns a Word document laid out as a PDF, for the viewer.
func (a Archives) DocPDF(ctx context.Context, data []byte) ([]byte, error) {
	return a.Client.DocPDF(ctx, data)
}

// DocxText returns the text of a Word document; empty when it cannot be read.
func (a Archives) DocxText(ctx context.Context, data []byte) string {
	raw, _, err := a.Client.post(ctx, "/docx/text", nil, data)
	if err != nil {
		return ""
	}

	var out struct{ Text string }
	if json.Unmarshal(raw, &out) != nil {
		return ""
	}

	return out.Text
}
