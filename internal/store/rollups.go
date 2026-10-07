package store

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// Rollup resolutions of the per-second run metrics (migration 00011).
const (
	Rollup10s = "10s"
	Rollup1m  = "1m"
)

var rollupViews = map[string]string{Rollup10s: "run_metrics_10s", Rollup1m: "run_metrics_1m"}

// RollupWidth is a resolution's bucket width.
func RollupWidth(res string) time.Duration {
	if res == Rollup1m {
		return time.Minute
	}
	return 10 * time.Second
}

// RollupPoint is one bucket of a run's rolled-up metrics. P50 is the mean
// of the per-second medians; P95 and P99 are the worst per-second values.
type RollupPoint struct {
	Bucket                       time.Time
	Requests, Failed             int64
	RPS, P50, P95, P99           float64
	VUs                          int
	Planned                      float64
	Dropped, Iterations, Samples int64
	SchedLag                     float64
}

// RunRollup returns a run's metrics in buckets of res (Rollup10s or
// Rollup1m), oldest first.
func (s *Store) RunRollup(ctx context.Context, run uuid.UUID, res string) ([]RollupPoint, error) {
	view, ok := rollupViews[res]
	if !ok {
		return nil, fmt.Errorf("unknown resolution %q", res)
	}
	rows, err := s.Pool.Query(ctx, `SELECT bucket, requests, failed, rps, p50, p95, p99, vus, planned, dropped, iterations, sched_lag, samples
		FROM `+view+` WHERE run_id = $1 ORDER BY bucket`, run)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []RollupPoint{}
	for rows.Next() {
		var p RollupPoint
		var vus int32
		if err := rows.Scan(&p.Bucket, &p.Requests, &p.Failed, &p.RPS, &p.P50, &p.P95, &p.P99, &vus, &p.Planned, &p.Dropped, &p.Iterations, &p.SchedLag, &p.Samples); err != nil {
			return nil, err
		}
		p.VUs = int(vus)
		out = append(out, p)
	}
	return out, rows.Err()
}

// ContinuousRollups reports whether the rollups are TimescaleDB continuous
// aggregates (kept after per-second rows expire) rather than plain views.
func (s *Store) ContinuousRollups(ctx context.Context) (bool, error) {
	var ext bool
	if err := s.Pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_extension WHERE extname = 'timescaledb')`).Scan(&ext); err != nil || !ext {
		return false, err
	}
	var n int
	err := s.Pool.QueryRow(ctx, `SELECT count(*) FROM timescaledb_information.continuous_aggregates WHERE view_name IN ('run_metrics_10s', 'run_metrics_1m')`).Scan(&n)
	return n == 2, err
}

// RefreshRollups brings continuous aggregates up to date for a time range
// (the refresh policy does this every minute; tests and backfills call it
// directly). It does nothing on plain PostgreSQL.
func (s *Store) RefreshRollups(ctx context.Context, from, to time.Time) error {
	cont, err := s.ContinuousRollups(ctx)
	if err != nil || !cont {
		return err
	}
	for _, v := range []string{"run_metrics_10s", "run_metrics_1m"} {
		if _, err := s.Pool.Exec(ctx, `CALL refresh_continuous_aggregate('`+v+`', $1::timestamptz, $2::timestamptz)`, from, to); err != nil {
			return err
		}
	}
	return nil
}

// MinMetricsRetention is the shortest retention accepted: the continuous
// aggregates refresh the last day, so per-second rows must outlive it.
const MinMetricsRetention = 24 * time.Hour

// SetMetricsRetention keeps per-second run metrics for d (0 keeps them
// forever). With TimescaleDB a retention policy drops whole chunks and
// managed is true; on plain PostgreSQL the caller deletes old rows with
// DeleteMetricsBefore, periodically.
func (s *Store) SetMetricsRetention(ctx context.Context, d time.Duration) (managed bool, err error) {
	if d > 0 && d < MinMetricsRetention {
		return false, fmt.Errorf("metrics retention must be at least %s", MinMetricsRetention)
	}
	var hyper bool
	if err := s.Pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_extension WHERE extname = 'timescaledb')`).Scan(&hyper); err != nil {
		return false, err
	}
	if hyper {
		if err := s.Pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM timescaledb_information.hypertables WHERE hypertable_name = 'run_metrics')`).Scan(&hyper); err != nil {
			return false, err
		}
	}
	if !hyper {
		return false, nil
	}
	if _, err := s.Pool.Exec(ctx, `SELECT remove_retention_policy('run_metrics', if_exists => true)`); err != nil {
		return false, err
	}
	if d > 0 {
		if _, err := s.Pool.Exec(ctx, `SELECT add_retention_policy('run_metrics', drop_after => $1::interval)`, fmt.Sprintf("%d seconds", int64(d/time.Second))); err != nil {
			return false, err
		}
	}
	return true, nil
}

// DeleteMetricsBefore deletes per-second run metrics older than t, in
// batches, and returns how many rows went.
func (s *Store) DeleteMetricsBefore(ctx context.Context, t time.Time) (int64, error) {
	var total int64
	for {
		tag, err := s.Pool.Exec(ctx, `DELETE FROM run_metrics WHERE ctid IN (SELECT ctid FROM run_metrics WHERE ts < $1 LIMIT 10000)`, t)
		if err != nil {
			return total, err
		}
		total += tag.RowsAffected()
		if tag.RowsAffected() < 10000 || ctx.Err() != nil {
			return total, ctx.Err()
		}
	}
}
