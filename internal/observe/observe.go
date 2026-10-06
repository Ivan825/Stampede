package observe

import (
	"context"
	"time"

	"github.com/Ivan825/Stampede/internal/report"
	"github.com/Ivan825/Stampede/internal/scenario"
)

// Config is an observe block with its URLs and credentials resolved: from
// the scenario for stampede run, from named integrations on the server.
type Config struct {
	// Prometheus is nil when no metrics are queried.
	Prometheus *Prometheus
	Queries    []Query
	// TraceURL is a link template containing {traceId}; empty for none.
	TraceURL string
}

// Empty reports whether there is nothing to do.
func (c *Config) Empty() bool {
	return c == nil || (c.Prometheus == nil && c.TraceURL == "")
}

// Apply adds the target's metrics over the run and trace links for the
// slowest requests to a finished run's report.
func (c *Config) Apply(ctx context.Context, rep *report.Report, interval time.Duration) {
	if c.Empty() {
		return
	}
	if c.TraceURL != "" {
		rep.SetTraceLinks(c.TraceURL)
	}
	if c.Prometheus != nil && len(c.Queries) > 0 {
		end := rep.Ended
		if end.Before(rep.Started) || end.IsZero() {
			end = rep.Started.Add(time.Duration(rep.Duration * float64(time.Second)))
		}
		rep.TargetMetrics = Collect(ctx, c.Prometheus, c.Queries, rep.Started, end, interval)
	}
}

// QueriesOf lists a scenario's queries in name order.
func QueriesOf(p *scenario.PrometheusObserve) []Query {
	if p == nil {
		return nil
	}
	out := make([]Query, 0, len(p.Queries))
	for _, name := range p.QueryNames() {
		out = append(out, Query{Name: name, Expr: p.Queries[name]})
	}
	return out
}
