package scenario

import (
	"strings"
	"testing"
)

func TestObserveValidation(t *testing.T) {
	base := `
metadata: {name: x}
target: {baseURL: "http://localhost:1"}
journeys: [{name: a, steps: [{get: /a}]}]
load: {vus: 1, duration: 1s}
`
	tests := []struct {
		name, observe, wantErr string
	}{
		{"url", `observe: {prometheus: {url: "http://prom:9090", queries: {cpu: "up"}}}`, ""},
		{"templated url", `observe: {prometheus: {url: "${env.PROM}", bearerToken: "${env.T}", queries: {cpu: "up"}}}`, ""},
		{"integration", `observe: {prometheus: {integration: prod-prom, queries: {cpu: "up"}}, traces: {integration: jaeger}}`, ""},
		{"traces url", `observe: {traces: {url: "https://jaeger.example.com/trace/{traceId}"}}`, ""},
		{"neither", `observe: {prometheus: {queries: {cpu: "up"}}}`, "set url (stampede run) or integration"},
		{"both", `observe: {prometheus: {url: "http://p", integration: p, queries: {cpu: "up"}}}`, "not both"},
		{"no queries", `observe: {prometheus: {url: "http://p"}}`, "at least one query"},
		{"bad name", `observe: {prometheus: {url: "http://p", queries: {"1cpu": "up"}}}`, "names start with a letter"},
		{"empty query", `observe: {prometheus: {url: "http://p", queries: {cpu: " "}}}`, "PromQL expression"},
		{"bad url", `observe: {prometheus: {url: "file:///etc", queries: {cpu: "up"}}}`, "absolute http(s) URL"},
		{"token with integration", `observe: {prometheus: {integration: p, bearerToken: x, queries: {cpu: "up"}}}`, "remove bearerToken"},
		{"trace placeholder", `observe: {traces: {url: "https://jaeger.example.com/trace/"}}`, "must contain {traceId}"},
		{"trace scheme", `observe: {traces: {url: "javascript:alert({traceId})"}}`, "absolute http(s) URL"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse([]byte(base + tc.observe))
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error %v, want it to mention %q", err, tc.wantErr)
			}
		})
	}
}

func TestTraceLink(t *testing.T) {
	got := TraceLink("https://tempo.example.com/explore?trace={traceId}", "0af7651916cd43dd8448eb211c80319c")
	if got != "https://tempo.example.com/explore?trace=0af7651916cd43dd8448eb211c80319c" {
		t.Errorf("TraceLink = %q", got)
	}
	if TraceLink("", "abc") != "" || TraceLink("https://x/{traceId}", "") != "" {
		t.Error("empty template or id should give no link")
	}
}
