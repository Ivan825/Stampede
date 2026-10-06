package server

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/prometheus/client_golang/prometheus"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// tracer creates Stampede's own spans: one per API request (through
// otelhttp) and one per run. Without an OTLP endpoint the global provider
// is a no-op and so are these.
var tracer = otel.Tracer("github.com/Ivan825/Stampede/internal/server")

// httpMetrics are the API's request metrics for /metrics.
type httpMetrics struct {
	duration *prometheus.HistogramVec
}

func newHTTPMetrics() *httpMetrics {
	return &httpMetrics{
		duration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "stampede_http_request_duration_seconds",
			Help:    "API request latency by method, route and status code (live streams excluded).",
			Buckets: []float64{.005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10},
		}, []string{"method", "route", "code"}),
	}
}

func (m *httpMetrics) collectors() []prometheus.Collector { return []prometheus.Collector{m.duration} }

// instrument traces every API request and records its latency. The span
// and the metric are named after the matched route pattern, such as
// "GET /api/v1/runs/{runId}", so neither grows with IDs in paths.
func (s *Server) instrument(next http.Handler) http.Handler {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
		start := time.Now()
		next.ServeHTTP(ww, r)
		route := routeOf(r)
		span := trace.SpanFromContext(r.Context())
		span.SetName(r.Method + " " + route)
		span.SetAttributes(attribute.String("http.route", route))
		if strings.HasSuffix(route, "/live") {
			return
		}
		status := ww.Status()
		if status == 0 {
			status = http.StatusOK
		}
		s.httpm.duration.WithLabelValues(r.Method, route, strconv.Itoa(status)).Observe(time.Since(start).Seconds())
	})
	// otelhttp names the span again once the request is routed, so the
	// formatter names it after the route too.
	return otelhttp.NewHandler(inner, "api",
		otelhttp.WithSpanNameFormatter(func(_ string, r *http.Request) string { return r.Method + " " + routeOf(r) }))
}

// routeOf is the matched chi route pattern, or "unmatched" before routing
// or when nothing matched.
func routeOf(r *http.Request) string {
	if rc := chi.RouteContext(r.Context()); rc != nil {
		if p := rc.RoutePattern(); p != "" && !strings.HasSuffix(p, "/*") {
			return p
		}
	}
	return "unmatched"
}
