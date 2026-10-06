// Package observe connects a run to the target's own telemetry: it queries
// Prometheus for the target's metrics over the run, for the report.
package observe

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/Ivan825/Stampede/internal/report"
	"github.com/Ivan825/Stampede/internal/version"
)

// MaxPoints bounds the points per query. Prometheus refuses ranges of more
// than 11,000 points per series, so long runs use a coarser step.
const MaxPoints = 10_000

// maxResponse bounds a query_range response body.
const maxResponse = 32 << 20

// Prometheus is a client for the Prometheus HTTP API (and compatible
// servers such as Thanos, Mimir and VictoriaMetrics).
type Prometheus struct {
	// URL is the base URL, for example http://prometheus:9090. A path
	// prefix (https://host/prometheus) is kept.
	URL string
	// BearerToken, when set, is sent as Authorization: Bearer.
	BearerToken string
	// Client defaults to a client with a 30 second timeout.
	Client *http.Client
}

// Point is one sample of a series.
type Point struct {
	T time.Time
	V float64
}

// Series is one time series returned by a range query.
type Series struct {
	Labels map[string]string
	Points []Point
}

// Error is an error reported by Prometheus itself, such as a PromQL
// syntax error.
type Error struct {
	Status int
	Type   string
	Msg    string
}

func (e *Error) Error() string {
	if e.Type != "" {
		return fmt.Sprintf("prometheus: %s: %s", e.Type, e.Msg)
	}
	return "prometheus: " + e.Msg
}

type apiResponse struct {
	Status    string   `json:"status"`
	ErrorType string   `json:"errorType"`
	Error     string   `json:"error"`
	Warnings  []string `json:"warnings"`
	Data      struct {
		ResultType string `json:"resultType"`
		Result     []struct {
			Metric map[string]string    `json:"metric"`
			Values [][2]json.RawMessage `json:"values"`
		} `json:"result"`
	} `json:"data"`
}

// QueryRange evaluates query at every step between start and end.
func (p *Prometheus) QueryRange(ctx context.Context, query string, start, end time.Time, step time.Duration) ([]Series, error) {
	base := strings.TrimRight(p.URL, "/")
	if u, err := url.Parse(base); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, fmt.Errorf("prometheus URL %q is not an absolute http(s) URL", p.URL)
	}
	form := url.Values{}
	form.Set("query", query)
	form.Set("start", unixString(start))
	form.Set("end", unixString(end))
	form.Set("step", strconv.FormatFloat(step.Seconds(), 'f', -1, 64))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/api/v1/query_range", strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "stampede/"+version.Version)
	if p.BearerToken != "" {
		req.Header.Set("Authorization", "Bearer "+p.BearerToken)
	}
	c := p.Client
	if c == nil {
		c = &http.Client{Timeout: 30 * time.Second}
	}
	resp, err := c.Do(req)
	if err != nil {
		return nil, fmt.Errorf("prometheus: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponse+1))
	if err != nil {
		return nil, fmt.Errorf("prometheus: read response: %w", err)
	}
	if len(body) > maxResponse {
		return nil, errors.New("prometheus: response larger than 32 MiB; narrow the query")
	}
	var r apiResponse
	if err := json.Unmarshal(body, &r); err != nil {
		if resp.StatusCode != http.StatusOK {
			return nil, &Error{Status: resp.StatusCode, Msg: fmt.Sprintf("HTTP %d: %s", resp.StatusCode, snippet(body))}
		}
		return nil, fmt.Errorf("prometheus: response is not the Prometheus API format: %s", snippet(body))
	}
	if r.Status != "success" {
		msg := r.Error
		if msg == "" {
			msg = fmt.Sprintf("HTTP %d", resp.StatusCode)
		}
		return nil, &Error{Status: resp.StatusCode, Type: r.ErrorType, Msg: msg}
	}
	if r.Data.ResultType != "matrix" {
		return nil, fmt.Errorf("prometheus: expected a range vector (matrix), got %q", r.Data.ResultType)
	}
	out := make([]Series, 0, len(r.Data.Result))
	for _, res := range r.Data.Result {
		s := Series{Labels: res.Metric, Points: make([]Point, 0, len(res.Values))}
		for _, v := range res.Values {
			var ts float64
			var val string
			if json.Unmarshal(v[0], &ts) != nil || json.Unmarshal(v[1], &val) != nil {
				return nil, errors.New("prometheus: malformed sample in response")
			}
			f, err := strconv.ParseFloat(val, 64)
			if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
				continue // NaN and ±Inf cannot be charted or encoded as JSON
			}
			sec, frac := math.Modf(ts)
			s.Points = append(s.Points, Point{T: time.Unix(int64(sec), int64(frac*1e9)), V: f})
		}
		out = append(out, s)
	}
	return out, nil
}

// Step returns the query step for a run: the report interval, made
// coarser when the run is long enough to exceed MaxPoints.
func Step(start, end time.Time, interval time.Duration) time.Duration {
	step := max(interval, time.Second)
	if span := end.Sub(start); span > 0 && span/step > MaxPoints {
		step = (span/MaxPoints + time.Second - 1).Truncate(time.Second)
	}
	return step
}

// Query is a named PromQL expression.
type Query struct {
	Name, Expr string
}

// Collect evaluates each query over [start, end] and returns them as
// report target metrics, with times relative to start. A query that fails
// is returned with its error rather than failing the others.
func Collect(ctx context.Context, p *Prometheus, queries []Query, start, end time.Time, interval time.Duration) []report.TargetMetric {
	step := Step(start, end, interval)
	out := make([]report.TargetMetric, 0, len(queries))
	for _, q := range queries {
		m := report.TargetMetric{Name: q.Name, Query: q.Expr, Points: []report.MetricPoint{}}
		qctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		series, err := p.QueryRange(qctx, q.Expr, start, end, step)
		cancel()
		switch {
		case err != nil:
			m.Error = err.Error()
		case len(series) == 0:
			m.Error = "no data: the query returned no series for the run's time range"
		default:
			if len(series) > 1 {
				m.Error = fmt.Sprintf("the query returned %d series; the first is shown. Aggregate it to one series, for example sum(...) or max(...)", len(series))
			}
			for _, pt := range series[0].Points {
				m.Points = append(m.Points, report.MetricPoint{T: max(pt.T.Sub(start).Seconds(), 0), Value: pt.V})
			}
		}
		out = append(out, m)
	}
	return out
}

func unixString(t time.Time) string {
	return strconv.FormatFloat(float64(t.UnixNano())/1e9, 'f', 3, 64)
}

func snippet(b []byte) string {
	s := strings.TrimSpace(string(b))
	if len(s) > 200 {
		s = s[:200] + "…"
	}
	return s
}
