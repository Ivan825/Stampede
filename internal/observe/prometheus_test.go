package observe

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Ivan825/Stampede/internal/report"
)

// fakeProm is a Prometheus stand-in that answers query_range for a few
// known expressions, recording what it was asked.
type fakeProm struct {
	mu    sync.Mutex
	calls []call
	token string
}

type call struct {
	path, query, start, end, step, auth string
}

func (f *fakeProm) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	f.mu.Lock()
	f.calls = append(f.calls, call{r.URL.Path, r.Form.Get("query"), r.Form.Get("start"), r.Form.Get("end"), r.Form.Get("step"), r.Header.Get("Authorization")})
	f.mu.Unlock()
	if f.token != "" && r.Header.Get("Authorization") != "Bearer "+f.token {
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, "unauthorized")
		return
	}
	if r.URL.Path != "/prom/api/v1/query_range" || r.Method != http.MethodPost {
		http.NotFound(w, r)
		return
	}
	start, _ := strconv.ParseFloat(r.Form.Get("start"), 64)
	step, _ := strconv.ParseFloat(r.Form.Get("step"), 64)
	values := func(vals ...string) [][2]any {
		var out [][2]any
		for i, v := range vals {
			out = append(out, [2]any{start + float64(i)*step, v})
		}
		return out
	}
	type res struct {
		Metric map[string]string `json:"metric"`
		Values [][2]any          `json:"values"`
	}
	ok := func(result ...res) {
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "success", "data": map[string]any{"resultType": "matrix", "result": result}})
	}
	switch q := r.Form.Get("query"); q {
	case "cpu":
		ok(res{map[string]string{"job": "shop"}, values("0.25", "0.5", "NaN", "0.75")})
	case "by_instance":
		ok(res{map[string]string{"instance": "a"}, values("1", "2")}, res{map[string]string{"instance": "b"}, values("3", "4")})
	case "empty":
		ok()
	case "bad(":
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, `{"status":"error","errorType":"bad_data","error":"1:5: parse error: unclosed left parenthesis"}`)
	default:
		w.WriteHeader(http.StatusBadGateway)
		fmt.Fprint(w, "<html>bad gateway</html>")
	}
}

func TestCollect(t *testing.T) {
	f := &fakeProm{token: "s3cret"}
	srv := httptest.NewServer(f)
	defer srv.Close()
	p := &Prometheus{URL: srv.URL + "/prom/", BearerToken: "s3cret"}
	start := time.Unix(1_700_000_000, 0)
	end := start.Add(10 * time.Second)
	got := Collect(context.Background(), p, []Query{
		{"cpu", "cpu"}, {"split", "by_instance"}, {"none", "empty"}, {"syntax", "bad("}, {"proxy", "other"},
	}, start, end, time.Second)

	if len(got) != 5 {
		t.Fatalf("got %d metrics", len(got))
	}
	cpu := got[0]
	if cpu.Name != "cpu" || cpu.Error != "" || len(cpu.Points) != 3 {
		t.Fatalf("cpu = %+v", cpu)
	}
	if cpu.Points[0] != (report.MetricPoint{T: 0, Value: 0.25}) || cpu.Points[2] != (report.MetricPoint{T: 3, Value: 0.75}) {
		t.Errorf("cpu points %+v: NaN should be skipped and times relative to the start", cpu.Points)
	}
	if len(got[1].Points) != 2 || !strings.Contains(got[1].Error, "returned 2 series") {
		t.Errorf("multi-series = %+v", got[1])
	}
	if !strings.Contains(got[2].Error, "no data") || got[2].Points == nil {
		t.Errorf("empty = %+v (points must be an empty list, not null)", got[2])
	}
	if !strings.Contains(got[3].Error, "bad_data") || !strings.Contains(got[3].Error, "unclosed left parenthesis") {
		t.Errorf("syntax error = %q", got[3].Error)
	}
	if !strings.Contains(got[4].Error, "HTTP 502") {
		t.Errorf("non-API error = %q", got[4].Error)
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	c := f.calls[0]
	if c.start != "1700000000.000" || c.end != "1700000010.000" || c.step != "1" || c.auth != "Bearer s3cret" {
		t.Errorf("request = %+v", c)
	}
}

func TestCollectUnauthorized(t *testing.T) {
	srv := httptest.NewServer(&fakeProm{token: "right"})
	defer srv.Close()
	got := Collect(context.Background(), &Prometheus{URL: srv.URL + "/prom", BearerToken: "wrong"}, []Query{{"cpu", "cpu"}}, time.Now().Add(-time.Minute), time.Now(), time.Second)
	if !strings.Contains(got[0].Error, "HTTP 401") {
		t.Errorf("error = %q", got[0].Error)
	}
}

func TestStep(t *testing.T) {
	t0 := time.Unix(0, 0)
	if s := Step(t0, t0.Add(time.Minute), time.Second); s != time.Second {
		t.Errorf("short run step %v", s)
	}
	if s := Step(t0, t0.Add(4*time.Hour), time.Second); s != 2*time.Second {
		t.Errorf("4h run step %v, want 2s (at most %d points)", s, MaxPoints)
	}
	if s := Step(t0, t0.Add(time.Minute), 0); s != time.Second {
		t.Errorf("zero interval step %v", s)
	}
}

func TestApply(t *testing.T) {
	srv := httptest.NewServer(&fakeProm{})
	defer srv.Close()
	start := time.Unix(1_700_000_000, 0)
	rep := &report.Report{Started: start, Ended: start.Add(5 * time.Second), Journeys: []report.Journey{{
		Name: "j", Steps: []report.Step{{Name: "s", Slowest: []report.SlowRequest{{Latency: 1, TraceID: "abc"}}}},
	}}}
	c := &Config{Prometheus: &Prometheus{URL: srv.URL + "/prom"}, Queries: []Query{{"cpu", "cpu"}}, TraceURL: "https://tempo.example.com/trace/{traceId}"}
	c.Apply(context.Background(), rep, time.Second)
	if len(rep.TargetMetrics) != 1 || len(rep.TargetMetrics[0].Points) != 3 {
		t.Errorf("target metrics %+v", rep.TargetMetrics)
	}
	if rep.Journeys[0].Steps[0].Slowest[0].TraceURL != "https://tempo.example.com/trace/abc" {
		t.Errorf("trace link %q", rep.Journeys[0].Steps[0].Slowest[0].TraceURL)
	}
	var nilCfg *Config
	nilCfg.Apply(context.Background(), rep, time.Second) // must not panic
}

func TestQueryRangeRejectsBadURL(t *testing.T) {
	if _, err := (&Prometheus{URL: "file:///etc/passwd"}).QueryRange(context.Background(), "up", time.Now(), time.Now(), time.Second); err == nil {
		t.Error("a non-http URL should be refused")
	}
}
