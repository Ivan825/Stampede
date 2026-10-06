// Package metrics defines ShopLab's Prometheus metrics. Everything is
// registered on a private registry (plus the Go and process collectors) that
// the API serves at /metrics.
package metrics

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
)

// Metrics bundles every collector ShopLab updates directly.
type Metrics struct {
	Registry *prometheus.Registry

	HTTPDuration *prometheus.HistogramVec // route, method, status
	HTTPInFlight prometheus.Gauge

	DBQueries *prometheus.CounterVec // result=ok|error

	CacheRequests *prometheus.CounterVec // result=hit|miss|stale|error
	CacheLoads    prometheus.Counter     // expensive recomputations
	CacheLoadDur  prometheus.Histogram

	Oversold  prometheus.Counter
	Checkouts *prometheus.CounterVec // result=ok|out_of_stock|empty_cart|error

	Logins *prometheus.CounterVec // result=ok|invalid
}

// New creates and registers all metrics. fixes is exported as the
// shoplab_fix_enabled gauge so dashboards can label runs.
func New(fixes map[string]bool) *Metrics {
	reg := prometheus.NewRegistry()
	reg.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
	)

	m := &Metrics{
		Registry: reg,
		HTTPDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "shoplab_http_request_duration_seconds",
			Help:    "HTTP request latency by chi route pattern, method and status code.",
			Buckets: []float64{.001, .0025, .005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10},
		}, []string{"route", "method", "status"}),
		HTTPInFlight: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "shoplab_http_requests_in_flight",
			Help: "HTTP requests currently being served.",
		}),
		DBQueries: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "shoplab_db_queries_total",
			Help: "SQL statements sent to Postgres (Query, QueryRow and Exec).",
		}, []string{"result"}),
		CacheRequests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "shoplab_cache_requests_total",
			Help: "Product-detail cache lookups by result.",
		}, []string{"result"}),
		CacheLoads: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "shoplab_cache_loads_total",
			Help: "Times the expensive product detail was recomputed from Postgres.",
		}),
		CacheLoadDur: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "shoplab_cache_load_duration_seconds",
			Help:    "Time to recompute a product detail on a cache miss.",
			Buckets: []float64{.001, .0025, .005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5},
		}),
		Oversold: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "shoplab_oversold_total",
			Help: "Units sold beyond available inventory, detected after checkout commits.",
		}),
		Checkouts: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "shoplab_checkouts_total",
			Help: "Checkout attempts by result.",
		}, []string{"result"}),
		Logins: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "shoplab_logins_total",
			Help: "Login attempts by result.",
		}, []string{"result"}),
	}
	reg.MustRegister(m.HTTPDuration, m.HTTPInFlight, m.DBQueries, m.CacheRequests,
		m.CacheLoads, m.CacheLoadDur, m.Oversold, m.Checkouts, m.Logins)

	fix := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "shoplab_fix_enabled",
		Help: "1 when the named planted bottleneck is fixed, 0 when it is active.",
	}, []string{"fix"})
	for name, on := range fixes {
		v := 0.0
		if on {
			v = 1
		}
		fix.WithLabelValues(name).Set(v)
	}
	reg.MustRegister(fix)
	return m
}

// RegisterSessionStore exposes session store size and retained bytes.
func (m *Metrics) RegisterSessionStore(size func() int, bytes func() int64) {
	m.Registry.MustRegister(
		prometheus.NewGaugeFunc(prometheus.GaugeOpts{
			Name: "shoplab_sessions",
			Help: "Sessions held in the in-memory session store (including expired ones not yet evicted).",
		}, func() float64 { return float64(size()) }),
		prometheus.NewGaugeFunc(prometheus.GaugeOpts{
			Name: "shoplab_session_store_bytes",
			Help: "Approximate bytes retained by the session store.",
		}, func() float64 { return float64(bytes()) }),
	)
}

// RegisterPool exposes pgxpool statistics, read at scrape time.
func (m *Metrics) RegisterPool(pool *pgxpool.Pool) {
	m.Registry.MustRegister(&poolCollector{pool: pool})
}

