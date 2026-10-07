package server_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Ivan825/Stampede/internal/server"
	"github.com/Ivan825/Stampede/internal/store/db"
	"github.com/Ivan825/Stampede/internal/store/storetest"
)

// TestTimelineRollupsAndRetention serves a run's timeline at 10s and 1m
// resolution and expires old per-second metrics.
func TestTimelineRollupsAndRetention(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer target.Close()
	st := storetest.Open(t)
	key, _ := keyringKey()
	srv, err := server.New(server.Config{Store: st, Keyring: key, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	hs := httptest.NewServer(srv.Handler())
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(func() {
		cancel()
		sctx, scancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer scancel()
		srv.Shutdown(sctx)
		hs.Close()
	})
	c := newClient(t, hs.URL)
	setup(t, c)
	var proj, tgt, sc, run map[string]any
	c.do("POST", "/projects", map[string]string{"name": "p"}, &proj)
	pid := proj["id"].(string)
	c.do("POST", "/projects/"+pid+"/targets", map[string]any{"name": "t", "baseURL": target.URL}, &tgt)
	c.do("POST", "/projects/"+pid+"/scenarios", map[string]string{"yaml": "metadata: {name: one}\njourneys: [{name: a, steps: [{get: /}]}]\nload: {iterations: 1}"}, &sc)
	c.do("POST", "/projects/"+pid+"/runs", map[string]any{"scenarioId": sc["id"], "targetId": tgt["id"]}, &run)
	rid := uuid.MustParse(run["id"].(string))
	eventually(t, 20*time.Second, "the run to finish", func() bool {
		c.do("GET", "/runs/"+rid.String(), nil, &run)
		return run["status"] == "completed"
	})

	// Replace its metrics with 70 seconds of history from 40 days ago.
	if _, err := st.Pool.Exec(context.Background(), "DELETE FROM run_metrics WHERE run_id = $1", rid); err != nil {
		t.Fatal(err)
	}
	t0 := time.Now().Add(-40 * 24 * time.Hour).Truncate(time.Minute)
	for i := 0; i < 70; i++ {
		if err := st.InsertRunMetric(context.Background(), db.InsertRunMetricParams{
			RunID: rid, Ts: t0.Add(time.Duration(i) * time.Second), Interval: int32(i), Requests: 10, Failed: 1, Rps: 10,
			ErrorRate: 0.1, P50: 0.01, P95: 0.02, P99: 0.03, Vus: 5, Planned: 10, Iterations: 10, Snapshot: []byte("{}"),
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.RefreshRollups(context.Background(), t0.Add(-time.Hour), t0.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	var pts []map[string]any
	c.do("GET", "/runs/"+rid.String()+"/timeline?resolution=10s", nil, &pts)
	if len(pts) != 7 || pts[1]["t"] != float64(10) || pts[0]["errorRate"] != 0.1 || pts[0]["rps"] != float64(10) {
		t.Errorf("10s timeline: %v", pts)
	}
	c.do("GET", "/runs/"+rid.String()+"/timeline?resolution=1m", nil, &pts)
	if len(pts) != 2 || pts[1]["t"] != float64(60) {
		t.Errorf("1m timeline: %v", pts)
	}
	c.do("GET", "/runs/"+rid.String()+"/timeline", nil, &pts)
	if len(pts) != 70 {
		t.Errorf("1s timeline: %d points", len(pts))
	}
	if code := c.do("GET", "/runs/"+rid.String()+"/timeline?resolution=5s", nil, nil); code != 422 {
		t.Errorf("bad resolution: %d", code)
	}

	// A 30-day retention removes the 40-day-old per-second points.
	if err := srv.StartMetricsRetention(ctx, 30*24*time.Hour); err != nil {
		t.Fatal(err)
	}
	cont, err := st.ContinuousRollups(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !cont {
		eventually(t, 10*time.Second, "old metrics to be deleted", func() bool {
			c.do("GET", "/runs/"+rid.String()+"/timeline", nil, &pts)
			return len(pts) == 0
		})
	}
	if err := srv.StartMetricsRetention(ctx, time.Hour); err == nil {
		t.Error("a retention under a day should be refused")
	}
}
