package agent

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// echo serves line echo and, for "BIG n\n", n bytes.
func echo(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				r := bufio.NewReader(c)
				for {
					line, err := r.ReadString('\n')
					if err != nil {
						return
					}
					if n, ok := strings.CutPrefix(strings.TrimSpace(line), "BIG "); ok {
						size, _ := strconv.Atoi(n)
						_, _ = c.Write(bytes.Repeat([]byte{'x'}, size))
						continue
					}
					_, _ = c.Write([]byte(line))
				}
			}()
		}
	}()
	return ln.Addr().String()
}

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func proxyTo(t *testing.T, upstream string) *Proxy {
	t.Helper()
	p := NewProxy("db", "127.0.0.1:0", upstream, quiet())
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	if err := p.Start(ctx); err != nil {
		t.Fatal(err)
	}
	return p
}

func roundTrip(t *testing.T, c net.Conn, r *bufio.Reader) (time.Duration, error) {
	t.Helper()
	start := time.Now()
	if _, err := c.Write([]byte("ping\n")); err != nil {
		return 0, err
	}
	_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
	line, err := r.ReadString('\n')
	if err != nil {
		return 0, err
	}
	if line != "ping\n" {
		t.Fatalf("got %q", line)
	}
	return time.Since(start), nil
}

func TestProxyAddsLatencyAndClears(t *testing.T) {
	p := proxyTo(t, echo(t))
	c, err := net.Dial("tcp", p.Addr())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	r := bufio.NewReader(c)
	base, err := roundTrip(t, c, r)
	if err != nil {
		t.Fatal(err)
	}
	p.SetFault(Fault{Latency: 60 * time.Millisecond})
	slow, err := roundTrip(t, c, r)
	if err != nil {
		t.Fatal(err)
	}
	if slow < 120*time.Millisecond || slow > 400*time.Millisecond {
		t.Errorf("round trip with 60ms each way = %s (base %s)", slow, base)
	}
	p.SetFault(Fault{})
	if fast, _ := roundTrip(t, c, r); fast > 60*time.Millisecond {
		t.Errorf("after clearing, round trip = %s", fast)
	}
}

func TestProxyLimitsBandwidth(t *testing.T) {
	p := proxyTo(t, echo(t))
	p.SetFault(Fault{Bandwidth: 200 << 10})
	c, err := net.Dial("tcp", p.Addr())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	start := time.Now()
	_, _ = c.Write([]byte("BIG 102400\n"))
	_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, err := io.ReadFull(c, make([]byte, 102400)); err != nil {
		t.Fatal(err)
	}
	// 100 KiB at 200 KiB/s, less a 20 KiB burst: about 0.4 s.
	if d := time.Since(start); d < 300*time.Millisecond || d > 1500*time.Millisecond {
		t.Errorf("100 KiB at 200 KiB/s took %s", d)
	}
}

func TestProxyResetsAndRefuses(t *testing.T) {
	p := proxyTo(t, echo(t))
	c, err := net.Dial("tcp", p.Addr())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	r := bufio.NewReader(c)
	if _, err := roundTrip(t, c, r); err != nil {
		t.Fatal(err)
	}
	p.SetFault(Fault{Reset: true})
	if _, err := roundTrip(t, c, r); err == nil {
		t.Fatal("an open connection must be reset")
	}
	p.SetFault(Fault{Refuse: true})
	c2, err := net.Dial("tcp", p.Addr())
	if err == nil {
		_, err = roundTrip(t, c2, bufio.NewReader(c2))
		c2.Close()
	}
	if err == nil {
		t.Fatal("a new connection must be refused")
	}
	p.SetFault(Fault{})
	c3, err := net.Dial("tcp", p.Addr())
	if err != nil {
		t.Fatal(err)
	}
	defer c3.Close()
	if _, err := roundTrip(t, c3, bufio.NewReader(c3)); err != nil {
		t.Fatalf("after clearing: %v", err)
	}
}

