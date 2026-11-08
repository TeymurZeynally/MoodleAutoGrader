// Package dolos calls the Dolos service (deploy/dolos): tree-sitter based source code plagiarism detection. It is the
// comparing.CodeMatcher of the similarity check.
package dolos

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"moodle-autograder/internal/app/comparing"
)

type Client struct {
	url  string
	http *http.Client
}

func New(url string) *Client {
	return &Client{url: strings.TrimRight(url, "/"), http: &http.Client{Timeout: 15 * time.Minute}}
}

type file struct {
	Name    string `json:"name"`
	Content string `json:"content"`
}

// Fragment is a matching region; rows are 0-based and inclusive.
type Fragment struct {
	Left  [2]int `json:"left"`
	Right [2]int `json:"right"`
}

type Pair struct {
	Left, Right  string
	Similarity   float64
	LeftCovered  int `json:"leftCovered"`
	LeftTotal    int `json:"leftTotal"`
	RightCovered int `json:"rightCovered"`
	RightTotal   int `json:"rightTotal"`
	Fragments    []Fragment
}

// Analyze compares every file with every other one. maxShare drops fingerprints found in more than that share of the
// files (template code); 0 keeps them all. minKgrams drops shorter matches; 0 is the service default.
func (c *Client) Analyze(ctx context.Context, language string, files []comparing.CodeFile, maxShare float64, minKgrams int) ([]comparing.CodePair, error) {
	body := make([]file, len(files))
	for i, f := range files {
		body[i] = file{Name: f.Name, Content: f.Content}
	}

	pairs, err := c.analyze(ctx, map[string]any{"language": language, "files": body}, maxShare, minKgrams)
	if err != nil {
		return nil, err
	}

	out := make([]comparing.CodePair, len(pairs))
	for i, p := range pairs {
		out[i] = comparing.CodePair{Left: p.Left, Right: p.Right, Similarity: p.Similarity}
		for _, f := range p.Fragments {
			out[i].Fragments = append(out[i].Fragments, [4]int{f.Left[0], f.Left[1], f.Right[0], f.Right[1]})
		}
	}

	return out, nil
}

func (c *Client) analyze(ctx context.Context, req map[string]any, maxShare float64, minKgrams int) ([]Pair, error) {
	if maxShare > 0 {
		req["maxFingerprintPercentage"] = maxShare
	}

	if minKgrams > 0 {
		req["minKgrams"] = minKgrams
	}

	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("encode dolos request: %w", err)
	}

	hr, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url+"/analyze", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("dolos request: %w", err)
	}

	hr.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(hr)
	if err != nil {
		return nil, fmt.Errorf("dolos: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 256<<20))
	if err != nil {
		return nil, fmt.Errorf("read dolos response: %w", err)
	}

	var out struct {
		Pairs []Pair `json:"pairs"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("decode dolos response (%d): %w", resp.StatusCode, err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("dolos %d: %s", resp.StatusCode, out.Error)
	}

	return out.Pairs, nil
}
