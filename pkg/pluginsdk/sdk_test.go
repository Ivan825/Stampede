package pluginsdk_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	pluginv1 "github.com/Ivan825/Stampede/gen/stampede/plugin/v1"
	"github.com/Ivan825/Stampede/pkg/pluginsdk"
)

const objSchema = `{"type":"object","properties":{"n":{"type":"integer"}},"additionalProperties":false}`

func step(name string, run func(context.Context, *pluginsdk.Call) (*pluginsdk.Result, error)) pluginsdk.Step {
	return pluginsdk.Step{Name: name, Schema: objSchema, Run: run}
}

func TestNewServerValidatesDescription(t *testing.T) {
	ok := func(context.Context, *pluginsdk.Call) (*pluginsdk.Result, error) { return nil, nil }
	tests := []struct {
		p    pluginsdk.Plugin
		want string
	}{
		{pluginsdk.Plugin{Name: "Bad Name", Version: "1", Steps: []pluginsdk.Step{step("a", ok)}}, "plugin name"},
		{pluginsdk.Plugin{Name: "x", Steps: []pluginsdk.Step{step("a", ok)}}, "version is empty"},
		{pluginsdk.Plugin{Name: "x", Version: "1"}, "no steps"},
		{pluginsdk.Plugin{Name: "x", Version: "1", Steps: []pluginsdk.Step{step("a", ok), step("a", ok)}}, "described twice"},
		{pluginsdk.Plugin{Name: "x", Version: "1", Steps: []pluginsdk.Step{{Name: "a", Schema: `{"type":"string"}`, Run: ok}}}, `"type": "object"`},
		{pluginsdk.Plugin{Name: "x", Version: "1", Steps: []pluginsdk.Step{{Name: "a", Schema: `{"type":"object","properties":{"p":{"type":"integer","x-stampede-target":true}}}`, Run: ok}}}, "string"},
		{pluginsdk.Plugin{Name: "x", Version: "1", Steps: []pluginsdk.Step{{Name: "a", Schema: `{"type":"object","properties":{"p":{"type":"object","properties":{"h":{"type":"string","x-stampede-target":true}}}}}`, Run: ok}}}, "top-level"},
		{pluginsdk.Plugin{Name: "x", Version: "1", Steps: []pluginsdk.Step{{Name: "a", Schema: `{"type":"object","minProperties":"two"}`, Run: ok}}}, "invalid config schema"},
		{pluginsdk.Plugin{Name: "x", Version: "1", Steps: []pluginsdk.Step{{Name: "a", Schema: objSchema}}}, "no Run"},
	}
	for _, tc := range tests {
		_, err := pluginsdk.NewServer(&tc.p)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%+v: got %v, want %q", tc.p.Name, err, tc.want)
		}
	}
}

type closer struct{ closed *int }

func (c closer) Close() error {
	*c.closed++
	return nil
}

func TestExecute(t *testing.T) {
	closed := 0
	p := &pluginsdk.Plugin{
		Name: "t", Version: "0.1.0",
		NewSession: func(_ context.Context, info pluginsdk.SessionInfo) (any, error) {
			if info.VU < 0 {
				return nil, errors.New("negative vu")
			}
			return closer{&closed}, nil
		},
		Steps: []pluginsdk.Step{
			step("ok", func(_ context.Context, c *pluginsdk.Call) (*pluginsdk.Result, error) {
				var cfg struct{ N int }
				if err := c.Decode(&cfg); err != nil {
					return nil, err
				}
				return &pluginsdk.Result{Values: map[string]any{"n": cfg.N, "vu": c.VU}, Phases: pluginsdk.Phases{Connect: time.Millisecond}}, nil
			}),
			step("fail", func(context.Context, *pluginsdk.Call) (*pluginsdk.Result, error) {
				return &pluginsdk.Result{Latency: time.Second}, pluginsdk.Fail("t refused", errors.New("no"))
			}),
			step("plain", func(context.Context, *pluginsdk.Call) (*pluginsdk.Result, error) {
				return nil, errors.New("something")
			}),
			step("deadline", func(ctx context.Context, _ *pluginsdk.Call) (*pluginsdk.Result, error) {
				<-ctx.Done()
				return nil, ctx.Err()
			}),
			step("array", func(context.Context, *pluginsdk.Call) (*pluginsdk.Result, error) {
				return &pluginsdk.Result{Values: []int{1}}, nil
			}),
			step("boom", func(context.Context, *pluginsdk.Call) (*pluginsdk.Result, error) {
				panic("boom")
			}),
		},
	}
	srv, err := pluginsdk.NewServer(p)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := srv.Open(ctx, &pluginv1.OpenRequest{Vu: -1}); err == nil {
		t.Fatal("NewSession error should fail Open")
	}
	o, err := srv.Open(ctx, &pluginv1.OpenRequest{Vu: 4})
	if err != nil {
		t.Fatal(err)
	}
	run := func(step, cfg string) *pluginv1.ExecuteResponse {
		r, err := srv.Execute(ctx, &pluginv1.ExecuteRequest{Session: o.GetSession(), Step: step, Config: []byte(cfg), TimeoutNs: int64(20 * time.Millisecond)})
		if err != nil {
			t.Fatalf("%s: %v", step, err)
		}
		return r
	}
	r := run("ok", `{"n":3}`)
	if !r.GetOk() || string(r.GetValues()) != `{"n":3,"vu":4}` || r.GetPhasesNs()["connect"] != int64(time.Millisecond) || r.GetLatencyNs() <= 0 {
		t.Fatalf("ok: %+v", r)
	}
	for _, tc := range []struct{ step, cfg, class string }{
		{"ok", `{"n":"x"}`, "invalid config"},
		{"ok", `{"m":1}`, "invalid config"},
		{"ok", `[]`, "invalid config"},
		{"fail", `{}`, "t refused"},
		{"plain", `{}`, "t error"},
		{"deadline", `{}`, "timeout"},
		{"array", `{}`, "invalid values"},
		{"boom", `{}`, "plugin panic"},
		{"missing", `{}`, "unknown step"},
	} {
		r := run(tc.step, tc.cfg)
		if r.GetOk() || r.GetErrorClass() != tc.class {
			t.Errorf("%s %s: got %+v, want class %q", tc.step, tc.cfg, r, tc.class)
		}
	}
	if r := run("fail", `{}`); r.GetLatencyNs() != int64(time.Second) {
		t.Errorf("a failed step keeps its latency: %+v", r)
	}
	if _, err := srv.Close(ctx, &pluginv1.CloseRequest{Session: o.GetSession()}); err != nil || closed != 1 {
		t.Fatalf("close: %v, closed %d", err, closed)
	}
	if _, err := srv.Close(ctx, &pluginv1.CloseRequest{Session: o.GetSession()}); err != nil || closed != 1 {
		t.Fatalf("second close: %v, closed %d", err, closed)
	}
	if r := run("ok", `{}`); r.GetErrorClass() != "unknown session" {
		t.Fatalf("after close: %+v", r)
	}
}
