package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"

	"github.com/Ivan825/Stampede/internal/metrics"
	"github.com/Ivan825/Stampede/internal/protocol/grpcx"
	"github.com/Ivan825/Stampede/internal/protocol/httpx"
	"github.com/Ivan825/Stampede/internal/scenario"
)

// grpcStep is what the engine resolves for a grpc step before the run.
type grpcStep struct {
	target grpcx.Target
	src    grpcx.Source
	// accept has bit c set for each accepted status code c.
	accept uint32
}

// prepareGRPC resolves every grpc step's target and accepted codes, and
// loads descriptor files so a bad .proto or a missing method fails the
// run before it starts. Reflection happens on first use instead, as the
// target may not be up yet.
func (e *Engine) prepareGRPC(static *vuVars) error {
	e.grpcSteps = map[int]*grpcStep{}
	for _, st := range e.prog.Steps {
		g := st.GRPC
		if g == nil {
			continue
		}
		if e.grpcPool == nil {
			e.grpcPool = grpcx.NewPool(grpcx.PoolOptions{
				Dial:               httpx.Dialer(e.httpOpts),
				InsecureSkipVerify: e.httpOpts.InsecureSkipVerify,
				UserAgent:          e.userAgent,
			})
			e.grpcDescs = grpcx.NewDescriptors()
		}
		raw := e.baseURL
		if g.Target != nil {
			s, err := g.Target.Render(static)
			if err != nil {
				return fmt.Errorf("step %q: target: %w", st.Name, err)
			}
			raw = s
		}
		if raw == "" {
			return fmt.Errorf("step %q: no gRPC target: set target or target.baseURL", st.Name)
		}
		t, err := grpcx.ParseTarget(raw)
		if err != nil {
			return fmt.Errorf("step %q: %w", st.Name, err)
		}
		gs := &grpcStep{target: t, src: grpcx.Source{Protoset: g.Protoset, Proto: g.Proto, ImportPaths: g.ImportPaths}}
		for _, name := range g.Codes {
			c, ok := grpcx.CodeByName(name)
			if !ok {
				return fmt.Errorf("step %q: unknown gRPC status %q", st.Name, name)
			}
			gs.accept |= 1 << c
		}
		if gs.src.Files() {
			if _, err := e.grpcDescs.Method(context.Background(), nil, t, gs.src, g.Service, g.Method); err != nil {
				return fmt.Errorf("step %q: %w", st.Name, err)
			}
		}
		e.grpcSteps[st.ID] = gs
	}
	return nil
}

// grpcCall makes one unary or server-streaming call. Its latency is the
// whole call; a server stream also records the time to its first message
// and its message rate, as for server-sent events. Checks and extractors
// see the response as protojson (a JSON array for a stream) and the
// response metadata as headers.
func (v *VU) grpcCall(ctx context.Context, st *scenario.CStep, intended time.Time) error {
	g, gs := st.GRPC, v.e.grpcSteps[st.ID]
	run := v.begin(st, intended)
	if err := run.blocked(gs.target.URL()); err != nil {
		return err
	}
	var body []byte
	if g.Message != nil {
		val, err := g.Message.Value(v.vars)
		if err != nil {
			return run.fail("template error", err)
		}
		if body, err = json.Marshal(val); err != nil {
			return run.fail("template error", err)
		}
	}
	md := metadata.MD{}
	for _, kv := range g.Metadata {
		s, err := kv.Value.Render(v.vars)
		if err != nil {
			return run.fail("template error", err)
		}
		md.Set(kv.Name, s)
	}
	if len(md.Get("traceparent")) == 0 {
		tp, baggage := v.traceContext()
		md.Set("traceparent", tp)
		if baggage != "" {
			md.Set("baggage", baggage)
		}
	}

	conn, err := v.e.grpcPool.Conn(gs.target, v.ID)
	if err != nil {
		return run.fail("network error", err)
	}
	cctx, cancel := context.WithTimeout(ctx, v.stepTimeout(st.Req.Timeout))
	defer cancel()
	m, err := v.e.grpcDescs.Method(cctx, conn, gs.target, gs.src, g.Service, g.Method)
	if err != nil {
		switch {
		case ctx.Err() != nil:
			return ctx.Err()
		case errors.Is(err, grpcx.ErrUnknownMethod):
			return run.fail("grpc unknown method", err)
		}
		return run.fail("grpc reflection failed", err)
	}
	req, err := grpcx.NewRequest(m, body)
	if err != nil {
		return run.fail("grpc invalid message", err)
	}

	res := grpcx.Invoke(cctx, conn, m, req, md, v.e.maxBody)
	run.s.Start, run.s.End = res.Start, res.End
	run.s.BytesIn, run.s.BytesOut = res.BytesIn, res.BytesOut
	if m.IsStreamingServer() && res.Messages > 0 {
		run.s.Events = res.Messages
		run.s.Phases[metrics.PhaseFirstEvent] = res.First.Sub(res.Start)
		run.s.StreamTime = res.End.Sub(res.First)
	}
	if res.Err != nil && ctx.Err() != nil {
		return ctx.Err()
	}
	if gs.accept&(1<<res.Code) == 0 {
		label := "gRPC " + grpcx.CodeName(res.Code)
		if g.CheckedStatus {
			run.s.ChecksFailed++
			label = "check status (" + label + ")"
		}
		return run.fail(label, nil)
	}
	if g.CheckedStatus {
		run.s.ChecksPassed++
	}
	if res.Code == codes.OK && res.Err != nil {
		return run.fail("grpc invalid response", res.Err)
	}

	header := http.Header{}
	for _, md := range []metadata.MD{res.Header, res.Trailer} {
		for k, vs := range md {
			for _, x := range vs {
				header.Add(k, x)
			}
		}
	}
	return v.verify(&run, st.Req, &httpx.Result{
		Status: int(res.Code), Header: header, Body: res.Body, Start: res.Start, End: res.End,
	})
}
