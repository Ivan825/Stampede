package report

import (
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/Ivan825/Stampede/internal/scenario"
)

// WriteJSON writes the report as indented JSON.
func (r *Report) WriteJSON(w io.Writer) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(r)
}

// ReadJSON loads a report written by WriteJSON.
func ReadJSON(rd io.Reader) (*Report, error) {
	var r Report
	if err := json.NewDecoder(rd).Decode(&r); err != nil {
		return nil, err
	}
	return &r, nil
}

// Ms formats seconds as milliseconds (or seconds above 10s).
func Ms(s float64) string {
	switch {
	case s >= 10:
		return strconv.FormatFloat(s, 'f', 1, 64) + "s"
	case s >= 1:
		return strconv.FormatFloat(s*1000, 'f', 0, 64) + "ms"
	case s >= 0.01:
		return strconv.FormatFloat(s*1000, 'f', 1, 64) + "ms"
	default:
		return strconv.FormatFloat(s*1000, 'f', 2, 64) + "ms"
	}
}

// Pct formats a fraction as a percentage.
func Pct(f float64) string { return strconv.FormatFloat(f*100, 'f', 2, 64) + "%" }

// Bytes formats a byte count.
func Bytes(n uint64) string {
	const k = 1024.0
	f := float64(n)
	switch {
	case f >= k*k*k:
		return fmt.Sprintf("%.2f GiB", f/(k*k*k))
	case f >= k*k:
		return fmt.Sprintf("%.2f MiB", f/(k*k))
	case f >= k:
		return fmt.Sprintf("%.1f KiB", f/k)
	}
	return fmt.Sprintf("%d B", n)
}

// VerdictLabel is a human label for a verdict.
func VerdictLabel(v string) string {
	switch v {
	case VerdictPass:
		return "PASS"
	case VerdictFail:
		return "FAIL"
	case VerdictGeneratorLimit:
		return "GENERATOR-LIMITED"
	default:
		return "NO TARGETS"
	}
}

