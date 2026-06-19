// Package client is the thin HTTP layer for the golem control-plane v1 API.
//
// All privilege lives in the API; this package just shapes requests, attaches
// the Bearer key, and decodes the {"error": "..."} convention into a Go error.
// Every call hits {GOLEM_API_URL}/api/v1/<route> with
// `Authorization: Bearer $GOLEM_API_KEY`.
package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// DefaultBaseURL is used when GOLEM_API_URL is unset.
const DefaultBaseURL = "https://platform.tools.deadnet.co"

// requestTimeout bounds every single API call.
const requestTimeout = 30 * time.Second

// ErrNoAPIKey is returned by New when GOLEM_API_KEY is absent. main turns this
// into a friendly, actionable stderr message and a non-zero exit — without ever
// making a network call.
var ErrNoAPIKey = fmt.Errorf(
	"no GOLEM_API_KEY in this environment — your Codespace may predate the key; " +
		"ask an admin to reissue, or restart the Codespace")

// Client is a configured golem API client.
type Client struct {
	baseURL string
	apiKey  string
	http    *http.Client
}

// New builds a Client from the environment:
//
//   - GOLEM_API_KEY  (required) — Bearer token for every call.
//   - GOLEM_API_URL  (optional) — API base, defaults to DefaultBaseURL.
//
// If GOLEM_API_KEY is unset it returns ErrNoAPIKey and makes no request, so the
// caller can print a friendly message and exit before any network I/O.
func New() (*Client, error) {
	key := strings.TrimSpace(os.Getenv("GOLEM_API_KEY"))
	if key == "" {
		return nil, ErrNoAPIKey
	}
	base := strings.TrimSpace(os.Getenv("GOLEM_API_URL"))
	if base == "" {
		base = DefaultBaseURL
	}
	base = strings.TrimRight(base, "/")
	return &Client{
		baseURL: base,
		apiKey:  key,
		http:    &http.Client{Timeout: requestTimeout},
	}, nil
}

// BaseURL returns the configured API base (no trailing slash).
func (c *Client) BaseURL() string { return c.baseURL }

// errorBody is the control-plane's uniform non-2xx envelope: {"error": "..."}.
type errorBody struct {
	Error string `json:"error"`
}

// Do performs one request against /api/v1/<path> and decodes a 2xx JSON body
// into out (out may be nil to discard the body). On a non-2xx it decodes the
// {"error"} envelope and returns it as a Go error; on a transport failure it
// returns a clear, wrapped error. `path` is everything after /api/v1 and may
// include a query string (e.g. "config?key=FOO").
func (c *Client) Do(ctx context.Context, method, path string, body any, out any) error {
	var reqBody io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("encode request body: %w", err)
		}
		reqBody = bytes.NewReader(b)
	}

	urlStr := c.baseURL + "/api/v1/" + strings.TrimLeft(path, "/")
	req, err := http.NewRequestWithContext(ctx, method, urlStr, reqBody)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("request to %s failed: %w", urlStr, err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read response from %s: %w", urlStr, err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var eb errorBody
		if json.Unmarshal(raw, &eb) == nil && eb.Error != "" {
			return fmt.Errorf("%s (HTTP %d)", eb.Error, resp.StatusCode)
		}
		msg := strings.TrimSpace(string(raw))
		if msg == "" {
			msg = http.StatusText(resp.StatusCode)
		}
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, msg)
	}

	if out != nil && len(raw) > 0 {
		if err := json.Unmarshal(raw, out); err != nil {
			return fmt.Errorf("decode response from %s: %w", urlStr, err)
		}
	}
	return nil
}

// Query escapes a single query value for use in a path's query string.
func Query(v string) string { return url.QueryEscape(v) }
