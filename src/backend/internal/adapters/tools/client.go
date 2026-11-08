// Package tools calls toolsd (cmd/toolsd): document utilities in an isolated container.
package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type Client struct {
	url  string
	http *http.Client
}

func New(url string) *Client {
	return &Client{url: strings.TrimRight(url, "/"), http: &http.Client{Timeout: 3 * time.Minute}}
}

// PDFText reads the text of a PDF a person sees, and whether text a person does not see was left out.
func (c *Client) PDFText(ctx context.Context, pdf []byte) (string, bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url+"/pdf/text", bytes.NewReader(pdf))
	if err != nil {
		return "", false, fmt.Errorf("tools request: %w", err)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return "", false, fmt.Errorf("tools: %w", err)
	}
	defer resp.Body.Close()
	var out struct {
		Text, Error string
		Hidden      int
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 32<<20)).Decode(&out); err != nil {
		return "", false, fmt.Errorf("decode tools response (%d): %w", resp.StatusCode, err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", false, fmt.Errorf("tools %d: %s", resp.StatusCode, out.Error)
	}

	return out.Text, out.Hidden > 0, nil
}

type Page struct {
	Page int    `json:"page"`
	PNG  []byte `json:"png"`
}

// PDFImagePages renders the pages of a PDF that contain images (screenshots), at most limit, longest side size px.
func (c *Client) PDFImagePages(ctx context.Context, pdf []byte, limit, size int) ([]Page, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, fmt.Sprintf("%s/pdf/pages?max=%d&size=%d", c.url, limit, size), bytes.NewReader(pdf))
	if err != nil {
		return nil, 0, fmt.Errorf("tools request: %w", err)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("tools: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 200<<20))
	if err != nil {
		return nil, 0, fmt.Errorf("read tools response: %w", err)
	}

	var out struct {
		Pages []Page `json:"pages"`
		Total int    `json:"total"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, 0, fmt.Errorf("decode tools response (%d): %w", resp.StatusCode, err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, 0, fmt.Errorf("tools %d: %s", resp.StatusCode, out.Error)
	}

	return out.Pages, out.Total, nil
}
