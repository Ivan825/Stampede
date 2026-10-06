package provider

import (
	"context"
	"errors"
	"sync"
)

// Fake is a scripted provider for tests: each call returns the next reply.
// It records every request so tests can assert on what would have been
// sent to a real provider (for example, that secrets were redacted).
type Fake struct {
	// Replies are returned in order. A reply that is an error is returned
	// as one.
	Replies []any
	// PerCall is the usage reported for each call.
	PerCall Usage
	// ModelName defaults to "fake-model".
	ModelName string

	mu       sync.Mutex
	requests []Request
}

// Name is "fake".
func (f *Fake) Name() string { return "fake" }

// Model is ModelName or "fake-model".
func (f *Fake) Model() string {
	if f.ModelName == "" {
		return "fake-model"
	}
	return f.ModelName
}

// Requests returns copies of the requests seen so far.
func (f *Fake) Requests() []Request {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Request(nil), f.requests...)
}

// Chat returns the next scripted reply.
func (f *Fake) Chat(ctx context.Context, req *Request) (*Response, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	f.mu.Lock()
	n := len(f.requests)
	f.requests = append(f.requests, *req)
	f.mu.Unlock()
	if n >= len(f.Replies) {
		return nil, errors.New("fake provider: no more scripted replies")
	}
	resp := &Response{Model: f.Model(), Usage: f.PerCall, StopReason: "end_turn"}
	switch r := f.Replies[n].(type) {
	case error:
		return resp, r
	case string:
		resp.Text = r
	default:
		return resp, errors.New("fake provider: replies must be strings or errors")
	}
	return finishJSON(req, resp)
}
