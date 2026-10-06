package report

import (
	_ "embed"
	"fmt"
	"html/template"
	"io"
	"math"
	"strconv"
	"time"
)

//go:embed report.html.tmpl
var htmlTemplate string

var htmlTmpl = template.Must(template.New("report").Funcs(template.FuncMap{
	"ms":      Ms,
	"pct":     Pct,
	"bytes":   Bytes,
	"verdict": VerdictLabel,
	"num":     num,
	"rate":    func(f float64) string { return strconv.FormatFloat(f, 'f', 1, 64) },
	"when":    func(t time.Time) string { return t.Format("2 Jan 2006 15:04:05 MST") },
	"secs":    func(f float64) string { return fmtSecs(f) },
	"offset":  fmtOffset,
}).Parse(htmlTemplate))

type htmlData struct {
	*Report
	ThroughputChart template.HTML
	LatencyChart    template.HTML
	ErrorChart      template.HTML
	CurveChart      template.HTML
	Unit            string
	// HasStreams shows the streams table.
	HasStreams bool
	// SlowRows lists every step's slowest requests.
	SlowRows []SlowRow
	// MetricCharts are the target's own metrics (observe.prometheus).
	MetricCharts []metricChart
}

type metricChart struct {
	Name, Query, Error string
	Min, Max, Last     string
	Chart              template.HTML
}

// WriteHTML writes a self-contained HTML report with inline SVG charts and
// no external requests, so it can be archived or attached to CI runs.
func (r *Report) WriteHTML(w io.Writer) error {
	xs := make([]float64, len(r.Timeline))
	rps := make([]float64, len(r.Timeline))
	vus := make([]float64, len(r.Timeline))
	planned := make([]float64, len(r.Timeline))
	p50 := make([]float64, len(r.Timeline))
	p95 := make([]float64, len(r.Timeline))
	p99 := make([]float64, len(r.Timeline))
	errs := make([]float64, len(r.Timeline))
	for i, p := range r.Timeline {
		xs[i] = p.T + 1
		rps[i], vus[i], planned[i] = p.RPS, float64(p.VUs), p.Planned
		if p.RPS == 0 {
			p50[i], p95[i], p99[i], errs[i] = math.NaN(), math.NaN(), math.NaN(), math.NaN()
			continue
		}
		p50[i], p95[i], p99[i], errs[i] = p.P50, p.P95, p.P99, p.ErrorRate
	}
	rateFmt := func(f float64) string { return strconv.FormatFloat(f, 'f', 0, 64) + "/s" }
	countFmt := func(f float64) string { return strconv.FormatFloat(f, 'f', 0, 64) }
	load := []series{{Name: "requests per second", Class: "s1", Ys: rps, Fmt: rateFmt}}
	if r.Load.Mode == "rate" {
		load = append(load, series{Name: "planned iterations per second", Class: "s4", Ys: planned, Fmt: rateFmt})
	}
	load = append(load, series{Name: "active virtual users", Class: "s2", Ys: vus, Fmt: countFmt, Right: true})

	d := htmlData{
		Report:          r,
		Unit:            Unit(r.Load.Mode),
		ThroughputChart: lineChart("Throughput", xs, load),
		LatencyChart: lineChart("Latency", xs, []series{
			{Name: "p50", Class: "s3", Ys: p50, Fmt: Ms},
			{Name: "p95", Class: "s1", Ys: p95, Fmt: Ms},
			{Name: "p99", Class: "s5", Ys: p99, Fmt: Ms},
		}),
		ErrorChart: lineChart("Errors", xs, []series{
			{Name: "error rate", Class: "s5", Ys: errs, Fmt: func(f float64) string { return fmt.Sprintf("%.1f%%", f*100) }},
		}),
	}
	if len(r.Curve) >= 2 {
		cx := make([]float64, len(r.Curve))
		thr := make([]float64, len(r.Curve))
		cp95 := make([]float64, len(r.Curve))
		for i, c := range r.Curve {
			cx[i], thr[i], cp95[i] = c.Offered, c.Throughput, c.P95
		}
		unit := Unit(r.Load.Mode)
		d.CurveChart = lineChartX("Throughput against load", cx, func(f float64) string { return num(math.Round(f)) + unit }, []series{
			{Name: "completed iterations per second", Class: "s1", Ys: thr, Fmt: rateFmt},
			{Name: "p95 latency", Class: "s5", Ys: cp95, Fmt: Ms, Right: true},
		})
	}
	for _, j := range r.Journeys {
		for _, s := range j.Steps {
			d.HasStreams = d.HasStreams || s.Stream != nil
		}
	}
	d.SlowRows = r.slowRows()
	for _, m := range r.TargetMetrics {
		mc := metricChart{Name: m.Name, Query: m.Query, Error: m.Error}
		if lo, hi, last, ok := m.Range(); ok {
			mc.Min, mc.Max, mc.Last = MetricValue(lo), MetricValue(hi), MetricValue(last)
			mx := make([]float64, len(m.Points))
			my := make([]float64, len(m.Points))
			for i, p := range m.Points {
				mx[i], my[i] = p.T, p.Value
			}
			mc.Chart = lineChart(m.Name, mx, []series{{Name: m.Name, Class: "s2", Ys: my, Fmt: MetricValue}})
		}
		d.MetricCharts = append(d.MetricCharts, mc)
	}
	return htmlTmpl.Execute(w, d)
}
