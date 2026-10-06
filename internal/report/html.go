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
}).Parse(htmlTemplate))

type htmlData struct {
	*Report
	ThroughputChart template.HTML
	LatencyChart    template.HTML
	ErrorChart      template.HTML
	Unit            string
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
	return htmlTmpl.Execute(w, d)
}
