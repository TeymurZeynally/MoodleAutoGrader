// Package moodle is the gateway to Moodle Web Services (Gateway + Anti-Corruption Layer).
// Each process role gets a client with its own function allowlist.
package moodle

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// maxResponseSize caps a Moodle response: submission lists can be large, but not unbounded.
const maxResponseSize = 32 << 20

// ErrFunctionNotAllowed means the function is not in the role's allowlist; no request is sent to Moodle.
var ErrFunctionNotAllowed = errors.New("moodle function not allowed for this role")

// Error is an error Moodle returned in the body of an HTTP 200 response.
type Error struct{ Function, Code, Message string }

func (e *Error) Error() string {
	return fmt.Sprintf("moodle %s: %s: %s", e.Function, e.Code, e.Message)
}

type Client struct {
	baseURL, token string
	allowed        map[string]bool
	http           *http.Client
}

type Option func(*Client)

// WithTransport sets how requests reach Moodle.
func WithTransport(t http.RoundTripper) Option { return func(c *Client) { c.http.Transport = t } }

func WithHTTPClient(h *http.Client) Option { return func(c *Client) { c.http = h } }

// stayOnSite refuses a redirect off the Moodle site: a 307 or 308 would send the token in the body along with it.
func stayOnSite(req *http.Request, via []*http.Request) error {
	if req.URL.Host != via[0].URL.Host || req.URL.Scheme != via[0].URL.Scheme {
		return fmt.Errorf("redirect to %s: %w", req.URL.Host, ErrForeignHost)
	}

	if len(via) >= 10 {
		return errors.New("stopped after 10 redirects")
	}

	return nil
}

func NewClient(baseURL, token string, allowed []string, opts ...Option) *Client {
	c := &Client{baseURL: strings.TrimRight(baseURL, "/"), token: token, allowed: make(map[string]bool, len(allowed)), http: &http.Client{Timeout: 60 * time.Second, CheckRedirect: stayOnSite}}
	for _, f := range allowed {
		c.allowed[f] = true
	}

	for _, opt := range opts {
		opt(c)
	}

	return c
}

// Call invokes a Web Services function and decodes the response into out. The token goes in the POST body, not the URL, so it never lands in logs.
func (c *Client) Call(ctx context.Context, function string, params url.Values, out any) error {
	if !c.allowed[function] {
		return fmt.Errorf("%s: %w", function, ErrFunctionNotAllowed)
	}

	form := url.Values{"wstoken": {c.token}, "wsfunction": {function}, "moodlewsrestformat": {"json"}}
	maps.Copy(form, params)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/webservice/rest/server.php", strings.NewReader(form.Encode()))
	if err != nil {
		return fmt.Errorf("build request %s: %w", function, err)
	}

	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("call %s: %w", function, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseSize))
	if err != nil {
		return fmt.Errorf("read %s: %w", function, err)
	}

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("call %s: http status %d", function, resp.StatusCode)
	}

	var mErr struct {
		Exception string `json:"exception"`
		ErrorCode string `json:"errorcode"`
		Message   string `json:"message"`
	}
	if json.Unmarshal(body, &mErr) == nil && mErr.Exception != "" {
		return &Error{Function: function, Code: mErr.ErrorCode, Message: mErr.Message}
	}

	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("decode %s: %w", function, err)
	}

	return nil
}