// WriteText writes the terminal summary.
func (r *Report) WriteText(w io.Writer) {
	o := r.Overall
	fmt.Fprintf(w, "\n  %s  %s  (%s, %.0fs, stop: %s)\n\n", VerdictLabel(r.Verdict), r.Scenario, r.Load.Executor, r.Duration, r.StopReason)
	fmt.Fprintf(w, "  requests     %d  (%.1f/s)   failed %d (%s)\n", o.Requests, o.RPS, o.Failed, Pct(o.ErrorRate))
	fmt.Fprintf(w, "  iterations   %d  failed %d  dropped %d\n", o.Iterations, o.IterationsFailed, o.Dropped)
	fmt.Fprintf(w, "  latency      p50 %s  p90 %s  p95 %s  p99 %s  max %s\n",
		Ms(o.Latency.P50), Ms(o.Latency.P90), Ms(o.Latency.P95), Ms(o.Latency.P99), Ms(o.Latency.Max))
	if o.Latency.P99 != o.Service.P99 {
		fmt.Fprintf(w, "  service      p50 %s  p95 %s  p99 %s   (from actual send; latency counts from scheduled send)\n",
			Ms(o.Service.P50), Ms(o.Service.P95), Ms(o.Service.P99))
	}
	fmt.Fprintf(w, "  data         in %s  out %s   peak VUs %d\n", Bytes(o.BytesIn), Bytes(o.BytesOut), r.Load.PeakVUs)

	if len(r.Thresholds) > 0 {
		fmt.Fprintf(w, "\n  targets\n")
		for _, c := range r.Thresholds {
			mark := "✓"
			if !c.Pass {
				mark = "✗"
			}
			fmt.Fprintf(w, "    %s %-36s observed %s\n", mark, c.Source, c.ObservedText)
		}
	}
	if r.Breakpoint != nil {
		bp := r.Breakpoint
		if bp.Found {
			fmt.Fprintf(w, "\n  breakpoint   targets held up to %s%s, failed at %s%s (%s)\n",
				num(bp.LastPass), bp.Unit, num(bp.FirstFail), bp.Unit, strings.Join(bp.FailedOn, ", "))
		} else {
			fmt.Fprintf(w, "\n  breakpoint   not reached: targets held at every level up to %s%s\n", num(bp.LastPass), bp.Unit)
		}
	}

	if k := r.Knee; k != nil {
		if k.Found {
			fmt.Fprintf(w, "\n  knee         scaled to %s%s (%.1f it/s, p95 %s); at %s%s %s (%.1f it/s, p95 %s)\n",
				num(k.At.Offered), k.Unit, k.At.Throughput, Ms(k.At.P95), num(k.Next.Offered), k.Unit, k.Reason, k.Next.Throughput, Ms(k.Next.P95))
		} else {
			fmt.Fprintf(w, "\n  knee         none: throughput kept up with load up to %s%s\n", num(k.At.Offered), k.Unit)
		}
	}

	fmt.Fprintf(w, "\n  %-44s %8s %8s %9s %9s %9s\n", "step", "reqs", "errors", "p50", "p95", "p99")
	for _, j := range r.Journeys {
		for _, s := range j.Steps {
			name := j.Name + " › " + s.Name
			if len(name) > 44 {
				name = name[:43] + "…"
			}
			fmt.Fprintf(w, "  %-44s %8d %8s %9s %9s %9s\n", name, s.Stats.Requests, Pct(s.Stats.ErrorRate),
				Ms(s.Stats.Latency.P50), Ms(s.Stats.Latency.P95), Ms(s.Stats.Latency.P99))
		}
	}
	r.writeStreams(w)
	r.writeSlowest(w)
	r.writeTargetMetrics(w)
	if len(r.Errors) > 0 {
		fmt.Fprintf(w, "\n  top errors\n")
		for i, e := range r.Errors {
			if i == 8 {
				break
			}
			fmt.Fprintf(w, "    %6d  %-28s %s › %s\n", e.Count, e.Error, e.Journey, e.Step)
		}
	}
	if len(r.Faults) > 0 {
		fmt.Fprintf(w, "\n  injected faults\n")
		for _, f := range r.Faults {
			res := "applied, then reverted"
			if f.Error != "" {
				res = f.Error
			}
			fmt.Fprintf(w, "    %6s-%-6s %-34s %s\n", fmtSecs(f.Start), fmtSecs(f.End), f.Label, res)
		}
	}
	for _, n := range r.Notes {
		fmt.Fprintf(w, "\n  note: %s\n", n)
	}
	r.WriteNarrativeText(w)
	fmt.Fprintln(w)
}

// writeStreams lists time to first event and event rate for streaming
// steps, the figures that matter for LLM APIs (time to first token,
// tokens per second).
func (r *Report) writeStreams(w io.Writer) {
	header := false
	for _, j := range r.Journeys {
		for _, s := range j.Steps {
			if s.Stream == nil {
				continue
			}
			if !header {
				fmt.Fprintf(w, "\n  %-44s %8s %9s %9s %9s %10s\n", "stream", "events", "first p50", "first p95", "first p99", "events/s")
				header = true
			}
			name := j.Name + " › " + s.Name
			if len(name) > 44 {
				name = name[:43] + "…"
			}
			st := s.Stream
			fmt.Fprintf(w, "  %-44s %8d %9s %9s %9s %10.1f\n", name, st.Events,
				Ms(st.FirstEvent.P50), Ms(st.FirstEvent.P95), Ms(st.FirstEvent.P99), st.EventsPerSec)
		}
	}
}

// writeSlowest lists the slowest requests of the run with their trace
// IDs (or links), so a slow request can be found in the target's traces.
func (r *Report) writeSlowest(w io.Writer) {
	rows := r.SlowestOverall(5)
	if len(rows) == 0 {
		return
	}
	fmt.Fprintf(w, "\n  slowest requests\n")
	for _, s := range rows {
		name := s.Journey + " › " + s.Step
		if len(name) > 36 {
			name = name[:35] + "…"
		}
		status := "-"
		if s.Status > 0 {
			status = strconv.Itoa(s.Status)
		}
		if s.Error != "" {
			status = s.Error
		}
		trace := s.TraceURL
		if trace == "" && s.TraceID != "" {
			trace = "trace " + s.TraceID
		}
		fmt.Fprintf(w, "    %9s  %-36s t+%-7s %-6s %s\n", Ms(s.Latency), name, fmtOffset(s.T), status, trace)
	}
}

