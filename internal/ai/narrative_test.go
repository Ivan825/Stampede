package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Ivan825/Stampede/internal/ai/provider"
	"github.com/Ivan825/Stampede/internal/metrics"
	"github.com/Ivan825/Stampede/internal/report"
)

func sampleReport() *report.Report {
	return &report.Report{
		Scenario: "shop", Verdict: "fail", StopReason: "completed", Duration: 120,
		Overall: report.Stats{
			Requests: 12000, RPS: 100, ErrorRate: 0.012,
			Latency: metrics.Percentiles{P50: 0.120, P95: 0.840, P99: 1.9, Max: 3.2},
			Service: metrics.Percentiles{P50: 0.110, P95: 0.420, P99: 0.9},
		},
		Thresholds: []report.Check{
			{Source: "checkout.p95 < 800ms", Pass: false, ObservedText: "840ms"},
			{Source: "errors < 2%", Pass: true, ObservedText: "1.20%"},
		},
		Errors: []report.ErrorRow{{Journey: "buy", Step: "pay", Error: "status 503 for user0042@shoplab.test", Count: 144}},
	}
}

func reply(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestNarrateKeepsCitedClaims(t *testing.T) {
	r := sampleReport()
	fake := &provider.Fake{Replies: []any{reply(t, map[string]any{
		"summary": "The run failed its checkout latency target; p95 was 840ms against 800ms.",
		"claims": []map[string]any{
			{"text": "p95 latency was 840ms, above the 800ms target.", "label": "measured", "refs": []string{"overall.latency", "target.0"}},
			{"text": "Service time p95 of 420ms against 840ms latency suggests requests queued before being sent.", "label": "suspected", "refs": []string{"overall.service", "overall.latency"}},
		},
	})}, PerCall: provider.Usage{InputTokens: 900, OutputTokens: 150}}
	n, usage, err := Narrate(context.Background(), r, NarrateOptions{Provider: fake})
	if err != nil {
		t.Fatal(err)
	}
	if len(n.Claims) != 2 || n.Summary == "" || n.Model != "fake-model" {
		t.Fatalf("narrative = %+v", n)
	}
	if len(fake.Requests()) != 1 || usage.Total() != 1050 {
		t.Errorf("calls=%d usage=%d", len(fake.Requests()), usage.Total())
	}
	req := fake.Requests()[0]
	if req.SchemaName != "narrative" || len(req.JSONSchema) == 0 {
		t.Errorf("request schema %q", req.SchemaName)
	}
	sent := req.Messages[0].Content
	if !strings.Contains(sent, `"overall.latency"`) || strings.Contains(sent, "user0042@shoplab.test") {
		t.Errorf("facts sent to the model are wrong or unredacted:\n%s", sent)
	}
}

func TestNarrateDropsUnsupportedClaimsAndRepairs(t *testing.T) {
	r := sampleReport()
	first := reply(t, map[string]any{
		"summary": "The run failed with p95 at 840ms.",
		"claims": []map[string]any{
			{"text": "p95 latency was 910ms.", "label": "measured", "refs": []string{"overall.latency"}},          // invented figure
			{"text": "The database is slow.", "label": "measured", "refs": []string{}},                            // no citation
			{"text": "Checkout failed its target.", "label": "measured", "refs": []string{"thresholds.checkout"}}, // unknown id
			{"text": "144 payment errors returned 503.", "label": "measured", "refs": []string{"error.0"}},
		},
	})
	second := reply(t, map[string]any{
		"summary": "The run failed with p95 at 840ms.",
		"claims": []map[string]any{
			{"text": "p95 latency was 840ms.", "label": "measured", "refs": []string{"overall.latency"}},
			{"text": "Checkout failed its target.", "label": "measured", "refs": []string{"target.0"}},
			{"text": "144 payment errors returned 503.", "label": "measured", "refs": []string{"error.0"}},
		},
	})
	fake := &provider.Fake{Replies: []any{first, second}}
	n, _, err := Narrate(context.Background(), r, NarrateOptions{Provider: fake})
	if err != nil {
		t.Fatal(err)
	}
	if len(fake.Requests()) != 2 {
		t.Fatalf("expected one repair round, got %d calls", len(fake.Requests()))
	}
	feedback := fake.Requests()[1].Messages[2].Content
	for _, want := range []string{"910", "must cite facts", "thresholds.checkout"} {
		if !strings.Contains(feedback, want) {
			t.Errorf("repair feedback lacks %q:\n%s", want, feedback)
		}
	}
	if len(n.Claims) != 3 {
		t.Errorf("claims = %+v", n.Claims)
	}
}

func TestNarrateWithoutRepairDropsAndKeepsTheRest(t *testing.T) {
	r := sampleReport()
	fake := &provider.Fake{Replies: []any{reply(t, map[string]any{
		"summary": "Throughput peaked at 5000 requests per second.", // not in any fact
		"claims": []map[string]any{
			{"text": "p95 latency was 840ms.", "label": "measured", "refs": []string{"overall.latency"}},
			{"text": "Error rate 1.20% is likely from the payment step.", "label": "suspected", "refs": []string{"overall.requests", "error.0"}},
			{"text": "It might be fine.", "label": "maybe", "refs": []string{"verdict"}},
		},
	})}}
	n, _, err := Narrate(context.Background(), r, NarrateOptions{Provider: fake, Repairs: -1})
	if err == nil {
		t.Fatalf("an unsupported summary with no repair must fail, got %+v", n)
	}

	fake = &provider.Fake{Replies: []any{reply(t, map[string]any{
		"summary": "The run failed its checkout target.",
		"claims": []map[string]any{
			{"text": "p95 latency was 840ms.", "label": "measured", "refs": []string{"overall.latency"}},
			{"text": "It might be fine.", "label": "maybe", "refs": []string{"verdict"}},
		},
	})}}
	n, _, err = Narrate(context.Background(), r, NarrateOptions{Provider: fake, Repairs: -1})
	if err != nil || len(n.Claims) != 1 {
		t.Fatalf("n=%+v err=%v", n, err)
	}
}

func TestNarrativeRendersInEveryFormat(t *testing.T) {
	r := sampleReport()
	r.Narrative = &report.Narrative{Model: "m", Summary: "Failed <b>checkout</b>.", Claims: []report.Claim{
		{Text: "p95 latency was 840ms.", Label: "measured", Refs: []string{"overall.latency"}},
	}}
	var txt bytes.Buffer
	r.WriteNarrativeText(&txt)
	if !strings.Contains(txt.String(), "[measured] p95 latency was 840ms.  (overall.latency)") {
		t.Errorf("text:\n%s", txt.String())
	}
	var html bytes.Buffer
	if err := r.WriteHTML(&html); err != nil {
		t.Fatal(err)
	}
	h := html.String()
	if !strings.Contains(h, "Failed &lt;b&gt;checkout&lt;/b&gt;.") || !strings.Contains(h, `class="label measured"`) || !strings.Contains(h, "latency from scheduled send p50") {
		t.Errorf("html narrative missing or unescaped")
	}
}

func TestUnsupportedComparesByValue(t *testing.T) {
	for _, c := range []struct {
		text, src, want string
	}{
		{"p95 was 840ms", "p95 840.0ms", ""},
		{"p95 was 840ms", "p95 1840.0ms", "840"},
		{"errors 1.2%", "1.20% failed", ""},
		{"12,000 requests", "12000 requests", ""},
		{"rate 840", "rate 8.40", "840"},
		{"2 journeys", "", ""},
	} {
		if got := unsupported(c.text, c.src); got != c.want {
			t.Errorf("unsupported(%q, %q) = %q, want %q", c.text, c.src, got, c.want)
		}
	}
}
