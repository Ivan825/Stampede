package scenario

import (
	"net/url"
	"regexp"
	"sort"
	"strings"
)

// Limits on the observe block.
const (
	MaxObserveQueries = 20
	maxQueryLen       = 4096
)

var (
	queryNameRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_.-]{0,63}$`)
	// IntegrationNameRe matches the names of server integrations and
	// notification channels.
	IntegrationNameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,99}$`)
)

// problems returns [path, message] pairs for an invalid observe block.
func (o *Observe) problems() [][2]string {
	var out [][2]string
	add := func(path, msg string) { out = append(out, [2]string{path, msg}) }
	if p := o.Prometheus; p != nil {
		switch {
		case p.URL == "" && p.Integration == "":
			add("observe.prometheus", "set url (stampede run) or integration (server runs)")
		case p.URL != "" && p.Integration != "":
			add("observe.prometheus", "set url or integration, not both")
		}
		if p.URL != "" && !strings.Contains(p.URL, "${") && !httpURL(p.URL) {
			add("observe.prometheus.url", "must be an absolute http(s) URL, got "+quote(p.URL))
		}
		if p.Integration != "" && !IntegrationNameRe.MatchString(p.Integration) {
			add("observe.prometheus.integration", "must be an integration name, got "+quote(p.Integration))
		}
		if p.Integration != "" && p.BearerToken != "" {
			add("observe.prometheus.bearerToken", "a server integration holds its own credentials; remove bearerToken")
		}
		if len(p.Queries) == 0 {
			add("observe.prometheus.queries", "at least one query is required, for example cpu: 'rate(process_cpu_seconds_total[30s])'")
		}
		if len(p.Queries) > MaxObserveQueries {
			add("observe.prometheus.queries", "at most 20 queries")
		}
		for _, name := range p.QueryNames() {
			q := p.Queries[name]
			if !queryNameRe.MatchString(name) {
				add("observe.prometheus.queries."+name, "names start with a letter and use letters, digits, '_', '.' or '-' (at most 64)")
			}
			if strings.TrimSpace(q) == "" || len(q) > maxQueryLen {
				add("observe.prometheus.queries."+name, "must be a PromQL expression of at most 4096 characters")
			}
		}
	}
	if t := o.Traces; t != nil {
		switch {
		case t.URL == "" && t.Integration == "":
			add("observe.traces", "set url (a link template containing {traceId}) or integration (server runs)")
		case t.URL != "" && t.Integration != "":
			add("observe.traces", "set url or integration, not both")
		}
		if t.URL != "" {
			if msg := CheckTraceURL(t.URL); msg != "" {
				add("observe.traces.url", msg)
			}
		}
		if t.Integration != "" && !IntegrationNameRe.MatchString(t.Integration) {
			add("observe.traces.integration", "must be an integration name, got "+quote(t.Integration))
		}
	}
	return out
}

// QueryNames returns the query names in a stable (sorted) order.
func (p *PrometheusObserve) QueryNames() []string {
	names := make([]string, 0, len(p.Queries))
	for k := range p.Queries {
		names = append(names, k)
	}
	sort.Strings(names)
	return names
}

// CheckTraceURL validates a trace link template and returns a problem, or
// "" when it is usable.
func CheckTraceURL(tmpl string) string {
	if !strings.Contains(tmpl, TraceIDPlaceholder) {
		return "must contain {traceId}, for example https://jaeger.example.com/trace/{traceId}"
	}
	if !httpURL(strings.ReplaceAll(tmpl, TraceIDPlaceholder, "0")) {
		return "must be an absolute http(s) URL"
	}
	return ""
}

// TraceLink fills a trace link template with a trace ID.
func TraceLink(tmpl, traceID string) string {
	if tmpl == "" || traceID == "" {
		return ""
	}
	return strings.ReplaceAll(tmpl, TraceIDPlaceholder, url.PathEscape(traceID))
}

func httpURL(s string) bool {
	u, err := url.Parse(s)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
}

func quote(s string) string { return `"` + s + `"` }
