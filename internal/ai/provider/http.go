package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// transport posts JSON with retries and turns failures into APIErrors that
// never echo the key.
type transport struct {
	name       string
	client     *http.Client
	maxRetries int
	key        string
	// sleep is replaceable in tests.
	sleep func(context.Context, time.Duration) error
}

func newTransport(name string, cfg Config) *transport {
	c := cfg.HTTPClient
	if c == nil {
		c = defaultHTTPClient()
	}
	r := cfg.MaxRetries
	if r == 0 {
		r = 2
	}
	if r < 0 {
		r = 0
	}
	return &transport{name: name, client: c, maxRetries: r, key: cfg.APIKey, sleep: sleepCtx}
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// retryable reports whether a status is worth retrying.
func retryable(status int) bool {
	switch status {
	case http.StatusTooManyRequests, http.StatusInternalServerError, http.StatusBadGateway,
		http.StatusServiceUnavailable, http.StatusGatewayTimeout, 529:
		return true
	}
	return false
}

// postJSON sends body to url and decodes a 2xx reply into out.
func (t *transport) postJSON(ctx context.Context, url string, headers map[string]string, body, out any) error {
	payload, err := json.Marshal(body)
	if err != nil {
		return err
	}
	var last error
	for attempt := 0; ; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
		if err != nil {
			return &APIError{Provider: t.name, Message: t.scrub(err.Error())}
		}
		req.Header.Set("Content-Type", "application/json")
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		resp, err := t.client.Do(req)
		var wait time.Duration
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			last = &APIError{Provider: t.name, Message: t.scrub(err.Error())}
		} else {
			b, rerr := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
			resp.Body.Close()
			if rerr != nil {
				last = &APIError{Provider: t.name, Status: resp.StatusCode, Message: t.scrub(rerr.Error())}
			} else if resp.StatusCode >= 200 && resp.StatusCode < 300 {
				if err := json.Unmarshal(b, out); err != nil {
					return &APIError{Provider: t.name, Status: resp.StatusCode, Message: "unreadable reply: " + t.scrub(err.Error())}
				}
				return nil
			} else {
				e := &APIError{Provider: t.name, Status: resp.StatusCode, Message: t.scrub(errorMessage(b))}
				if !retryable(resp.StatusCode) {
					return e
				}
				last = e
				wait = retryAfter(resp.Header.Get("Retry-After"))
			}
		}
		if attempt >= t.maxRetries {
			return last
		}
		if wait == 0 {
			wait = time.Duration(1<<attempt) * time.Second
		}
		if err := t.sleep(ctx, wait); err != nil {
			return err
		}
	}
}

func retryAfter(h string) time.Duration {
	if h == "" {
		return 0
	}
	if s, err := strconv.Atoi(strings.TrimSpace(h)); err == nil && s >= 0 {
		return min(time.Duration(s)*time.Second, 30*time.Second)
	}
	return 0
}

// scrub removes the API key from text, in case a server or proxy echoes it.
func (t *transport) scrub(s string) string {
	if t.key != "" && len(t.key) >= 4 {
		s = strings.ReplaceAll(s, t.key, "[REDACTED]")
	}
	return s
}

// errorMessage extracts a human-readable message from an error body.
func errorMessage(b []byte) string {
	var v struct {
		Error   json.RawMessage `json:"error"`
		Message string          `json:"message"`
	}
	if json.Unmarshal(b, &v) == nil {
		if len(v.Error) > 0 {
			var s string
			if json.Unmarshal(v.Error, &s) == nil && s != "" {
				return s
			}
			var o struct {
				Message string `json:"message"`
				Type    string `json:"type"`
				Status  string `json:"status"`
			}
			if json.Unmarshal(v.Error, &o) == nil && o.Message != "" {
				return o.Message
			}
		}
		if v.Message != "" {
			return v.Message
		}
	}
	s := strings.TrimSpace(string(b))
	if len(s) > 300 {
		s = s[:300] + "…"
	}
	if s == "" {
		s = "empty error response"
	}
	return s
}

// extractJSON returns the first complete JSON object in text, tolerating
// markdown fences and surrounding prose.
func extractJSON(text string) (string, error) {
	s := strings.TrimSpace(text)
	if json.Valid([]byte(s)) {
		return s, nil
	}
	start := strings.IndexByte(s, '{')
	if start < 0 {
		return "", errors.New("the reply contains no JSON object")
	}
	depth, inStr, esc := 0, false, false
	for i := start; i < len(s); i++ {
		c := s[i]
		switch {
		case esc:
			esc = false
		case inStr && c == '\\':
			esc = true
		case c == '"':
			inStr = !inStr
		case inStr:
		case c == '{':
			depth++
		case c == '}':
			depth--
			if depth == 0 {
				cand := s[start : i+1]
				if json.Valid([]byte(cand)) {
					return cand, nil
				}
				return "", fmt.Errorf("the reply's JSON object is malformed")
			}
		}
	}
	return "", errors.New("the reply's JSON object is incomplete")
}

// ErrNoJSON is returned when a reply that should be JSON is not. The
// returned Response still carries the text and usage.
var ErrNoJSON = errors.New("the reply is not a valid JSON object")

// ExtractJSON is exported for callers that receive free text.
func ExtractJSON(text string) (string, error) { return extractJSON(text) }

// finishJSON post-processes a reply that should be JSON.
func finishJSON(req *Request, resp *Response) (*Response, error) {
	if len(req.JSONSchema) == 0 {
		return resp, nil
	}
	if resp.Truncated {
		return resp, ErrTruncated
	}
	j, err := extractJSON(resp.Text)
	if err != nil {
		return resp, fmt.Errorf("%w: %w", ErrNoJSON, err)
	}
	resp.Text = j
	return resp, nil
}
