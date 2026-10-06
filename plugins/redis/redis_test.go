package main

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"

	pluginv1 "github.com/Ivan825/Stampede/gen/stampede/plugin/v1"
	"github.com/Ivan825/Stampede/internal/pluginhost/plugintest/runtest"
	"github.com/Ivan825/Stampede/internal/scenario"
	"github.com/Ivan825/Stampede/pkg/pluginsdk"
	"github.com/Ivan825/Stampede/pkg/pluginsdk/conformance"
)

type harness struct {
	t   *testing.T
	srv pluginv1.PluginServiceServer
	ses string
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	srv, err := pluginsdk.NewServer(newPlugin())
	if err != nil {
		t.Fatal(err)
	}
	o, err := srv.Open(context.Background(), &pluginv1.OpenRequest{Vu: 1})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = srv.Close(context.Background(), &pluginv1.CloseRequest{Session: o.GetSession()}) })
	return &harness{t: t, srv: srv, ses: o.GetSession()}
}

func (h *harness) run(cfg map[string]any) (*pluginv1.ExecuteResponse, map[string]any) {
	h.t.Helper()
	b, _ := json.Marshal(cfg)
	r, err := h.srv.Execute(context.Background(), &pluginv1.ExecuteRequest{Session: h.ses, Step: "command", Config: b, TimeoutNs: int64(2 * time.Second)})
	if err != nil {
		h.t.Fatal(err)
	}
	var vals map[string]any
	_ = json.Unmarshal(r.GetValues(), &vals)
	return r, vals
}

func TestCommands(t *testing.T) {
	mr := miniredis.RunT(t)
	h := newHarness(t)
	addr := mr.Addr()

	if r, v := h.run(map[string]any{"addr": addr, "command": []any{"SET", "k", "v1"}}); !r.GetOk() || v["value"] != "OK" || r.GetBytesOut() == 0 {
		t.Fatalf("set: %+v %v", r, v)
	}
	if r, v := h.run(map[string]any{"addr": addr, "command": "GET k"}); !r.GetOk() || v["value"] != "v1" || r.GetBytesIn() != 2 {
		t.Fatalf("get: %+v %v", r, v)
	}
	if r, v := h.run(map[string]any{"addr": addr, "command": []any{"INCRBY", "n", 5}}); !r.GetOk() || v["value"] != float64(5) {
		t.Fatalf("incrby: %+v %v", r, v)
	}
	if r, v := h.run(map[string]any{"addr": addr, "command": "GET missing"}); !r.GetOk() || v["value"] != nil {
		t.Fatalf("nil: %+v %v", r, v)
	}
	r, v := h.run(map[string]any{"addr": addr, "pipeline": []any{[]any{"RPUSH", "l", "a", "b"}, "LRANGE l 0 -1", []any{"HSET", "h", "f", 1}}})
	vals, _ := v["values"].([]any)
	if !r.GetOk() || len(vals) != 3 || vals[0] != float64(2) {
		t.Fatalf("pipeline: %+v %v", r, v)
	}
	if l, _ := vals[1].([]any); len(l) != 2 || l[1] != "b" {
		t.Fatalf("lrange in pipeline: %v", vals[1])
	}
	if r, _ := h.run(map[string]any{"addr": addr, "command": "LPUSH k x"}); r.GetOk() || r.GetErrorClass() != "redis WRONGTYPE" {
		t.Fatalf("wrongtype: %+v", r)
	}
	if r, _ := h.run(map[string]any{"addr": "redis://" + addr + "/1", "command": "SET k other"}); !r.GetOk() {
		t.Fatalf("url with db: %+v", r)
	}
	mr.Select(1)
	if got, _ := mr.Get("k"); got != "other" {
		t.Fatalf("db 1 holds %q", got)
	}
	if r, _ := h.run(map[string]any{"addr": addr, "command": "GET k", "pipeline": []any{"GET k"}}); r.GetErrorClass() != "invalid config" {
		t.Fatalf("command and pipeline: %+v", r)
	}
}

func TestAuthAndDownServer(t *testing.T) {
	mr := miniredis.RunT(t)
	mr.RequireAuth("s3cret")
	h := newHarness(t)
	if r, _ := h.run(map[string]any{"addr": mr.Addr(), "command": "PING"}); r.GetOk() || r.GetErrorClass() != "redis NOAUTH" {
		t.Fatalf("no password: %+v", r)
	}
	if r, v := h.run(map[string]any{"addr": mr.Addr(), "password": "s3cret", "command": "PING"}); !r.GetOk() || v["value"] != "PONG" {
		t.Fatalf("password: %+v %v", r, v)
	}
	addr := mr.Addr()
	mr.Close()
	if r, _ := h.run(map[string]any{"addr": addr, "password": "s3cret", "command": "PING"}); r.GetOk() || r.GetErrorClass() != "redis connection error" {
		t.Fatalf("server down: %+v", r)
	}
}

func TestExample(t *testing.T) {
	mr := miniredis.RunT(t)
	bin := conformance.Build(t, ".", "redis")
	res := runtest.Run(t, "examples/session-cache.yaml", filepath.Dir(bin), map[string]string{"REDIS_ADDR": mr.Addr()},
		&scenario.Load{VUs: 10, Iterations: 100})
	for _, name := range []string{"store session", "read session", "touch and count"} {
		st := res.Step(t, name)
		if st.Requests != 100 || st.Failed != 0 || st.Protocols["redis"] != 100 {
			t.Errorf("%s: %d requests, %d failed (%v)", name, st.Requests, st.Failed, st.Errors)
		}
	}
	if n, _ := mr.Get("stats:views"); n != "100" {
		t.Errorf("stats:views = %q, want 100", n)
	}
}

func TestConformance(t *testing.T) {
	mr := miniredis.RunT(t)
	conformance.Run(t, conformance.Build(t, ".", "redis"), conformance.Options{
		Cases: []conformance.Case{
			{Step: "command", Config: map[string]any{"addr": mr.Addr(), "command": []any{"INCR", "c"}}},
			{Step: "command", Config: map[string]any{"addr": mr.Addr(), "pipeline": []any{"SET a 1", "GET a"}}},
			{Step: "command", Config: map[string]any{"addr": mr.Addr(), "command": "NOSUCHCOMMAND"}, WantClass: "redis ERR"},
		},
	})
}