// writeTargetMetrics summarises the target's own metrics.
func (r *Report) writeTargetMetrics(w io.Writer) {
	if len(r.TargetMetrics) == 0 {
		return
	}
	fmt.Fprintf(w, "\n  %-44s %10s %10s %10s\n", "target metrics (Prometheus)", "min", "max", "last")
	for _, m := range r.TargetMetrics {
		name := m.Name
		if len(name) > 42 {
			name = name[:41] + "…"
		}
		lo, hi, last, ok := m.Range()
		switch {
		case ok:
			fmt.Fprintf(w, "    %-42s %10s %10s %10s\n", name, MetricValue(lo), MetricValue(hi), MetricValue(last))
		default:
			fmt.Fprintf(w, "    %-42s no data\n", name)
		}
		if m.Error != "" {
			fmt.Fprintf(w, "      %s\n", m.Error)
		}
	}
}

// fmtOffset formats seconds since the start with a tenth of a second
// under two minutes.
func fmtOffset(s float64) string {
	if s < 120 {
		return strconv.FormatFloat(s, 'f', 1, 64) + "s"
	}
	return fmtSecs(s)
}

// SlowRow is a slow request with the step it belongs to.
type SlowRow struct {
	Journey string
	Step    string
	SlowRequest
}

// SlowestOverall returns up to n of the run's slowest requests across all
// steps, slowest first.
func (r *Report) SlowestOverall(n int) []SlowRow {
	rows := r.slowRows()
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].Latency > rows[j].Latency })
	if len(rows) > n {
		rows = rows[:n]
	}
	return rows
}

func (r *Report) slowRows() []SlowRow {
	var rows []SlowRow
	for _, j := range r.Journeys {
		for _, s := range j.Steps {
			for _, sl := range s.Slowest {
				rows = append(rows, SlowRow{Journey: j.Name, Step: s.Name, SlowRequest: sl})
			}
		}
	}
	return rows
}

// MetricValue formats a target metric value compactly: 0.0123, 12.3,
// 4.56k, 120M.
func MetricValue(f float64) string {
	a := math.Abs(f)
	switch {
	case a == 0:
		return "0"
	case a >= 1e12:
		return trimFloat(f/1e12) + "T"
	case a >= 1e9:
		return trimFloat(f/1e9) + "G"
	case a >= 1e6:
		return trimFloat(f/1e6) + "M"
	case a >= 1e4:
		return trimFloat(f/1e3) + "k"
	case a >= 0.001:
		return trimFloat(f)
	default:
		return strconv.FormatFloat(f, 'e', 2, 64)
	}
}

// trimFloat prints three significant digits without trailing zeros.
func trimFloat(f float64) string {
	s := strconv.FormatFloat(f, 'f', max(0, 2-int(math.Floor(math.Log10(math.Abs(f))))), 64)
	if strings.Contains(s, ".") {
		s = strings.TrimRight(strings.TrimRight(s, "0"), ".")
	}
	return s
}

func num(f float64) string {
	if f == float64(int64(f)) {
		return strconv.FormatInt(int64(f), 10)
	}
	return strconv.FormatFloat(f, 'f', 1, 64)
}

