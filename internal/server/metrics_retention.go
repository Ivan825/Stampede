package server

import (
	"context"
	"time"

	"github.com/Ivan825/Stampede/internal/api/gen"
	"github.com/Ivan825/Stampede/internal/store"
	"github.com/Ivan825/Stampede/internal/store/db"
)

// rolledUpTimeline serves GET /runs/{id}/timeline?resolution=10s|1m from
// the run_metrics_10s and run_metrics_1m rollups. Times are seconds since
// the run started, like the per-second timeline.
func (h *handlers) rolledUpTimeline(ctx context.Context, r db.GetRunRow, res string) (gen.GetRunTimelineResponseObject, error) {
	if res != store.Rollup10s && res != store.Rollup1m {
		return nil, errInvalid("resolution must be 1s, 10s or 1m")
	}
	rows, err := h.st.RunRollup(ctx, r.ID, res)
	if err != nil {
		return nil, err
	}
	out := gen.GetRunTimeline200JSONResponse{}
	for _, b := range rows {
		// t counts from the first bucket, as the per-second timeline
		// counts from the first second.
		p := gen.Point{
			T: b.Bucket.Sub(rows[0].Bucket).Seconds(), Rps: b.RPS,
			P50: b.P50, P95: b.P95, P99: b.P99, Vus: b.VUs, Planned: b.Planned, Dropped: int(b.Dropped),
			Iterations: ptr(int(b.Iterations)), SchedLagP99: ptr(b.SchedLag),
		}
		if b.Requests > 0 {
			p.ErrorRate = float64(b.Failed) / float64(b.Requests)
		}
		out = append(out, p)
	}
	return out, nil
}

// MetricsRetentionInterval is how often per-second metrics past the
// retention are deleted on plain PostgreSQL.
const MetricsRetentionInterval = time.Hour

// StartMetricsRetention keeps per-second run metrics for d (0 keeps them
// forever) and returns at once. With TimescaleDB it sets a retention
// policy that drops old chunks; the 10s and 1m rollups and every report
// stay. On plain PostgreSQL it deletes old rows every hour until ctx ends;
// the rollups there are views, so they lose those rows too.
func (s *Server) StartMetricsRetention(ctx context.Context, d time.Duration) error {
	managed, err := s.st.SetMetricsRetention(ctx, d)
	if err != nil {
		return err
	}
	switch {
	case d == 0:
		s.log.Info("per-second run metrics are kept forever")
		return nil
	case managed:
		s.log.Info("per-second run metrics expire through a TimescaleDB retention policy", "after", d)
		return nil
	}
	s.log.Info("per-second run metrics are deleted when older than the retention", "after", d)
	go func() {
		t := time.NewTicker(MetricsRetentionInterval)
		defer t.Stop()
		for {
			n, err := s.st.DeleteMetricsBefore(ctx, s.cfg.Now().Add(-d))
			switch {
			case err != nil && ctx.Err() == nil:
				s.log.Error("delete expired run metrics", "error", err)
			case n > 0:
				s.log.Info("deleted expired run metrics", "rows", n)
			}
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
		}
	}()
	return nil
}