func TestProxyBlackholeHoldsDataUntilCleared(t *testing.T) {
	p := proxyTo(t, echo(t))
	c, err := net.Dial("tcp", p.Addr())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	p.SetFault(Fault{Blackhole: true})
	_, _ = c.Write([]byte("ping\n"))
	_ = c.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
	buf := make([]byte, 5)
	if _, err := c.Read(buf); err == nil {
		t.Fatal("a blackhole must not forward")
	}
	p.SetFault(Fault{})
	_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := io.ReadFull(c, buf); err != nil || string(buf) != "ping\n" {
		t.Fatalf("held data after clearing: %q %v", buf, err)
	}
}

type recorder struct {
	mu   sync.Mutex
	logs bytes.Buffer
}

func (r *recorder) Write(b []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.logs.Write(b)
}
func (r *recorder) String() string { r.mu.Lock(); defer r.mu.Unlock(); return r.logs.String() }

func TestAgentRevertsAfterDurationAndAudits(t *testing.T) {
	p := proxyTo(t, echo(t))
	var audit recorder
	a := New(Config{Proxies: []*Proxy{p}, Audit: slog.New(slog.NewJSONHandler(&audit, nil)), Logger: quiet(), MaxDuration: time.Minute})
	ctx := context.Background()
	if _, err := a.Apply(ctx, Request{Kind: KindProxy, Target: "db", Fault: Fault{Latency: time.Millisecond}}); err == nil {
		t.Error("a fault without a duration must be refused")
	}
	if _, err := a.Apply(ctx, Request{Kind: KindProxy, Target: "db", Fault: Fault{Latency: time.Millisecond}, Duration: time.Hour}); err == nil {
		t.Error("a fault over the limit must be refused")
	}
	if _, err := a.Apply(ctx, Request{Kind: KindProxy, Target: "nope", Fault: Fault{Reset: true}, Duration: time.Second}); err == nil {
		t.Error("an unknown proxy must be refused")
	}
	if _, err := a.Apply(ctx, Request{Kind: KindContainer, Target: "x", Action: "pause", Duration: time.Second}); !errors.Is(err, ErrNotAllowed) {
		t.Errorf("container actions without --allow-docker: %v", err)
	}
	act, err := a.Apply(ctx, Request{Kind: KindProxy, Target: "db", Fault: Fault{Latency: 5 * time.Millisecond}, Duration: 150 * time.Millisecond, Run: "r1"})
	if err != nil {
		t.Fatal(err)
	}
	// A second fault on the same proxy replaces the first.
	act2, err := a.Apply(ctx, Request{Kind: KindProxy, Target: "db", Fault: Fault{Latency: 9 * time.Millisecond}, Duration: 150 * time.Millisecond, Run: "r1"})
	if err != nil {
		t.Fatal(err)
	}
	if n := len(a.Active()); n != 1 || a.Active()[0].ID != act2.ID || p.Fault().Latency != 9*time.Millisecond {
		t.Fatalf("active = %+v", a.Active())
	}
	time.Sleep(400 * time.Millisecond)
	if len(a.Active()) != 0 || !p.Fault().IsZero() {
		t.Fatalf("the fault must revert after its duration: %+v %+v", a.Active(), p.Fault())
	}
	log := audit.String()
	for _, want := range []string{`"msg":"fault applied"`, `"why":"replaced"`, `"why":"duration ended"`, `"id":"` + act.ID + `"`, `"run":"r1"`} {
		if !strings.Contains(log, want) {
			t.Errorf("audit log lacks %s:\n%s", want, log)
		}
	}
}

