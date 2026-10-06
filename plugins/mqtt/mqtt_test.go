package main

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"path/filepath"
	"testing"
	"time"

	mochi "github.com/mochi-mqtt/server/v2"
	"github.com/mochi-mqtt/server/v2/hooks/auth"
	"github.com/mochi-mqtt/server/v2/listeners"
	"github.com/mochi-mqtt/server/v2/packets"

	pluginv1 "github.com/Ivan825/Stampede/gen/stampede/plugin/v1"
	"github.com/Ivan825/Stampede/internal/pluginhost/plugintest/runtest"
	"github.com/Ivan825/Stampede/internal/scenario"
	"github.com/Ivan825/Stampede/pkg/pluginsdk"
	"github.com/Ivan825/Stampede/pkg/pluginsdk/conformance"
)

// broker starts an in-process MQTT broker. With users set it requires
// those credentials; it also answers "rpc/request" with "rpc/reply".
func broker(t *testing.T, users map[string]string) (string, *mochi.Server) {
	t.Helper()
	s := mochi.New(&mochi.Options{InlineClient: true, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if users == nil {
		if err := s.AddHook(new(auth.AllowHook), nil); err != nil {
			t.Fatal(err)
		}
	} else {
		var rules auth.AuthRules
		for u, p := range users {
			rules = append(rules, auth.AuthRule{Username: auth.RString(u), Password: auth.RString(p), Allow: true})
		}
		if err := s.AddHook(new(auth.Hook), &auth.Options{Ledger: &auth.Ledger{Auth: rules, ACL: auth.ACLRules{{Filters: auth.Filters{"#": auth.ReadWrite}}}}}); err != nil {
			t.Fatal(err)
		}
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.AddListener(listeners.NewNet("t", ln)); err != nil {
		t.Fatal(err)
	}
	if err := s.Serve(); err != nil {
		t.Fatal(err)
	}
	if err := s.Subscribe("rpc/request", 1, func(_ *mochi.Client, _ packets.Subscription, pk packets.Packet) {
		_ = s.Publish("rpc/reply", append([]byte("re:"), pk.Payload...), false, 0)
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return "tcp://" + ln.Addr().String(), s
}

type harness struct {
	t   *testing.T
	srv pluginv1.PluginServiceServer
	ses string
}

func newHarness(t *testing.T, vu int64) *harness {
	t.Helper()
	srv, err := pluginsdk.NewServer(newPlugin())
	if err != nil {
		t.Fatal(err)
	}
	o, err := srv.Open(context.Background(), &pluginv1.OpenRequest{Vu: vu, RunId: "test"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = srv.Close(context.Background(), &pluginv1.CloseRequest{Session: o.GetSession()}) })
	return &harness{t: t, srv: srv, ses: o.GetSession()}
}

func (h *harness) run(step string, cfg map[string]any, timeout time.Duration) (*pluginv1.ExecuteResponse, map[string]any) {
	h.t.Helper()
	b, _ := json.Marshal(cfg)
	r, err := h.srv.Execute(context.Background(), &pluginv1.ExecuteRequest{Session: h.ses, Step: step, Config: b, TimeoutNs: int64(timeout)})
	if err != nil {
		h.t.Fatal(err)
	}
	var vals map[string]any
	_ = json.Unmarshal(r.GetValues(), &vals)
	return r, vals
}

func TestConnectPublishSubscribe(t *testing.T) {
	url, srv := broker(t, nil)
	h := newHarness(t, 7)

	if r, _ := h.run("publish", map[string]any{"topic": "a"}, time.Second); r.GetErrorClass() != "mqtt not connected" {
		t.Fatalf("publish before connect: %+v", r)
	}
	r, v := h.run("connect", map[string]any{"broker": url}, 2*time.Second)
	if !r.GetOk() || v["clientId"] != "stampede-test-7" || r.GetPhasesNs()["connect"] == 0 {
		t.Fatalf("connect: %+v %v", r, v)
	}
	if r, _ := h.run("connect", map[string]any{"broker": url}, 2*time.Second); !r.GetSkipped() {
		t.Fatalf("a second connect reuses the connection: %+v", r)
	}
	if r, _ := h.run("connect", map[string]any{"broker": url, "fresh": true}, 2*time.Second); !r.GetOk() || r.GetSkipped() {
		t.Fatalf("fresh connect: %+v", r)
	}

	if r, _ := h.run("subscribe", map[string]any{"topic": "devices/+/telemetry", "qos": 1}, 2*time.Second); !r.GetOk() {
		t.Fatalf("subscribe: %+v", r)
	}
	if r, _ := h.run("subscribe", map[string]any{"topic": "devices/+/telemetry", "qos": 1}, 2*time.Second); !r.GetSkipped() {
		t.Fatalf("a repeated subscribe is skipped: %+v", r)
	}
	for _, qos := range []int{0, 1, 2} {
		r, v := h.run("publish", map[string]any{"topic": "devices/7/telemetry", "payload": `{"temp": 21, "qos": ` + string(rune('0'+qos)) + `}`, "qos": qos}, 2*time.Second)
		if !r.GetOk() || r.GetBytesOut() == 0 {
			t.Fatalf("publish qos %d: %+v", qos, r)
		}
		if _, ok := v["messageId"]; ok != (qos > 0) {
			t.Fatalf("publish qos %d values %v", qos, v)
		}
		r, v = h.run("expect", map[string]any{"topic": "devices/7/#", "json": map[string]any{"$.qos": qos, "$.temp": "exists"}}, 2*time.Second)
		if !r.GetOk() || v["topic"] != "devices/7/telemetry" || r.GetLatencyNs() <= 0 || r.GetLatencyNs() > int64(time.Second) {
			t.Fatalf("expect qos %d: %+v %v", qos, r, v)
		}
		if j, _ := v["json"].(map[string]any); j["temp"] != float64(21) {
			t.Fatalf("expect json: %v", v)
		}
	}

	// Several messages: counted as events with a time to the first.
	for i := range 3 {
		_ = srv.Publish("devices/9/telemetry", []byte{'m', byte('0' + i)}, false, 0)
	}
	r, v = h.run("expect", map[string]any{"count": 3, "match": "^m"}, 2*time.Second)
	if !r.GetOk() || v["messages"] != float64(3) || r.GetEvents() != 3 || r.GetPhasesNs()["firstEvent"] < 0 {
		t.Fatalf("expect 3: %+v %v", r, v)
	}
	start := time.Now()
	r, _ = h.run("expect", map[string]any{"match": "never"}, 150*time.Millisecond)
	if r.GetErrorClass() != "mqtt expect timeout" || time.Since(start) > time.Second {
		t.Fatalf("expect timeout: %+v", r)
	}

	// Request and reply through a broker-side responder.
	if r, _ := h.run("subscribe", map[string]any{"topic": "rpc/reply"}, 2*time.Second); !r.GetOk() {
		t.Fatal(r)
	}
	h.run("publish", map[string]any{"topic": "rpc/request", "payload": "ping", "qos": 1}, 2*time.Second)
	if r, v := h.run("expect", map[string]any{"topic": "rpc/reply"}, 2*time.Second); !r.GetOk() || v["payload"] != "re:ping" {
		t.Fatalf("rpc: %+v %v", r, v)
	}
	if r, _ := h.run("disconnect", map[string]any{}, time.Second); !r.GetOk() {
		t.Fatal(r)
	}
	if r, _ := h.run("expect", map[string]any{}, time.Second); r.GetErrorClass() != "mqtt not connected" {
		t.Fatalf("expect after disconnect: %+v", r)
	}
}

func TestConnectFailures(t *testing.T) {
	url, _ := broker(t, map[string]string{"device": "s3cret"})
	h := newHarness(t, 1)
	if r, _ := h.run("connect", map[string]any{"broker": url, "username": "device", "password": "wrong"}, 2*time.Second); r.GetErrorClass() != "mqtt not authorized" {
		t.Fatalf("bad password: %+v", r)
	}
	if r, _ := h.run("connect", map[string]any{"broker": url, "username": "device", "password": "s3cret"}, 2*time.Second); !r.GetOk() {
		t.Fatalf("good password: %+v", r)
	}
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	dead := "mqtt://" + ln.Addr().String()
	_ = ln.Close()
	if r, _ := h.run("connect", map[string]any{"broker": dead, "fresh": true}, 2*time.Second); r.GetOk() || r.GetErrorClass() == "" {
		t.Fatalf("no broker: %+v", r)
	}
}

func TestTopicMatches(t *testing.T) {
	for _, tc := range []struct {
		f, t string
		ok   bool
	}{
		{"a/b", "a/b", true}, {"a/+", "a/b", true}, {"a/+", "a/b/c", false}, {"a/#", "a/b/c", true},
		{"#", "x", true}, {"a/+/c", "a/b/c", true}, {"a/b", "a/c", false}, {"a/b/c", "a/b", false},
	} {
		if got := topicMatches(tc.f, tc.t); got != tc.ok {
			t.Errorf("%s ~ %s = %v", tc.f, tc.t, got)
		}
	}
}

func TestExample(t *testing.T) {
	url, _ := broker(t, nil)
	bin := conformance.Build(t, ".", "mqtt")
	res := runtest.Run(t, "examples/telemetry.yaml", filepath.Dir(bin), map[string]string{"MQTT_BROKER": url},
		&scenario.Load{VUs: 20, Iterations: 60})
	if c := res.Step(t, "connect"); c.Requests != 20 || c.Failed != 0 {
		t.Errorf("connect once per device: %d requests, %d failed (%v)", c.Requests, c.Failed, c.Errors)
	}
	for _, name := range []string{"publish telemetry", "delivered"} {
		st := res.Step(t, name)
		if st.Requests != 60 || st.Failed != 0 || st.Protocols["mqtt"] != 60 {
			t.Errorf("%s: %d requests, %d failed (%v)", name, st.Requests, st.Failed, st.Errors)
		}
	}
}

func TestConformance(t *testing.T) {
	url, _ := broker(t, nil)
	conformance.Run(t, conformance.Build(t, ".", "mqtt"), conformance.Options{
		Cases: []conformance.Case{
			{Step: "connect", Config: map[string]any{"broker": url}},
			{Step: "subscribe", Config: map[string]any{"topic": "conf/#", "qos": 1}},
			{Step: "publish", Config: map[string]any{"topic": "conf/x", "payload": "hello", "qos": 1}},
			{Step: "expect", Config: map[string]any{"topic": "conf/x", "match": "hello"}},
			{Step: "expect", Config: map[string]any{"match": "("}, WantClass: "invalid config"},
		},
		VUs: 30,
	})
}
