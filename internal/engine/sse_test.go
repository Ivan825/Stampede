package engine

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Ivan825/Stampede/internal/metrics"
)

// sseServer streams like an LLM chat API: a pause before the first token,
// then tokens at a steady rate.
func sseServer(t *testing.T, badRequests *atomic.Int64) *httptest.Server {
	t.Helper()
	stream := func(w http.ResponseWriter, r *http.Request, first, gap time.Duration, n int, done bool) {
		w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		fl := w.(http.Flusher)
		fmt.Fprint(w, ": keep-alive\n\n")
		fl.Flush()
		for i := 1; n < 0 || i <= n; i++ {
			d := gap
			if i == 1 {
				d = first
			}
			select {
			case <-time.After(d):
			case <-r.Context().Done():
				return
			}
			fmt.Fprintf(w, "event: token\ndata: {\"i\":%d,\"choices\":[{\"delta\":{\"content\":\"tok%d\"}}]}\n\n", i, i)
			fl.Flush()
		}
		if done {
			fmt.Fprint(w, "data: [DONE]\n\n")
		}
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/chat", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Prompt string `json:"prompt"`
		}
		if r.Method != http.MethodPost || r.Header.Get("Accept") != "text/event-stream" || r.Header.Get("traceparent") == "" ||
			json.NewDecoder(r.Body).Decode(&body) != nil || body.Prompt == "" {
			badRequests.Add(1)
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		stream(w, r, 30*time.Millisecond, 2*time.Millisecond, 20, true)
	})
	mux.HandleFunc("/forever", func(w http.ResponseWriter, r *http.Request) {
		stream(w, r, 5*time.Millisecond, 10*time.Millisecond, -1, false)
	})
	mux.HandleFunc("/short", func(w http.ResponseWriter, r *http.Request) {
		stream(w, r, time.Millisecond, time.Millisecond, 3, false)
	})
	mux.HandleFunc("/silent", func(w http.ResponseWriter, r *http.Request) { stream(w, r, 0, 0, 0, false) })
	mux.HandleFunc("/json", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{}`)
	})
	mux.HandleFunc("/fail", func(w http.ResponseWriter, r *http.Request) { http.Error(w, "down", http.StatusServiceUnavailable) })
	mux.HandleFunc("/echo/", func(w http.ResponseWriter, r *http.Request) {})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestSSE(t *testing.T) {
	tests := []struct {
		name       string
		step       string
		mut        func(*Options)
		wantErr    string
		minEvents  uint64
		maxEvents  uint64
		minFirst   time.Duration
		wantSteps2 bool // the step after the stream ran
	}{
		{
			name: "LLM stream until done, then use an extracted token",
			step: `
      - sse: /v1/chat
        json: { prompt: "hello ${vu}" }
        until: { events: 21, match: '\[DONE\]' }
        check: { bodyContains: "[DONE]" }
      - sse: /v1/chat
        json: { prompt: "again" }
        until: { events: 5 }
        extract: { token: "$.choices[0].delta.content", n: "$.i" }
      - get: /echo/${token}/${n}`,
			minEvents: 21 + 5, maxEvents: 21 + 5, minFirst: 30 * time.Millisecond, wantSteps2: true,
		},
		{name: "duration caps an endless stream", step: `[{sse: /forever, until: {duration: 100ms}}]`, minEvents: 5, maxEvents: 12},
		{name: "server closing the stream ends the step", step: `[{sse: /short}]`, minEvents: 3, maxEvents: 3},
		{name: "required events not reached in time", step: `[{sse: /forever, until: {events: 1000, duration: 50ms}}]`, wantErr: "sse ended early", minEvents: 1, maxEvents: 6},
		{name: "required match never arrives", step: `[{sse: /short, until: {match: nope}}]`, wantErr: "sse no match", minEvents: 3, maxEvents: 3},
		{name: "stream without events", step: `[{sse: /silent}]`, wantErr: "sse no events"},
		{name: "not an event stream", step: `[{sse: /json}]`, wantErr: "sse not an event stream"},
		{name: "error status", step: `[{sse: /fail}]`, wantErr: "HTTP 503"},
		{name: "step timeout", step: `[{sse: /forever, timeout: 60ms}]`, wantErr: "timeout", minEvents: 3, maxEvents: 7},
		{
			name: "safety policy applies", step: `[{sse: /short}]`, wantErr: "blocked by safety",
			mut: func(o *Options) { o.AllowHost = func(*url.URL) bool { return false } },
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var bad atomic.Int64
			srv := sseServer(t, &bad)
			out := run(t, fmt.Sprintf(`
metadata: {name: sse}
target: {baseURL: %q}
journeys:
  - name: a
    steps: %s
load: {iterations: 2, vus: 1}`, srv.URL, tc.step), tc.mut)
			if bad.Load() != 0 {
				t.Fatalf("server rejected %d requests", bad.Load())
			}
			var events, failed, streams uint64
			errs := map[string]uint64{}
			for _, st := range out.total.Steps {
				events += st.Events
				failed += st.Failed
				streams += st.Streams
				for k, n := range st.Errors {
					errs[k] += n
				}
			}
			if tc.wantErr != "" {
				if errs[tc.wantErr] != 2 {
					t.Fatalf("errors %v, want 2 x %q", errs, tc.wantErr)
				}
			} else if failed != 0 {
				t.Fatalf("errors %v", errs)
			}
			if per := events / 2; per < tc.minEvents || per > tc.maxEvents {
				t.Errorf("%d events per iteration, want %d..%d", per, tc.minEvents, tc.maxEvents)
			}
			if tc.minFirst > 0 {
				first := out.res.Phases[0][metrics.PhaseFirstEvent]
				if first.Count() != 2 || time.Duration(first.Quantile(0.5))*time.Microsecond < tc.minFirst {
					t.Errorf("first event p50 %dµs over %d streams, want >= %v", first.Quantile(0.5), first.Count(), tc.minFirst)
				}
				if st := out.total.Steps[0]; st.StreamUs == 0 || st.Streams != 2 {
					t.Errorf("stream counters %+v", st)
				}
			}
			if tc.wantSteps2 {
				if st := out.total.Steps[2]; st == nil || st.Requests != 2 || st.Failed != 0 {
					t.Errorf("step after the stream: %+v", st)
				}
			}
		})
	}
}
