package report

import (
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
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
	if len(r.Errors) > 0 {
		fmt.Fprintf(w, "\n  top errors\n")
		for i, e := range r.Errors {
			if i == 8 {
				break
			}
			fmt.Fprintf(w, "    %6d  %-28s %s › %s\n", e.Count, e.Error, e.Journey, e.Step)
		}
	}
	for _, n := range r.Notes {
		fmt.Fprintf(w, "\n  note: %s\n", n)
	}
	fmt.Fprintln(w)
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
