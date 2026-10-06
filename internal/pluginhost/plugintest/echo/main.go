// Command echo is a plugin for Stampede's own tests. Its steps return what
// they are given and can be told to fail, sleep, panic or crash.
package main

import (
	"context"
	"errors"
	"os"
	"sync/atomic"
	"time"

	"github.com/Ivan825/Stampede/pkg/pluginsdk"
)

type session struct {
	connected bool
	calls     atomic.Int64
	closed    *atomic.Int64
}

func (s *session) Close() error {
	s.closed.Add(1)
	return nil
}

func main() {
	var closed atomic.Int64
	pluginsdk.Serve(&pluginsdk.Plugin{
		Name:        "echo",
		Version:     "1.0.0",
		Description: "Returns what it is given (Stampede's test plugin).",
		NewSession: func(_ context.Context, _ pluginsdk.SessionInfo) (any, error) {
			return &session{closed: &closed}, nil
		},
		Steps: []pluginsdk.Step{
			{
				Name:        "say",
				Description: "Returns text; can sleep, fail or report phases.",
				Schema: `{
					"type": "object",
					"additionalProperties": false,
					"required": ["text"],
					"properties": {
						"text": {"type": "string"},
						"count": {"type": "integer", "minimum": 0},
						"addr": {"type": "string", "x-stampede-target": true},
						"sleep": {"type": "string"},
						"fail": {"type": "string"},
						"mode": {"enum": ["plain", "loud"]},
						"tags": {"type": "array", "items": {"type": "string"}}
					}
				}`,
				Run: say,
			},
			{
				Name:   "connect",
				Schema: `{"type": "object", "properties": {"addr": {"type": "string", "x-stampede-target": true}}}`,
				Run: func(_ context.Context, c *pluginsdk.Call) (*pluginsdk.Result, error) {
					s := c.Session.(*session)
					if s.connected {
						return &pluginsdk.Result{Skipped: true, Values: map[string]any{"reused": true}}, nil
					}
					s.connected = true
					return &pluginsdk.Result{Latency: time.Millisecond, Phases: pluginsdk.Phases{Connect: 700 * time.Microsecond}, Values: map[string]any{"reused": false}}, nil
				},
			},
			{
				Name:   "crash",
				Schema: `{"type": "object"}`,
				Run: func(context.Context, *pluginsdk.Call) (*pluginsdk.Result, error) {
					os.Exit(3)
					return nil, nil
				},
			},
			{
				Name:   "panic",
				Schema: `{"type": "object"}`,
				Run: func(context.Context, *pluginsdk.Call) (*pluginsdk.Result, error) {
					panic("echo was told to panic")
				},
			},
			{
				Name:   "closed",
				Schema: `{"type": "object"}`,
				Run: func(context.Context, *pluginsdk.Call) (*pluginsdk.Result, error) {
					return &pluginsdk.Result{Values: map[string]any{"closed": closed.Load()}}, nil
				},
			},
		},
	})
}

func say(ctx context.Context, c *pluginsdk.Call) (*pluginsdk.Result, error) {
	var cfg struct {
		Text  string   `json:"text"`
		Count int      `json:"count"`
		Addr  string   `json:"addr"`
		Sleep string   `json:"sleep"`
		Fail  string   `json:"fail"`
		Mode  string   `json:"mode"`
		Tags  []string `json:"tags"`
	}
	if err := c.Decode(&cfg); err != nil {
		return nil, err
	}
	s := c.Session.(*session)
	n := s.calls.Add(1)
	if cfg.Sleep != "" {
		d, err := time.ParseDuration(cfg.Sleep)
		if err != nil {
			return nil, pluginsdk.Fail("invalid config", err)
		}
		t := time.NewTimer(d)
		defer t.Stop()
		select {
		case <-t.C:
		case <-ctx.Done():
			return nil, pluginsdk.Fail("echo timeout", ctx.Err())
		}
	}
	// The reported latency (3ms) is real: the host never records more
	// than the call took.
	time.Sleep(3 * time.Millisecond)
	if cfg.Fail != "" {
		return &pluginsdk.Result{Latency: 2 * time.Millisecond}, pluginsdk.Fail(cfg.Fail, errors.New("told to fail"))
	}
	return &pluginsdk.Result{
		Latency:  3 * time.Millisecond,
		Phases:   pluginsdk.Phases{Wait: 2 * time.Millisecond},
		BytesOut: int64(len(cfg.Text)), BytesIn: int64(len(cfg.Text)),
		Values: map[string]any{
			"text": cfg.Text, "count": cfg.Count, "vu": c.VU, "iteration": c.Iteration,
			"calls": n, "traceparent": c.Traceparent, "tags": cfg.Tags,
		},
	}, nil
}
