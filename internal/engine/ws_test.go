package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// wsServer is a chat-like WebSocket service: a welcome message, then a
// pong for each ping, preceded by a heartbeat expect steps must skip.
type wsServer struct {
	open, peak, conns atomic.Int64
	badHandshakes     atomic.Int64
	echoed            atomic.Int64
}

func (s *wsServer) handler(t *testing.T) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/login", func(w http.ResponseWriter, r *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: "session", Value: "s1", Path: "/"})
	})
	mux.HandleFunc("/echo/", func(w http.ResponseWriter, r *http.Request) { s.echoed.Add(1) })
	mux.HandleFunc("/chat", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("traceparent") == "" || r.Header.Get("X-Token") == "" {
			s.badHandshakes.Add(1)
		}
		if c, err := r.Cookie("session"); err != nil || c.Value != "s1" {
			s.badHandshakes.Add(1)
		}
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer c.CloseNow()
		s.track()
		defer s.open.Add(-1)
		ctx := r.Context()
		user := "u-" + strconv.FormatInt(s.conns.Load(), 10)
		_ = c.Write(ctx, websocket.MessageText, []byte(`{"type":"welcome","user":"`+user+`"}`))
		for {
			_, b, err := c.Read(ctx)
			if err != nil {
				return
			}
			var msg struct {
				Type string `json:"type"`
				N    any    `json:"n"`
			}
			if json.Unmarshal(b, &msg) != nil || msg.Type != "ping" {
				continue
			}
			_ = c.Write(ctx, websocket.MessageText, []byte(`{"type":"heartbeat"}`))
			time.Sleep(10 * time.Millisecond)
			pong, _ := json.Marshal(map[string]any{"type": "pong", "n": msg.N})
			_ = c.Write(ctx, websocket.MessageText, pong)
		}
	})
	mux.HandleFunc("/reject", func(w http.ResponseWriter, r *http.Request) { http.Error(w, "no", http.StatusForbidden) })
	mux.HandleFunc("/closer", func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		c.Close(websocket.StatusGoingAway, "bye")
	})
	mux.HandleFunc("/hold", func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer c.CloseNow()
		s.track()
		defer s.open.Add(-1)
		// Read until the client closes; this also answers its close.
		_, _, _ = c.Read(r.Context())
	})
	return mux
}

func (s *wsServer) track() {
	s.conns.Add(1)
	n := s.open.Add(1)
	for {
		p := s.peak.Load()
		if n <= p || s.peak.CompareAndSwap(p, n) {
			return
		}
	}
}

func TestWebSocket(t *testing.T) {
	tests := []struct {
		name    string
		steps   string
		mut     func(*Options)
		wantErr string
		check   func(t *testing.T, out runOut, s *wsServer)
	}{
		{
			name: "chat: welcome, ping/pong loop, extracted variable used later",
			steps: `
      - post: /login
      - ws: /chat
        headers: { X-Token: "t-${vu}" }
        steps:
          - expect: { json: { "$.type": welcome } }
            extract: { user: "$.user" }
          - loop: 3
            steps:
              - send: { type: ping, n: "${iter}", user: "${user}" }
              - expect: { json: { "$.type": pong }, timeout: 2s }
                extract: { n: "$.n" }
          - think: 5ms
      - get: /echo/${user}/${n}`,
			check: func(t *testing.T, out runOut, s *wsServer) {
				if s.badHandshakes.Load() != 0 {
					t.Errorf("%d handshakes lacked trace context, headers or cookies", s.badHandshakes.Load())
				}
				ws, pong := out.total.Steps[1], out.total.Steps[4]
				if ws.Requests != 4 || ws.Status[101] != 4 || ws.Protocols["HTTP/1.1"] != 4 {
					t.Errorf("handshake stats %+v", ws)
				}
				if pong.Requests != 12 || pong.ChecksPassed != 12 {
					t.Errorf("pong stats %+v", pong)
				}
				// Latency runs from the ping to its pong, which the server
				// delays by 10ms.
				if p50 := pong.Latency.Quantile(0.5); p50 < 10_000 || p50 > 200_000 {
					t.Errorf("pong p50 %dµs, want just over 10ms", p50)
				}
				if s.echoed.Load() != 4 {
					t.Errorf("extracted variables reached the next step %d times, want 4", s.echoed.Load())
				}
			},
		},
		{name: "rejected handshake", steps: `[{ws: /reject, steps: [{send: hi}]}]`, wantErr: "HTTP 403"},
		{name: "server closes the connection", steps: `[{ws: /closer, steps: [{expect: anything}]}]`, wantErr: "ws closed"},
		{name: "no matching message", steps: `[{ws: /hold, steps: [{expect: {match: never, timeout: 50ms}}]}]`, wantErr: "ws expect timeout"},
		{
			name: "safety policy applies", steps: `[{ws: /hold, steps: [{send: hi}]}]`, wantErr: "blocked by safety",
			mut: func(o *Options) { o.AllowHost = func(*url.URL) bool { return false } },
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := &wsServer{}
			srv := httptest.NewServer(s.handler(t))
			defer srv.Close()
			out := run(t, fmt.Sprintf(`
metadata: {name: ws}
target: {baseURL: %q}
journeys:
  - name: a
    steps: %s
load: {iterations: 4, vus: 2}`, srv.URL, tc.steps), tc.mut)
			tot := out.total.Totals()
			if tc.wantErr != "" {
				if tot.Errors[tc.wantErr] != 4 {
					t.Fatalf("errors %v, want 4 x %q", tot.Errors, tc.wantErr)
				}
			} else if tot.Failed != 0 {
				t.Fatalf("errors %v", tot.Errors)
			}
			if tc.check != nil {
				tc.check(t, out, s)
			}
		})
	}
}

// TestWebSocketManyConnections holds many connections open at once, one
// per virtual user. STAMPEDE_WS_CONNS raises the count; the in-process
// server needs a second descriptor per connection, so very large counts
// are better run against a separate server process.
func TestWebSocketManyConnections(t *testing.T) {
	n := 1000
	if v, err := strconv.Atoi(os.Getenv("STAMPEDE_WS_CONNS")); err == nil && v > 0 {
		n = v
	}
	if testing.Short() {
		n = 100
	}
	s := &wsServer{}
	srv := httptest.NewServer(s.handler(t))
	defer srv.Close()
	start := time.Now()
	out := run(t, fmt.Sprintf(`
metadata: {name: ws-many}
target: {baseURL: %q}
journeys:
  - name: a
    steps:
      - ws: /hold
        steps:
          - think: 3s
          - send: bye
load: {iterations: %d, vus: %d, gracefulStop: 30s}`, srv.URL, n, n), nil)
	tot := out.total.Totals()
	if tot.Failed != 0 || tot.Requests != uint64(2*n) {
		t.Fatalf("requests %d failed %d errors %v", tot.Requests, tot.Failed, tot.Errors)
	}
	// Every user held its connection through the think, so nearly all
	// were open at once.
	if p := s.peak.Load(); p < int64(n*9/10) {
		t.Errorf("peak %d concurrent connections, want about %d", p, n)
	}
	t.Logf("%d connections, peak %d open at once, handshake p99 %dµs, run %v",
		n, s.peak.Load(), out.total.Steps[0].Latency.Quantile(0.99), time.Since(start).Round(time.Millisecond))
	// Closing is asynchronous; give the server a moment to see it.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for s.open.Load() > 0 && ctx.Err() == nil {
		time.Sleep(10 * time.Millisecond)
	}
	if s.open.Load() != 0 {
		t.Errorf("%d connections still open after the run", s.open.Load())
	}
}
