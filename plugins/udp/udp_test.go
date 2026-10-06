package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"path/filepath"
	"testing"
	"time"

	pluginv1 "github.com/Ivan825/Stampede/gen/stampede/plugin/v1"
	"github.com/Ivan825/Stampede/internal/pluginhost/plugintest/runtest"
	"github.com/Ivan825/Stampede/internal/scenario"
	"github.com/Ivan825/Stampede/pkg/pluginsdk"
	"github.com/Ivan825/Stampede/pkg/pluginsdk/conformance"
)

// echoServer answers every datagram with "echo:" and the datagram, except
// "silent", which it ignores, and "twice", which gets a stale reply first.
func echoServer(t *testing.T) string {
	t.Helper()
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pc.Close() })
	go func() {
		buf := make([]byte, 65535)
		for {
			n, from, err := pc.ReadFrom(buf)
			if err != nil {
				return
			}
			msg := buf[:n]
			switch {
			case bytes.Equal(msg, []byte("silent")):
				continue
			case bytes.Equal(msg, []byte("twice")):
				_, _ = pc.WriteTo([]byte("stale"), from)
			}
			_, _ = pc.WriteTo(append([]byte("echo:"), msg...), from)
		}
	}()
	return pc.LocalAddr().String()
}

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

func (h *harness) send(cfg map[string]any, timeout time.Duration) (*pluginv1.ExecuteResponse, map[string]any) {
	h.t.Helper()
	b, _ := json.Marshal(cfg)
	r, err := h.srv.Execute(context.Background(), &pluginv1.ExecuteRequest{Session: h.ses, Step: "send", Config: b, TimeoutNs: int64(timeout)})
	if err != nil {
		h.t.Fatal(err)
	}
	var vals map[string]any
	if len(r.GetValues()) > 0 {
		_ = json.Unmarshal(r.GetValues(), &vals)
	}
	return r, vals
}

func TestSend(t *testing.T) {
	addr := echoServer(t)
	h := newHarness(t)

	r, v := h.send(map[string]any{"addr": addr, "payload": "ping", "reply": true}, time.Second)
	if !r.GetOk() || v["reply"] != "echo:ping" || r.GetBytesOut() != 4 || r.GetBytesIn() != 9 || r.GetPhasesNs()["wait"] == 0 {
		t.Fatalf("text: %+v %v", r, v)
	}
	r, v = h.send(map[string]any{"addr": addr, "payload": "ff 00 01", "encoding": "hex", "reply": true}, time.Second)
	if !r.GetOk() || v["replyHex"] != "6563686f3aff0001" || v["replyEncoding"] != "base64" {
		t.Fatalf("hex: %+v %v", r, v)
	}
	r, v = h.send(map[string]any{"addr": addr, "payload": "aGk=", "encoding": "base64", "reply": true}, time.Second)
	if !r.GetOk() || v["reply"] != "echo:hi" {
		t.Fatalf("base64: %+v %v", r, v)
	}
	if r, v := h.send(map[string]any{"addr": addr, "payload": "fire and forget"}, time.Second); !r.GetOk() || v["bytesSent"] != float64(15) {
		t.Fatalf("no reply: %+v %v", r, v)
	}
	// The forgotten reply is still queued; match skips it.
	r, v = h.send(map[string]any{"addr": addr, "payload": "twice", "reply": true, "match": "^echo:twice$"}, time.Second)
	if !r.GetOk() || v["reply"] != "echo:twice" {
		t.Fatalf("match: %+v %v", r, v)
	}
	start := time.Now()
	r, _ = h.send(map[string]any{"addr": addr, "payload": "silent", "reply": true}, 100*time.Millisecond)
	if r.GetOk() || r.GetErrorClass() != "udp timeout" || time.Since(start) > time.Second {
		t.Fatalf("timeout: %+v", r)
	}
	if r, _ := h.send(map[string]any{"addr": addr, "payload": "zz", "encoding": "hex"}, time.Second); r.GetErrorClass() != "invalid config" {
		t.Fatalf("bad hex: %+v", r)
	}
	if r, _ := h.send(map[string]any{"addr": addr, "match": "("}, time.Second); r.GetErrorClass() != "invalid config" {
		t.Fatalf("bad regex: %+v", r)
	}
}

func TestRefused(t *testing.T) {
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := pc.LocalAddr().String()
	_ = pc.Close()
	h := newHarness(t)
	// The first datagram's ICMP error surfaces on the read.
	r, _ := h.send(map[string]any{"addr": addr, "payload": "x", "reply": true}, 500*time.Millisecond)
	if r.GetOk() || (r.GetErrorClass() != "udp refused" && r.GetErrorClass() != "udp timeout") {
		t.Fatalf("closed port: %+v", r)
	}
}

// TestExample runs examples/game-ping.yaml through Stampede's engine with
// the built plugin.
func TestExample(t *testing.T) {
	addr := echoServer(t)
	bin := conformance.Build(t, ".", "udp")
	res := runtest.Run(t, "examples/game-ping.yaml", filepath.Dir(bin), map[string]string{"UDP_ADDR": addr},
		&scenario.Load{VUs: 5, Iterations: 50})
	ping, pos := res.Step(t, "ping"), res.Step(t, "position")
	if ping.Requests != 50 || ping.Failed != 0 || pos.Requests != 50 || pos.Failed != 0 {
		t.Fatalf("ping %d/%d failed (%v), position %d/%d failed", ping.Failed, ping.Requests, ping.Errors, pos.Failed, pos.Requests)
	}
	if ping.Protocols["udp"] != 50 || ping.BytesIn == 0 {
		t.Errorf("ping protocols %v, %d bytes in", ping.Protocols, ping.BytesIn)
	}
}

func TestConformance(t *testing.T) {
	addr := echoServer(t)
	conformance.Run(t, conformance.Build(t, ".", "udp"), conformance.Options{
		Cases: []conformance.Case{
			{Step: "send", Config: map[string]any{"addr": addr, "payload": "ping", "reply": true, "match": "^echo:ping$"}},
			{Step: "send", Config: map[string]any{"addr": addr, "payload": "00ff", "encoding": "hex"}},
			{Step: "send", Config: map[string]any{"addr": addr, "payload": "zz", "encoding": "hex"}, WantClass: "invalid config"},
		},
	})
}
