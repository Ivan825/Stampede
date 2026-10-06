package report

import (
	"fmt"
	"html/template"
	"io"
	"strings"
)

// Narrative is a written summary of a report. Every claim cites the report
// facts it rests on and says whether those facts show it (measured) or
// only suggest it (suspected).
type Narrative struct {
	Summary string  `json:"summary"`
	Claims  []Claim `json:"claims"`
	// Model is the model that wrote it, for the record.
	Model string `json:"model,omitempty"`
	// Facts are the report figures the claims cite.
	Facts []Fact `json:"facts,omitempty"`
}

// KeepCited sets Facts to the facts the claims cite, in report order.
func (n *Narrative) KeepCited(facts []Fact) {
	cited := map[string]bool{}
	for _, c := range n.Claims {
		for _, r := range c.Refs {
			cited[r] = true
		}
	}
	n.Facts = n.Facts[:0]
	for _, f := range facts {
		if cited[f.ID] {
			n.Facts = append(n.Facts, f)
		}
	}
}

// Claim is one statement in a narrative.
type Claim struct {
	Text  string   `json:"text"`
	Label string   `json:"label"` // measured or suspected
	Refs  []string `json:"refs"`  // fact ids, see Facts
}

// Fact is one figure from a report, with a stable id a narrative cites.
type Fact struct {
	ID    string `json:"id"`
	Text  string `json:"text"`
	Where string `json:"where"` // the report section that shows it
}

// Facts lists the figures a narrative may cite. Ids are stable for a given
// report, so a claim's citations can be checked and linked.
func (r *Report) Facts() []Fact {
	var fs []Fact
	add := func(id, where, format string, args ...any) {
		fs = append(fs, Fact{ID: id, Where: where, Text: fmt.Sprintf(format, args...)})
	}
	o := r.Overall
	add("verdict", "summary", "verdict %s; stop reason %s; %.0fs of load", VerdictLabel(r.Verdict), r.StopReason, r.Duration)
	add("overall.requests", "summary", "%d requests at %.1f/s, %s failed", o.Requests, o.RPS, Pct(o.ErrorRate))
	add("overall.latency", "summary", "latency from scheduled send p50 %s, p95 %s, p99 %s, max %s", Ms(o.Latency.P50), Ms(o.Latency.P95), Ms(o.Latency.P99), Ms(o.Latency.Max))
	add("overall.service", "summary", "service time from actual send p50 %s, p95 %s, p99 %s", Ms(o.Service.P50), Ms(o.Service.P95), Ms(o.Service.P99))
	add("overall.iterations", "summary", "%d iterations, %d failed, %d dropped, peak %d users", o.Iterations, o.IterationsFailed, o.Dropped, r.Load.PeakVUs)
	for i, c := range r.Thresholds {
		status := "passed"
		if !c.Pass {
			status = "failed"
		}
		add(fmt.Sprintf("target.%d", i), "targets", "target %s %s (observed %s)", c.Source, status, c.ObservedText)
	}
	if bp := r.Breakpoint; bp != nil {
		if bp.Found {
			add("breakpoint", "breakpoint", "targets held up to %s%s and failed at %s%s on %s", num(bp.LastPass), bp.Unit, num(bp.FirstFail), bp.Unit, strings.Join(bp.FailedOn, ", "))
		} else {
			add("breakpoint", "breakpoint", "targets held at every level up to %s%s", num(bp.LastPass), bp.Unit)
		}
	}
	if k := r.Knee; k != nil && k.Found && k.Next != nil {
		add("knee", "throughput against load", "throughput scaled to %s%s (%.1f it/s, p95 %s); at %s%s %s (%.1f it/s, p95 %s)",
			num(k.At.Offered), k.Unit, k.At.Throughput, Ms(k.At.P95), num(k.Next.Offered), k.Unit, k.Reason, k.Next.Throughput, Ms(k.Next.P95))
	}
	for _, j := range r.Journeys {
		for _, s := range j.Steps {
			id := "step." + j.Name + "/" + s.Name
			add(id, "journeys and steps", "%s › %s: %d requests, %s errors, p95 %s, p99 %s, mean wait %s, mean connect %s",
				j.Name, s.Name, s.Stats.Requests, Pct(s.Stats.ErrorRate), Ms(s.Stats.Latency.P95), Ms(s.Stats.Latency.P99),
				Ms(s.Phases["wait"].Mean), Ms(s.Phases["connect"].Mean))
		}
	}
	for i, e := range r.Errors {
		if i == 10 {
			break
		}
		add(fmt.Sprintf("error.%d", i), "errors", "%d × %s at %s › %s", e.Count, e.Error, e.Journey, e.Step)
	}
	for i, n := range r.Notes {
		add(fmt.Sprintf("note.%d", i), "notes", "%s", n)
	}
	return fs
}

// Check drops claims that cite unknown facts or carry an unknown label,
// and claims labelled measured that cite nothing. It returns how many
// claims were dropped.
func (n *Narrative) Check(facts []Fact) int {
	known := map[string]bool{}
	for _, f := range facts {
		known[f.ID] = true
	}
	kept := n.Claims[:0]
	for _, c := range n.Claims {
		ok := c.Label == "measured" || c.Label == "suspected"
		for _, r := range c.Refs {
			ok = ok && known[r]
		}
		if c.Label == "measured" && len(c.Refs) == 0 {
			ok = false
		}
		if ok && strings.TrimSpace(c.Text) != "" {
			kept = append(kept, c)
		}
	}
	dropped := len(n.Claims) - len(kept)
	n.Claims = kept
	return dropped
}

// WriteNarrativeText writes the narrative for the terminal.
func (r *Report) WriteNarrativeText(w io.Writer) {
	n := r.Narrative
	if n == nil {
		return
	}
	fmt.Fprintf(w, "\n  summary (written by %s; every claim cites the report)\n  %s\n", n.Model, n.Summary)
	for _, c := range n.Claims {
		fmt.Fprintf(w, "    [%s] %s  (%s)\n", c.Label, c.Text, strings.Join(c.Refs, ", "))
	}
}

func narrativeHTML(n *Narrative, facts []Fact) template.HTML {
	if n == nil {
		return ""
	}
	byID := map[string]Fact{}
	for _, f := range facts {
		byID[f.ID] = f
	}
	var b strings.Builder
	fmt.Fprintf(&b, `<p>%s</p><ul class="claims">`, template.HTMLEscapeString(n.Summary))
	for _, c := range n.Claims {
		var refs []string
		for _, id := range c.Refs {
			f := byID[id]
			refs = append(refs, fmt.Sprintf(`<span class="ref" title="%s">%s</span>`, template.HTMLEscapeString(f.Text), template.HTMLEscapeString(f.Where)))
		}
		fmt.Fprintf(&b, `<li><span class="label %s">%s</span> %s <span class="refs">%s</span></li>`,
			template.HTMLEscapeString(c.Label), template.HTMLEscapeString(c.Label), template.HTMLEscapeString(c.Text), strings.Join(refs, " "))
	}
	fmt.Fprintf(&b, `</ul><p class="by">Written by %s from this report's figures; hover a citation to see the figure.</p>`, template.HTMLEscapeString(n.Model))
	return template.HTML(b.String()) //nolint:gosec // every value is escaped above
}
