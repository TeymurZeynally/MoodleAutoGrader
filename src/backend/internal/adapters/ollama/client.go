// Package ollama calls local models served by Ollama on an internal network.
package ollama

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"
)

const (
	defaultContext = 32768 // tokens of input + reasoning + answer; Ollama silently drops the start of a longer input
	maxOutput      = 8192  // tokens per reply of a model that answers at once: a long transcript fits, a runaway generation stops
	maxReasoning   = 32768 // models that think reason before answering; this bounds reasoning plus answer
)

// seeds give repeatable answers: a reply that loops until the token limit is asked again once with another seed, since
// loops depend on sampling.
var seeds = []int{42, 7}

// errLoop marks a reply that ran into the token limit or repeated itself: worth one more try.
var errLoop = errors.New("the model reply looped")

// ErrContextFull means the input did not fit into the model context and would lose its instructions; send less.
var ErrContextFull = errors.New("did not fit into the model context")

// Options are Ollama's own generation options (num_ctx, temperature, top_p, ...) per model, set by the installation for
// its hardware; "*" applies to every model. What they leave out comes from the model itself.
type Options map[string]map[string]any

type Client struct {
	url     string
	http    *http.Client
	options Options
	mu      sync.Mutex
	caps    map[string]bool // model -> supports thinking
}

func New(url string, options Options) *Client {
	return &Client{url: strings.TrimRight(url, "/"), http: &http.Client{Timeout: 20 * time.Minute}, options: options, caps: map[string]bool{}}
}

// generation returns what a call to a model sends as options, with the size of its context and the limit of a reply: a
// quarter of the context at most, so that the input has room, unless the installation sets its own limit.
func (c *Client) generation(model string, thinking bool, seed int) (opts map[string]any, contextTokens, limit int) {
	opts = map[string]any{}
	maps.Copy(opts, c.options["*"])
	maps.Copy(opts, c.options[model])
	contextTokens = whole(opts["num_ctx"], defaultContext)
	limit = maxOutput
	if thinking {
		limit = maxReasoning
	}

	limit = whole(opts["num_predict"], min(limit, contextTokens/4))
	opts["num_ctx"], opts["num_predict"], opts["seed"] = contextTokens, limit, seed
	return opts, contextTokens, limit
}

// whole reads a number from options decoded from JSON.
func whole(v any, def int) int {
	if n, ok := v.(float64); ok {
		return int(n)
	}

	return def
}

type Message struct {
	Role    string   `json:"role"` // system, user
	Content string   `json:"content"`
	Images  [][]byte `json:"images,omitempty"` // encoded as base64 by encoding/json
}

type Usage struct{ PromptTokens, OutputTokens int }

// JSON sends a chat to model and decodes the reply, constrained by the JSON schema, into out. Thinking models reason
// first (slower, more careful); instruct models answer at once.
func (c *Client) JSON(ctx context.Context, model string, msgs []Message, schema, out any) (Usage, error) {
	thinking, err := c.thinking(ctx, model)
	if err != nil {
		return Usage{}, err
	}

	var u Usage
	for _, seed := range seeds {
		if u, err = c.chat(ctx, model, thinking, seed, msgs, schema, out); !errors.Is(err, errLoop) {
			return u, err
		}
	}

	return u, err
}

func (c *Client) chat(ctx context.Context, model string, thinking bool, seed int, msgs []Message, schema, out any) (Usage, error) {
	opts, contextTokens, limit := c.generation(model, thinking, seed)
	req := map[string]any{"model": model, "messages": msgs, "stream": false, "format": schema, "think": thinking, "keep_alive": "30m", "options": opts}
	body, err := json.Marshal(req)
	if err != nil {
		return Usage{}, fmt.Errorf("encode ollama request: %w", err)
	}

	hr, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url+"/api/chat", bytes.NewReader(body))
	if err != nil {
		return Usage{}, fmt.Errorf("ollama request: %w", err)
	}

	hr.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(hr)
	if err != nil {
		return Usage{}, fmt.Errorf("ollama: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return Usage{}, fmt.Errorf("read ollama response: %w", err)
	}

	var r struct {
		Message struct {
			Content, Thinking string
		} `json:"message"`
		Error           string `json:"error"`
		DoneReason      string `json:"done_reason"`
		PromptEvalCount int    `json:"prompt_eval_count"`
		EvalCount       int    `json:"eval_count"`
	}
	if err := json.Unmarshal(raw, &r); err != nil {
		return Usage{}, fmt.Errorf("decode ollama response (%d): %w", resp.StatusCode, err)
	}

	if strings.Contains(r.Error, "repeat limit") {
		return Usage{}, fmt.Errorf("ollama %d: %s: %w", resp.StatusCode, r.Error, errLoop)
	}

	if resp.StatusCode != http.StatusOK || r.Error != "" {
		return Usage{}, fmt.Errorf("ollama %d: %s", resp.StatusCode, r.Error)
	}

	u := Usage{r.PromptEvalCount, r.EvalCount}
	if r.PromptEvalCount >= contextTokens-limit-16 {
		return u, fmt.Errorf("the input (%d tokens): %w", r.PromptEvalCount, ErrContextFull)
	}

	switch {
	case r.Message.Content == "" && r.Message.Thinking != "":
		return u, fmt.Errorf("model %s reasoned for %d tokens without answering: %w", model, r.EvalCount, errLoop)
	case r.DoneReason == "length":
		return u, fmt.Errorf("the model reply was cut off after %d tokens: %w", r.EvalCount, errLoop)
	}

	if err := json.Unmarshal([]byte(r.Message.Content), out); err != nil {
		return u, fmt.Errorf("model reply is not the expected JSON: %w", err)
	}

	return u, nil
}

// thinking reports whether a model reasons before answering (Ollama "thinking" capability); cached per model.
func (c *Client) thinking(ctx context.Context, model string) (bool, error) {
	c.mu.Lock()
	v, ok := c.caps[model]
	c.mu.Unlock()
	if ok {
		return v, nil
	}

	body, _ := json.Marshal(map[string]string{"model": model})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url+"/api/show", bytes.NewReader(body))
	if err != nil {
		return false, fmt.Errorf("ollama show: %w", err)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return false, fmt.Errorf("ollama show: %w", err)
	}
	defer resp.Body.Close()
	var info struct {
		Capabilities []string `json:"capabilities"`
		Error        string   `json:"error"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&info); err != nil || resp.StatusCode != http.StatusOK {
		return false, fmt.Errorf("model %s is not available in Ollama (download it first): %s", model, info.Error)
	}

	v = slices.Contains(info.Capabilities, "thinking")
	c.mu.Lock()
	c.caps[model] = v
	c.mu.Unlock()
	return v, nil
}

// Models lists the models downloaded into Ollama.
func (c *Client) Models(ctx context.Context) ([]string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.url+"/api/tags", nil)
	if err != nil {
		return nil, fmt.Errorf("ollama tags: %w", err)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("ollama tags: %w", err)
	}
	defer resp.Body.Close()
	var tags struct {
		Models []struct{ Name string } `json:"models"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&tags); err != nil {
		return nil, fmt.Errorf("decode ollama tags: %w", err)
	}

	out := []string{}
	for _, m := range tags.Models {
		out = append(out, m.Name)
	}

	return out, nil
}
