package store_test

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Ivan825/Stampede/internal/store"
	"github.com/Ivan825/Stampede/internal/store/db"
	"github.com/Ivan825/Stampede/internal/store/storetest"
)

// TestRollupsAndRetention rolls 25 seconds of per-second metrics into
// 10-second and 1-minute buckets (continuous aggregates on TimescaleDB,
// views on plain PostgreSQL) and deletes rows past the retention.
func TestRollupsAndRetention(t *testing.T) {
	s := storetest.Open(t)
	ctx := context.Background()
	run := uuid.New()
	t0 := time.Date(2026, 1, 2, 3, 4, 0, 0, time.UTC)
	for i := 0; i < 25; i++ {
		err := s.InsertRunMetric(ctx, db.InsertRunMetricParams{
			RunID: run, Ts: t0.Add(time.Duration(i) * time.Second), Interval: int32(i),
			Requests: 100, Failed: int64(i % 2), Rps: 100, ErrorRate: 0, P50: 0.01, P95: 0.02 + float64(i)/1000, P99: 0.05,
			Vus: int32(10 + i), Planned: 100, Dropped: 0, Iterations: 100, SchedLag: 0.001, Snapshot: []byte("{}"),
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := s.RefreshRollups(ctx, t0.Add(-time.Hour), t0.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	ten, err := s.RunRollup(ctx, run, store.Rollup10s)
	if err != nil {
		t.Fatal(err)
	}
	if len(ten) != 3 {
		t.Fatalf("10s buckets: %+v", ten)
	}
	if b := ten[0]; !b.Bucket.Equal(t0) || b.Requests != 1000 || b.Failed != 5 || b.Samples != 10 || b.VUs != 19 || math.Abs(b.P95-0.029) > 1e-9 {
		t.Errorf("first 10s bucket: %+v", b)
	}
	if b := ten[2]; b.Requests != 500 || b.Samples != 5 {
		t.Errorf("last 10s bucket: %+v", b)
	}
	minute, err := s.RunRollup(ctx, run, store.Rollup1m)
	if err != nil {
		t.Fatal(err)
	}
	if len(minute) != 1 || minute[0].Requests != 2500 || minute[0].Failed != 12 || minute[0].VUs != 34 {
		t.Errorf("1m buckets: %+v", minute)
	}
	if _, err := s.RunRollup(ctx, run, "5s"); err == nil {
		t.Error("unknown resolution accepted")
	}

	if _, err := s.SetMetricsRetention(ctx, time.Hour); err == nil {
		t.Error("a retention shorter than a day should be refused")
	}
	managed, err := s.SetMetricsRetention(ctx, 30*24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	cont, err := s.ContinuousRollups(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if managed != cont {
		t.Errorf("retention managed by TimescaleDB = %v, continuous rollups = %v", managed, cont)
	}
	if !managed {
		n, err := s.DeleteMetricsBefore(ctx, t0.Add(20*time.Second))
		if err != nil || n != 20 {
			t.Fatalf("deleted %d, %v", n, err)
		}
		pts, _ := s.ListRunPoints(ctx, run)
		if len(pts) != 5 {
			t.Errorf("points left: %d", len(pts))
		}
	}
	if _, err := s.SetMetricsRetention(ctx, 0); err != nil {
		t.Fatal(err)
	}
}
