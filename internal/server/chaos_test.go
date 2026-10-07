package server_test

import (
	"context"
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

	"github.com/jackc/pgx/v5"

	"github.com/Ivan825/Stampede/internal/server"
	"github.com/Ivan825/Stampede/internal/store"
	"github.com/Ivan825/Stampede/internal/store/storetest"
)

// cutProxy forwards TCP to the database until cut, which drops every
// connection and refuses new ones until restore.
type cutProxy struct {
	ln       net.Listener
	upstream string
	mu       sync.Mutex
	down     bool
	conns    []net.Conn
}

func newCutProxy(t *testing.T, upstream string) *cutProxy {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	p := &cutProxy{ln: ln, upstream: upstream}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			p.mu.Lock()
			if p.down {
				p.mu.Unlock()
				c.Close()
				continue
			}
			u, err := net.Dial("tcp", upstream)
			if err != nil {
				p.mu.Unlock()
				c.Close()
				continue
			}
			p.conns = append(p.conns, c, u)
			p.mu.Unlock()
			go func() { _, _ = io.Copy(u, c); u.Close() }()
			go func() { _, _ = io.Copy(c, u); c.Close() }()
		}
	}()
	t.Cleanup(func() { ln.Close(); p.cut() })
	return p
}

func (p *cutProxy) cut() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.down = true
	for _, c := range p.conns {
		c.Close()
	}
	p.conns = nil
}

func (p *cutProxy) restore() {
	p.mu.Lock()
	p.down = false
	p.mu.Unlock()
}

// The database goes away while a run is ending: the load is unaffected,
// and the report and final status are written once it is back.
func TestDatabaseOutageDuringRun(t *testing.T) {
	dbURL := storetest.URL(t)
	cfg, err := pgx.ParseConfig(dbURL)
	if err != nil {
		t.Fatal(err)
	}
	proxy := newCutProxy(t, net.JoinHostPort(cfg.Host, itoa(int(cfg.Port))))
	viaProxy := strings.Replace(dbURL, net.JoinHostPort(cfg.Host, itoa(int(cfg.Port))), proxy.ln.Addr().String(), 1)
	ctx := context.Background()
	st, err := store.Open(ctx, viaProxy)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(st.Close)
	if err := st.Migrate(ctx, slog.New(slog.NewTextHandler(io.Discard, nil)), false); err != nil {
		t.Fatal(err)
	}
	key, _ := keyringKey()
	srv, err := server.New(server.Config{Store: st, Keyring: key, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	hs := httptest.NewServer(srv.Handler())
	t.Cleanup(func() {
		proxy.restore()
		sctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		srv.Shutdown(sctx)
		hs.Close()
	})

	var hits atomicCounter
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hits.add() }))
	defer target.Close()
	c := newClient(t, hs.URL)
	setup(t, c)
	var proj, tgt, sc, run map[string]any
	c.do("POST", "/projects", map[string]string{"name": "Chaos"}, &proj)
	pid := proj["id"].(string)
	c.do("POST", "/projects/"+pid+"/targets", map[string]any{"name": "local", "baseURL": target.URL}, &tgt)
	c.do("POST", "/projects/"+pid+"/scenarios", map[string]string{"yaml": `
metadata: {name: chaos}
target: {baseURL: "http://ignored.example"}
journeys: [{name: a, steps: [{get: /}]}]
load: {mode: rate, rate: 50/s, duration: 4s, gracefulStop: 1s}
targets: ["errors < 1%"]`}, &sc)
	if code := c.do("POST", "/projects/"+pid+"/runs", map[string]any{"scenarioId": sc["id"], "targetId": tgt["id"]}, &run); code != 201 {
		t.Fatalf("run: %d", code)
	}
	rid := run["id"].(string)

	// Cut the database two seconds in, past the end of the load, then
	// bring it back.
	time.Sleep(2 * time.Second)
	proxy.cut()
	time.Sleep(5 * time.Second)
	proxy.restore()

	deadline := time.Now().Add(60 * time.Second)
	var got map[string]any
	for time.Now().Before(deadline) {
		got = nil
		if c.do("GET", "/runs/"+rid, nil, &got) == 200 && got["status"] == "completed" {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	if got["status"] != "completed" || got["verdict"] != "pass" {
		t.Fatalf("run after the outage: %v", got)
	}
	if n := got["summary"].(map[string]any)["requests"].(float64); n != 200 || hits.get() != 200 {
		t.Errorf("requests in the report %v, at the target %d; want 200 (50/s for 4s)", n, hits.get())
	}
	var rep map[string]any
	if code := c.do("GET", "/runs/"+rid+"/report", nil, &rep); code != 200 {
		t.Fatalf("report: %d", code)
	}
}

type atomicCounter struct {
	mu sync.Mutex
	n  int
}

func (a *atomicCounter) add() { a.mu.Lock(); a.n++; a.mu.Unlock() }
func (a *atomicCounter) get() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.n
}

func itoa(n int) string { return strconv.Itoa(n) }