// QueryTracer returns a pgx tracer that counts statements. On the listing
// endpoint the rate of this counter divided by the request rate is the
// clearest N+1 signal.
func (m *Metrics) QueryTracer() pgx.QueryTracer { return &queryTracer{c: m.DBQueries} }

type queryTracer struct{ c *prometheus.CounterVec }

func (t *queryTracer) TraceQueryStart(ctx context.Context, _ *pgx.Conn, _ pgx.TraceQueryStartData) context.Context {
	return ctx
}

func (t *queryTracer) TraceQueryEnd(_ context.Context, _ *pgx.Conn, data pgx.TraceQueryEndData) {
	if data.Err != nil {
		t.c.WithLabelValues("error").Inc()
		return
	}
	t.c.WithLabelValues("ok").Inc()
}

var (
	poolAcquired     = prometheus.NewDesc("shoplab_db_pool_acquired_conns", "Connections currently checked out of the pool.", nil, nil)
	poolIdle         = prometheus.NewDesc("shoplab_db_pool_idle_conns", "Idle connections in the pool.", nil, nil)
	poolTotal        = prometheus.NewDesc("shoplab_db_pool_total_conns", "Total connections in the pool (acquired + idle + constructing).", nil, nil)
	poolMax          = prometheus.NewDesc("shoplab_db_pool_max_conns", "Configured pool size (MaxConns).", nil, nil)
	poolAcquires     = prometheus.NewDesc("shoplab_db_pool_acquire_total", "Successful connection acquisitions.", nil, nil)
	poolAcquireSecs  = prometheus.NewDesc("shoplab_db_pool_acquire_duration_seconds_total", "Total time spent acquiring connections.", nil, nil)
	poolWaits        = prometheus.NewDesc("shoplab_db_pool_wait_total", "Acquisitions that had to wait because the pool was empty.", nil, nil)
	poolWaitSecs     = prometheus.NewDesc("shoplab_db_pool_wait_duration_seconds_total", "Total time spent waiting for a connection when the pool was empty.", nil, nil)
	poolCanceledAcqs = prometheus.NewDesc("shoplab_db_pool_canceled_acquire_total", "Acquisitions cancelled by the caller's context (e.g. client timeout) while waiting.", nil, nil)
)

type poolCollector struct{ pool *pgxpool.Pool }

func (c *poolCollector) Describe(ch chan<- *prometheus.Desc) {
	for _, d := range []*prometheus.Desc{poolAcquired, poolIdle, poolTotal, poolMax, poolAcquires, poolAcquireSecs, poolWaits, poolWaitSecs, poolCanceledAcqs} {
		ch <- d
	}
}

func (c *poolCollector) Collect(ch chan<- prometheus.Metric) {
	s := c.pool.Stat()
	gauge := func(d *prometheus.Desc, v float64) {
		ch <- prometheus.MustNewConstMetric(d, prometheus.GaugeValue, v)
	}
	counter := func(d *prometheus.Desc, v float64) {
		ch <- prometheus.MustNewConstMetric(d, prometheus.CounterValue, v)
	}
	gauge(poolAcquired, float64(s.AcquiredConns()))
	gauge(poolIdle, float64(s.IdleConns()))
	gauge(poolTotal, float64(s.TotalConns()))
	gauge(poolMax, float64(s.MaxConns()))
	counter(poolAcquires, float64(s.AcquireCount()))
	counter(poolAcquireSecs, s.AcquireDuration().Seconds())
	counter(poolWaits, float64(s.EmptyAcquireCount()))
	counter(poolWaitSecs, s.EmptyAcquireWaitTime().Seconds())
	counter(poolCanceledAcqs, float64(s.CanceledAcquireCount()))
}

// ObserveHTTP records one finished request.
func (m *Metrics) ObserveHTTP(route, method, status string, d time.Duration) {
	m.HTTPDuration.WithLabelValues(route, method, status).Observe(d.Seconds())
}