// WriteMarkdown writes a summary suitable for a pull request comment.
func (r *Report) WriteMarkdown(w io.Writer) {
	icon := map[string]string{VerdictPass: "✅", VerdictFail: "❌", VerdictGeneratorLimit: "⚠️", VerdictNoTargets: "ℹ️"}[r.Verdict]
	o := r.Overall
	fmt.Fprintf(w, "### %s Stampede: %s — %s\n\n", icon, r.Scenario, VerdictLabel(r.Verdict))
	fmt.Fprintf(w, "%d requests at %.1f/s over %.0fs · errors %s · p95 %s · p99 %s\n\n",
		o.Requests, o.RPS, r.Duration, Pct(o.ErrorRate), Ms(o.Latency.P95), Ms(o.Latency.P99))
	if n := r.Narrative; n != nil {
		fmt.Fprintf(w, "%s\n\n", n.Summary)
		for _, c := range n.Claims {
			fmt.Fprintf(w, "- **%s** %s <sub>%s</sub>\n", c.Label, c.Text, strings.Join(c.Refs, ", "))
		}
		fmt.Fprintf(w, "\n<sub>Summary written by %s from this report's figures.</sub>\n\n", n.Model)
	}
	if len(r.Thresholds) > 0 {
		fmt.Fprintf(w, "| Target | Observed | Result |\n|---|---|---|\n")
		for _, c := range r.Thresholds {
			res := "pass"
			if !c.Pass {
				res = "**fail**"
			}
			fmt.Fprintf(w, "| `%s` | %s | %s |\n", c.Source, c.ObservedText, res)
		}
		fmt.Fprintln(w)
	}
	if len(r.Faults) > 0 {
		fmt.Fprintf(w, "**Injected faults:** ")
		for i, f := range r.Faults {
			if i > 0 {
				fmt.Fprint(w, "; ")
			}
			fmt.Fprintf(w, "%s (%s–%s)", f.Label, fmtSecs(f.Start), fmtSecs(f.End))
			if f.Error != "" {
				fmt.Fprintf(w, " **failed: %s**", f.Error)
			}
		}
		fmt.Fprint(w, "\n\n")
	}
	fmt.Fprintf(w, "<details><summary>Per step</summary>\n\n| Journey | Step | Requests | Errors | p95 | p99 |\n|---|---|---:|---:|---:|---:|\n")
	for _, j := range r.Journeys {
		for _, s := range j.Steps {
			fmt.Fprintf(w, "| %s | `%s` | %d | %s | %s | %s |\n", j.Name, s.Name, s.Stats.Requests, Pct(s.Stats.ErrorRate), Ms(s.Stats.Latency.P95), Ms(s.Stats.Latency.P99))
		}
	}
	fmt.Fprintf(w, "\n</details>\n")
}

type junitSuites struct {
	XMLName  xml.Name     `xml:"testsuites"`
	Name     string       `xml:"name,attr"`
	Tests    int          `xml:"tests,attr"`
	Failures int          `xml:"failures,attr"`
	Time     float64      `xml:"time,attr"`
	Suites   []junitSuite `xml:"testsuite"`
}

type junitSuite struct {
	Name     string      `xml:"name,attr"`
	Tests    int         `xml:"tests,attr"`
	Failures int         `xml:"failures,attr"`
	Time     float64     `xml:"time,attr"`
	Cases    []junitCase `xml:"testcase"`
}

type junitCase struct {
	Name      string        `xml:"name,attr"`
	Classname string        `xml:"classname,attr"`
	Failure   *junitFailure `xml:"failure,omitempty"`
}

type junitFailure struct {
	Message string `xml:"message,attr"`
	Text    string `xml:",chardata"`
}

// WriteJUnit writes one test case per target, for CI systems.
func (r *Report) WriteJUnit(w io.Writer) error {
	s := junitSuite{Name: "stampede." + r.Scenario, Time: r.Duration}
	checks := append([]Check(nil), r.Thresholds...)
	sort.SliceStable(checks, func(i, j int) bool { return checks[i].Scope < checks[j].Scope })
	for _, c := range checks {
		tc := junitCase{Name: c.Source, Classname: "stampede." + r.Scenario + "." + c.Scope}
		if !c.Pass {
			tc.Failure = &junitFailure{
				Message: fmt.Sprintf("%s: observed %s, target %s %s", c.Source, c.ObservedText, c.Op, c.TargetText),
				Text:    fmt.Sprintf("metric %s for %s", c.Metric, c.Scope),
			}
			s.Failures++
		}
		s.Cases = append(s.Cases, tc)
		s.Tests++
	}
	if s.Tests == 0 {
		s.Cases = append(s.Cases, junitCase{Name: "run completed", Classname: "stampede." + r.Scenario})
		s.Tests = 1
	}
	doc := junitSuites{Name: "stampede", Tests: s.Tests, Failures: s.Failures, Time: s.Time, Suites: []junitSuite{s}}
	if _, err := io.WriteString(w, xml.Header); err != nil {
		return err
	}
	enc := xml.NewEncoder(w)
	enc.Indent("", "  ")
	if err := enc.Encode(doc); err != nil {
		return err
	}
	_, err := io.WriteString(w, "\n")
	return err
}

// Unit returns the load unit for the plan mode.
func Unit(mode string) string {
	if mode == scenario.ModeRate {
		return "/s"
	}
	return " VUs"
}
