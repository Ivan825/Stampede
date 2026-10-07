package ai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/Ivan825/Stampede/internal/protocol/grpcx"
	"github.com/Ivan825/Stampede/internal/protocol/httpx"
	"github.com/Ivan825/Stampede/internal/scenario"
)

// Close releases connections the dry run opened for gRPC steps.
func (d *DryRunner) Close() {
	if d.grpcPool != nil {
		d.grpcPool.Close()
	}
}

// grpcCall makes one gRPC call of a dry run, as the engine does: the
// response is checked and extracted from as JSON, with response metadata
// as headers and the status code as the status.
func (p *pass) grpcCall(ctx context.Context, st *scenario.CStep) error {
	d := p.d
	red := d.Redactor
	g := st.GRPC
	tr := StepTrace{Step: st.Name, Method: "GRPC"}
	fail := func(msg string) error {
		tr.Error = msg
		p.trace.Steps = append(p.trace.Steps, tr)
		return errStop
	}
	p.requests++
	limit := d.MaxRequests
	if limit <= 0 {
		limit = 100
	}
	if p.requests > limit {
		return fail(fmt.Sprintf("more than %d requests in one pass; the dry run stopped", limit))
	}

	raw := d.BaseURL
	if g.Target != nil {
		s, err := g.Target.Render(p.vars)
		if err != nil {
			return fail("target: " + err.Error())
		}
		raw = s
	}
	t, err := grpcx.ParseTarget(raw)
	if err != nil {
		return fail(err.Error())
	}
	tr.URL = red.URL(t.String() + "/" + g.Service + "/" + g.Method)
	if d.Allow != nil {
		if why := d.Allow(t.URL()); why != "" {
			return fail("not sent: " + why)
		}
	}
	var body []byte
	if g.Message != nil {
		v, err := g.Message.Value(p.vars)
		if err != nil {
			return fail("could not build the message: " + err.Error())
		}
		if body, err = json.Marshal(v); err != nil {
			return fail("could not build the message: " + err.Error())
		}
	}
	tr.RequestBody = red.Body(body, d.limit())
	md := metadata.MD{}
	hdr := http.Header{}
	for _, kv := range g.Metadata {
		s, err := kv.Value.Render(p.vars)
		if err != nil {
			return fail("metadata " + kv.Name + ": " + err.Error())
		}
		md.Set(kv.Name, s)
		hdr.Set(kv.Name, s)
	}
	tr.RequestHeaders = red.Headers(hdr)

	d.grpcOnce.Do(func() {
		d.grpcPool = grpcx.NewPool(grpcx.PoolOptions{PerTarget: 1, InsecureSkipVerify: d.Program.Scenario.Target.HTTP.InsecureSkipVerify})
		d.grpcDescs = grpcx.NewDescriptors()
	})
	conn, err := d.grpcPool.Conn(t, 0)
	if err != nil {
		return fail("network error: " + red.Text(err.Error()))
	}
	timeout := st.Req.Timeout.D()
	if timeout == 0 {
		timeout = d.Program.Scenario.Target.Timeout.D()
	}
	if timeout == 0 || timeout > 30*time.Second {
		timeout = 30 * time.Second
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var m protoreflect.MethodDescriptor
	if d.GRPCFiles != nil {
		m, err = grpcx.FindMethod(d.GRPCFiles, g.Service, g.Method)
	}
	if m == nil {
		m, err = d.grpcDescs.Method(cctx, conn, t, grpcx.Source{Protoset: g.Protoset, Proto: g.Proto, ImportPaths: g.ImportPaths}, g.Service, g.Method)
	}
	if err != nil {
		if errors.Is(err, grpcx.ErrUnknownMethod) {
			return fail(err.Error())
		}
		return fail("could not load the method's descriptor: " + red.Text(err.Error()))
	}
	req, err := grpcx.NewRequest(m, body)
	if err != nil {
		return fail("the message does not fit " + string(m.Input().FullName()) + ": " + err.Error())
	}
	res := grpcx.Invoke(cctx, conn, m, req, md, 10<<20)
	tr.DurationMs = float64(res.End.Sub(res.Start).Microseconds()) / 1000
	tr.Status = int(res.Code)
	header := http.Header{}
	for _, mdv := range []metadata.MD{res.Header, res.Trailer} {
		for k, vs := range mdv {
			for _, x := range vs {
				header.Add(k, x)
			}
		}
	}
	tr.ResponseHeaders = red.Headers(header)
	accepted := false
	name := grpcx.CodeName(res.Code)
	for _, c := range g.Codes {
		if c == name {
			accepted = true
		}
	}
	if !accepted {
		tr.ResponseBody = red.Body(res.Body, d.limit())
		msg := "gRPC " + name
		if res.Err != nil {
			msg += ": " + red.Text(res.Err.Error())
		}
		if g.CheckedStatus {
			msg = "check status failed: " + msg
		}
		return fail(msg)
	}
	if res.Err != nil && name == "OK" {
		return fail("invalid response: " + red.Text(res.Err.Error()))
	}
	return p.verify(tr, st.Req, &httpx.Result{Status: int(res.Code), Header: header, Body: res.Body, Start: res.Start, End: res.End})
}