func TestAPIRequiresTokenAndKillSwitchRevertsAll(t *testing.T) {
	p := proxyTo(t, echo(t))
	a := New(Config{Proxies: []*Proxy{p}, Logger: quiet()})
	srv := httptest.NewServer(a.Handler("tok"))
	defer srv.Close()

	bad := &Client{BaseURL: srv.URL, Token: "wrong"}
	if _, err := bad.Status(context.Background()); err == nil || !strings.Contains(err.Error(), "token") {
		t.Fatalf("wrong token: %v", err)
	}
	c := &Client{BaseURL: srv.URL, Token: "tok"}
	act, err := c.Apply(context.Background(), Request{Kind: KindProxy, Target: "db", Fault: Fault{Latency: 20 * time.Millisecond, Jitter: 5 * time.Millisecond}, Duration: time.Minute, Run: "r1"})
	if err != nil {
		t.Fatal(err)
	}
	if act.Request.Fault.Latency != 20*time.Millisecond || act.Request.Duration != time.Minute {
		t.Errorf("round-tripped request = %+v", act.Request)
	}
	st, err := c.Status(context.Background())
	if err != nil || len(st.Active) != 1 || st.Proxies[0].Fault.Latency != 20*time.Millisecond {
		t.Fatalf("status = %+v, %v", st, err)
	}
	if err := c.RevertAll(context.Background(), "other-run"); err != nil {
		t.Fatal(err)
	}
	if len(a.Active()) != 1 {
		t.Error("clearing another run's faults must leave this one")
	}
	if err := c.RevertAll(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
	if len(a.Active()) != 0 || !p.Fault().IsZero() {
		t.Error("the kill switch must revert everything")
	}
}

func TestDockerPauseAndUnpause(t *testing.T) {
	var mu sync.Mutex
	var calls []string
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		calls = append(calls, r.Method+" "+r.URL.Path)
		mu.Unlock()
		if strings.HasSuffix(r.URL.Path, "/json") {
			_, _ = w.Write([]byte(`{"State":{"Running":true,"Paused":false}}`))
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer api.Close()
	d, err := NewDocker(api.URL, []string{"shop-*"})
	if err != nil {
		t.Fatal(err)
	}
	a := New(Config{Docker: d, Logger: quiet()})
	ctx := context.Background()
	if _, err := a.Apply(ctx, Request{Kind: KindContainer, Target: "postgres", Action: "pause", Duration: time.Second}); !errors.Is(err, ErrNotAllowed) {
		t.Errorf("a container not allowed: %v", err)
	}
	act, err := a.Apply(ctx, Request{Kind: KindContainer, Target: "shop-db", Action: "pause", Duration: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Revert(ctx, act.ID, "test"); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	want := []string{"GET /v1.43/containers/shop-db/json", "POST /v1.43/containers/shop-db/pause", "POST /v1.43/containers/shop-db/unpause"}
	if strings.Join(calls, "|") != strings.Join(want, "|") {
		t.Errorf("calls = %v", calls)
	}
}

func TestKubernetesScaleAndRestore(t *testing.T) {
	var mu sync.Mutex
	var patches []string
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer k8s" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.URL.Path != "/apis/apps/v1/namespaces/shop/deployments/api/scale" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if r.Method == http.MethodPatch {
			b, _ := io.ReadAll(r.Body)
			mu.Lock()
			patches = append(patches, string(b))
			mu.Unlock()
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"spec": map[string]int{"replicas": 4}})
	}))
	defer api.Close()
	k, err := NewKubernetes(api.URL, "k8s", []string{"shop/*"})
	if err != nil {
		t.Fatal(err)
	}
	a := New(Config{Kubernetes: k, Logger: quiet()})
	one := 1
	if _, err := a.Apply(context.Background(), Request{Kind: KindDeployment, Target: "kube-system/coredns", Replicas: &one, Duration: time.Second}); !errors.Is(err, ErrNotAllowed) {
		t.Errorf("a deployment not allowed: %v", err)
	}
	if _, err := a.Apply(context.Background(), Request{Kind: KindDeployment, Target: "shop/api", Replicas: &one, Duration: time.Minute}); err != nil {
		t.Fatal(err)
	}
	if err := a.RevertAll(context.Background(), "", "test"); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if strings.Join(patches, "|") != `{"spec":{"replicas":1}}|{"spec":{"replicas":4}}` {
		t.Errorf("patches = %v", patches)
	}
}
