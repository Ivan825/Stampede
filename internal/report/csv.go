package report

import (
	"encoding/csv"
	"io"
	"strconv"
)

// CSV tables: one row per step (with a row per journey and one for the
// whole run), or one row per second of the timeline. Latencies are in
// milliseconds, rates in requests per second, fractions as 0..1.

var csvStepHeader = []string{
	"journey", "step", "requests", "failed", "error_rate", "rps",
	"p50_ms", "p90_ms", "p95_ms", "p99_ms", "p999_ms", "max_ms", "mean_ms",
	"service_p95_ms", "service_p99_ms", "checks_passed", "checks_failed", "bytes_in", "bytes_out",
}

// WriteCSV writes the per-step table. Journey totals have an empty step
// and the run's totals have an empty journey and step.
func (r *Report) WriteCSV(w io.Writer) error {
	cw := csv.NewWriter(w)
	if err := cw.Write(csvStepHeader); err != nil {
		return err
	}
	row := func(journey, step string, s Stats) error {
		return cw.Write([]string{
			journey, step, csvUint(s.Requests), csvUint(s.Failed), csvNum(s.ErrorRate, 6), csvNum(s.RPS, 3),
			csvMs(s.Latency.P50), csvMs(s.Latency.P90), csvMs(s.Latency.P95), csvMs(s.Latency.P99), csvMs(s.Latency.P999), csvMs(s.Latency.Max), csvMs(s.Latency.Mean),
			csvMs(s.Service.P95), csvMs(s.Service.P99), csvUint(s.ChecksPassed), csvUint(s.ChecksFailed), csvUint(s.BytesIn), csvUint(s.BytesOut),
		})
	}
	for _, j := range r.Journeys {
		for _, st := range j.Steps {
			if err := row(j.Name, st.Name, st.Stats); err != nil {
				return err
			}
		}
		if err := row(j.Name, "", j.Stats); err != nil {
			return err
		}
	}
	if err := row("", "", r.Overall); err != nil {
		return err
	}
	cw.Flush()
	return cw.Error()
}

// WriteTimelineCSV writes one row per second of the run.
func (r *Report) WriteTimelineCSV(w io.Writer) error {
	cw := csv.NewWriter(w)
	if err := cw.Write([]string{"t_s", "rps", "planned", "error_rate", "p50_ms", "p95_ms", "p99_ms", "vus", "iterations", "dropped", "sched_lag_p99_ms"}); err != nil {
		return err
	}
	for _, p := range r.Timeline {
		if err := cw.Write([]string{
			csvNum(p.T, 0), csvNum(p.RPS, 3), csvNum(p.Planned, 3), csvNum(p.ErrorRate, 6), csvMs(p.P50), csvMs(p.P95), csvMs(p.P99),
			strconv.Itoa(p.VUs), csvUint(p.Iterations), csvUint(p.Dropped), csvMs(p.SchedLag99),
		}); err != nil {
			return err
		}
	}
	cw.Flush()
	return cw.Error()
}

func csvUint(n uint64) string           { return strconv.FormatUint(n, 10) }
func csvNum(x float64, prec int) string { return strconv.FormatFloat(x, 'f', prec, 64) }
func csvMs(seconds float64) string      { return strconv.FormatFloat(seconds*1000, 'f', 3, 64) }
