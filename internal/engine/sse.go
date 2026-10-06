package engine

import (
	"context"
	"errors"
	"io"
	"mime"
	"sync/atomic"
	"time"

	"github.com/Ivan825/Stampede/internal/metrics"
	"github.com/Ivan825/Stampede/internal/protocol/httpx"
	"github.com/Ivan825/Stampede/internal/protocol/sse"
	"github.com/Ivan825/Stampede/internal/scenario"
)

// sse reads a server-sent event stream until its stop condition. The
// step's latency is the whole stream; the time to the first event is
// recorded as its own phase and the events after it give the stream's
// rate, which for an LLM API are time to first token and tokens per
// second. Checks and extractors see the matching event's data (or the
// last event's) as the response body.
func (v *VU) sse(ctx context.Context, st *scenario.CStep, intended time.Time) error {
	r, until := st.Req, st.SSE
	run := v.begin(st, intended)

	req, bodyLen, err := v.buildRequest(ctx, r)
	if err != nil {
		return run.fail("template error", err)
	}
	if err := run.blocked(req.URL); err != nil {
		return err
	}
	timeout := v.stepTimeout(r.Timeout)
	if r.Timeout == 0 && until.Duration.D() >= timeout {
		// The default timeout bounds waiting, not a stream asked to run
		// for longer.
		timeout += until.Duration.D()
	}
	rctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var capped atomic.Bool
	if until.Duration > 0 {
		t := time.AfterFunc(until.Duration.D(), func() {
			capped.Store(true)
			cancel()
		})
		defer t.Stop()
	}
	req = req.WithContext(rctx)
	if req.Header.Get("Accept") == "" {
		req.Header.Set("Accept", "text/event-stream")
	}
	req.Header.Set("Cache-Control", "no-cache")

	res, resp := httpx.Open(v.client, req, bodyLen)
	if resp == nil {
		run.fromHTTP(res)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return run.fail(httpx.ClassifyError(res.Err), nil)
	}

	statusOK := res.Status < 400
	if r.Check != nil && !r.Check.Status.Empty() {
		statusOK = r.Check.Status.Match(res.Status)
	}
	if !statusOK {
		// Keep the error body for checks, and let verify label the status.
		res.Body, err = io.ReadAll(io.LimitReader(resp.Body, v.e.maxBody))
		resp.Body.Close()
		res.Finish(int64(len(res.Body)), err)
		run.fromHTTP(res)
		return v.verify(&run, r, res)
	}
	if mt, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type")); mt != "text/event-stream" {
		resp.Body.Close()
		res.Finish(0, nil)
		run.fromHTTP(res)
		return run.fail("sse not an event stream", nil)
	}

	rd := sse.NewReader(resp.Body, int(v.e.maxBody))
	var (
		first   time.Time
		events  int
		matched bool
		readErr error
	)
	for {
		ev, err := rd.Next()
		if err != nil {
			readErr = err
			break
		}
		now := time.Now()
		if events == 0 {
			first = now
		}
		events++
		res.Body = append(res.Body[:0], ev.Data...)
		if until.Match != nil && until.Match.Match(ev.Data) {
			matched = true
			break
		}
		if until.Events > 0 && events >= until.Events {
			break
		}
	}
	resp.Body.Close()
	res.Finish(rd.BytesRead(), nil)
	run.fromHTTP(res)
	run.s.Events = events
	if events > 0 {
		run.s.Phases[metrics.PhaseFirstEvent] = first.Sub(res.Start)
		run.s.StreamTime = res.End.Sub(first)
	}

	switch {
	case readErr == nil, errors.Is(readErr, io.EOF):
		// The stop condition was met, or the server ended the stream.
	case capped.Load():
		// until.duration elapsed.
	case ctx.Err() != nil:
		return ctx.Err()
	case errors.Is(readErr, sse.ErrTooLarge):
		return run.fail("sse event too large", nil)
	default:
		return run.fail(httpx.ClassifyError(readErr), nil)
	}
	switch {
	case events == 0:
		return run.fail("sse no events", nil)
	case until.Match != nil && !matched:
		return run.fail("sse no match", nil)
	case until.Events > 0 && events < until.Events:
		return run.fail("sse ended early", nil)
	}
	return v.verify(&run, r, res)
}
