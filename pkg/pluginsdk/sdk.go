// Package pluginsdk is the public API for writing Stampede protocol
// plugins: steps such as mqtt.publish or kafka.produce that Stampede runs
// next to its built-in HTTP, GraphQL, WebSocket, SSE and gRPC steps.
//
// A plugin is an ordinary Go program. It describes its step types, each
// with a JSON Schema for its configuration, and calls Serve:
//
//	func main() {
//		pluginsdk.Serve(&pluginsdk.Plugin{
//			Name:    "echo",
//			Version: "0.1.0",
//			Steps: []pluginsdk.Step{{
//				Name:   "say",
//				Schema: `{"type":"object","properties":{"text":{"type":"string"}},"required":["text"]}`,
//				Run: func(ctx context.Context, c *pluginsdk.Call) (*pluginsdk.Result, error) {
//					var cfg struct{ Text string `json:"text"` }
//					if err := c.Decode(&cfg); err != nil {
//						return nil, err
//					}
//					return &pluginsdk.Result{Values: map[string]any{"text": cfg.Text}}, nil
//				},
//			}},
//		})
//	}
//
// Built as stampede-plugin-echo and installed with `stampede plugin
// install`, its step is used in a scenario as
//
//	steps:
//	  - plugin: echo.say
//	    with: { text: "hello ${vu}" }
//	    extract: { said: "$.text" }
//
// Stampede starts the plugin as a child process and talks to it over gRPC
// (hashicorp/go-plugin), so a crash in a plugin fails its steps but never
// the worker. Package conformance checks a built plugin against the
// contract.
package pluginsdk

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// Plugin describes a plugin and implements its steps.
type Plugin struct {
	// Name is used in scenarios (mqtt in mqtt.publish) and in the
	// executable's name (stampede-plugin-mqtt): lowercase letters, digits
	// and hyphens, starting with a letter.
	Name string
	// Version is the plugin's own version, such as 0.1.0.
	Version     string
	Description string
	Steps       []Step
	// NewSession creates the state of one virtual user: its connection or
	// client. Every step the user runs receives it as Call.Session. If the
	// value implements io.Closer it is closed when the user retires. Nil
	// gives every user a nil session.
	NewSession func(ctx context.Context, info SessionInfo) (any, error)
}

// SessionInfo identifies the virtual user a session belongs to.
type SessionInfo struct {
	// VU is unique within a run.
	VU    int64
	RunID string
}

// Step is one kind of step.
type Step struct {
	// Name is the step's name within the plugin (publish in mqtt.publish):
	// letters, digits, underscores and hyphens, starting with a letter.
	Name        string
	Description string
	// Schema is a JSON Schema (draft 2020-12) for the step's config, the
	// `with:` block of a scenario step. Stampede checks scenarios against
	// it when they are loaded, and the SDK checks every rendered config
	// before Run sees it. Mark the top-level property holding the address
	// the step connects to with "x-stampede-target": true so Stampede can
	// apply its target policy to it.
	Schema string
	// Run executes the step. A returned error fails the step; use Fail to
	// give it an error class. Run is called concurrently for different
	// sessions, never for the same one.
	Run func(ctx context.Context, c *Call) (*Result, error)
}

// Call is one execution of a step.
type Call struct {
	Step string
	// Session is the value NewSession returned for this virtual user.
	Session   any
	VU        int64
	Iteration int64
	// Config is the step's rendered config, a JSON object that has passed
	// the step's schema.
	Config json.RawMessage
	// Timeout is the step's timeout; ctx carries the same deadline.
	Timeout time.Duration
	// Traceparent and Baggage are W3C trace context for this step, for
	// protocols that can carry it (message headers, metadata).
	Traceparent string
	Baggage     string
}

// Decode unmarshals the config into v, rejecting fields v does not
// define.
func (c *Call) Decode(v any) error {
	d := json.NewDecoder(bytesReader(c.Config))
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		return Fail("invalid config", err)
	}
	return nil
}

// Result is what a step measured and returned.
type Result struct {
	// Latency is the step's duration. Zero means the SDK uses how long Run
	// took.
	Latency time.Duration
	// Phases break the latency down where meaningful.
	Phases Phases
	// BytesIn and BytesOut count payload bytes received and sent.
	BytesIn, BytesOut int64
	// Values is what the step returned, marshalled to a JSON object
	// (a map or a struct). Scenario checks and extractors read it:
	// extract: { id: "$.messageId" }.
	Values any
	// Events counts messages a streaming step received. With
	// Phases.FirstEvent set the step is reported like a stream: time to
	// first message and messages per second.
	Events int64
	// Skipped means there was nothing to do (connect on a session that is
	// already connected): Stampede records no sample for the step.
	Skipped bool
}

// Phases are the parts of a step's latency, named as in Stampede's
// reports. Leave the ones that do not apply at zero.
type Phases struct {
	DNS, Connect, TLS time.Duration
	// Wait is the time from sending to the first byte of the reply.
	Wait time.Duration
	// Download is the time spent reading the reply.
	Download time.Duration
	// FirstEvent is the time from the start of a streaming step to its
	// first message.
	FirstEvent time.Duration
}

func (p Phases) toMap() map[string]int64 {
	m := map[string]int64{}
	for name, d := range map[string]time.Duration{
		"dns": p.DNS, "connect": p.Connect, "tls": p.TLS, "wait": p.Wait,
		"download": p.Download, "firstEvent": p.FirstEvent,
	} {
		if d > 0 {
			m[name] = int64(d)
		}
	}
	return m
}

// Error is a step failure with a class: a short label that groups
// failures in reports, such as "mqtt timeout" or "sql error".
type Error struct {
	Class string
	Err   error
}

func (e *Error) Error() string {
	if e.Err == nil {
		return e.Class
	}
	return e.Class + ": " + e.Err.Error()
}

func (e *Error) Unwrap() error { return e.Err }

// Fail returns an error that fails the step with the given class. The
// class must be bounded: no ids, addresses or other values that vary from
// call to call (put those in err).
func Fail(class string, err error) error { return &Error{Class: class, Err: err} }

// Failf is Fail with a formatted detail.
func Failf(class, format string, args ...any) error {
	return &Error{Class: class, Err: fmt.Errorf(format, args...)}
}

// classify returns an error's class: its Error class, "timeout" for a
// deadline, or "<plugin> error".
func classify(plugin string, err error) string {
	var e *Error
	switch {
	case errors.As(err, &e) && e.Class != "":
		return e.Class
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	}
	return plugin + " error"
}
