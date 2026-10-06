package pluginsdk

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"runtime/debug"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/hashicorp/go-hclog"
	"github.com/hashicorp/go-plugin"
	"github.com/santhosh-tekuri/jsonschema/v6"
	"google.golang.org/grpc"

	pluginv1 "github.com/Ivan825/Stampede/gen/stampede/plugin/v1"
)

// Handshake is shared by Stampede and every plugin. ProtocolVersion is
// the plugin protocol's major version (proto/stampede/plugin/v1); a host
// refuses a plugin built for another. The cookie only stops the binary
// from being mistaken for a normal program; it is not a secret.
var Handshake = plugin.HandshakeConfig{
	ProtocolVersion:  1,
	MagicCookieKey:   "STAMPEDE_PLUGIN",
	MagicCookieValue: "d3b5a8c4-stampede-protocol-plugin",
}

// PluginKey names the plugin in go-plugin's plugin set.
const PluginKey = "stampede"

// MaxValuesBytes caps the JSON a step may return.
const MaxValuesBytes = 1 << 20

// GRPCPlugin connects go-plugin to the plugin service. Plugins serve it
// through Serve; the host uses it to dispense a client.
type GRPCPlugin struct {
	plugin.NetRPCUnsupportedPlugin
	// Impl is the plugin's implementation; nil on the host side.
	Impl pluginv1.PluginServiceServer
}

// GRPCServer registers the plugin service.
func (p *GRPCPlugin) GRPCServer(_ *plugin.GRPCBroker, s *grpc.Server) error {
	if p.Impl == nil {
		return errors.New("pluginsdk: no server to register")
	}
	pluginv1.RegisterPluginServiceServer(s, p.Impl)
	return nil
}

// GRPCClient returns a pluginv1.PluginServiceClient.
func (p *GRPCPlugin) GRPCClient(_ context.Context, _ *plugin.GRPCBroker, c *grpc.ClientConn) (any, error) {
	return pluginv1.NewPluginServiceClient(c), nil
}

// Serve runs the plugin until Stampede stops it. It exits the process
// with an error when the plugin's description is invalid.
func Serve(p *Plugin) {
	log := hclog.New(&hclog.LoggerOptions{Name: p.Name, Output: os.Stderr, JSONFormat: true, Level: hclog.Info})
	srv, err := newServer(p, log)
	if err != nil {
		fmt.Fprintf(os.Stderr, "stampede plugin %s: %v\n", p.Name, err)
		os.Exit(1)
	}
	plugin.Serve(&plugin.ServeConfig{
		HandshakeConfig: Handshake,
		Plugins:         plugin.PluginSet{PluginKey: &GRPCPlugin{Impl: srv}},
		GRPCServer:      plugin.DefaultGRPCServer,
		Logger:          log,
	})
}

// NewServer returns the plugin service for p without starting a process,
// for serving it in-process (tests) or over a transport of your own.
func NewServer(p *Plugin) (pluginv1.PluginServiceServer, error) {
	return newServer(p, hclog.NewNullLogger())
}

type server struct {
	pluginv1.UnimplementedPluginServiceServer
	p     *Plugin
	log   hclog.Logger
	desc  *pluginv1.DescribeResponse
	steps map[string]*compiledStep

	next     atomic.Uint64
	mu       sync.Mutex
	sessions map[string]*session
}

type compiledStep struct {
	step   *Step
	schema *jsonschema.Schema
}

type session struct {
	mu     sync.Mutex
	value  any
	vu     int64
	closed bool
}

func newServer(p *Plugin, log hclog.Logger) (*server, error) {
	s := &server{p: p, log: log, steps: map[string]*compiledStep{}, sessions: map[string]*session{}}
	s.desc = &pluginv1.DescribeResponse{Name: p.Name, Version: p.Version, Description: p.Description}
	for i := range p.Steps {
		st := &p.Steps[i]
		if st.Run == nil {
			return nil, fmt.Errorf("step %s has no Run function", st.Name)
		}
		s.desc.Steps = append(s.desc.Steps, &pluginv1.StepType{
			Name: st.Name, Description: st.Description, ConfigSchema: []byte(st.Schema),
		})
	}
	if err := ValidateDescription(s.desc); err != nil {
		return nil, err
	}
	for i := range p.Steps {
		st := &p.Steps[i]
		sch, _ := CompileSchema(st.Name, []byte(st.Schema))
		s.steps[st.Name] = &compiledStep{step: st, schema: sch}
	}
	return s, nil
}

func (s *server) Describe(context.Context, *pluginv1.DescribeRequest) (*pluginv1.DescribeResponse, error) {
	return s.desc, nil
}

func (s *server) Open(ctx context.Context, req *pluginv1.OpenRequest) (resp *pluginv1.OpenResponse, err error) {
	defer s.recoverInto("open", &err)
	sess := &session{vu: req.GetVu()}
	if s.p.NewSession != nil {
		v, err := s.p.NewSession(ctx, SessionInfo{VU: req.GetVu(), RunID: req.GetRunId()})
		if err != nil {
			return nil, err
		}
		sess.value = v
	}
	id := strconv.FormatUint(s.next.Add(1), 10)
	s.mu.Lock()
	s.sessions[id] = sess
	s.mu.Unlock()
	return &pluginv1.OpenResponse{Session: id}, nil
}

func (s *server) Close(_ context.Context, req *pluginv1.CloseRequest) (resp *pluginv1.CloseResponse, err error) {
	defer s.recoverInto("close", &err)
	s.mu.Lock()
	sess := s.sessions[req.GetSession()]
	delete(s.sessions, req.GetSession())
	s.mu.Unlock()
	if sess == nil {
		return &pluginv1.CloseResponse{}, nil
	}
	sess.mu.Lock()
	defer sess.mu.Unlock()
	sess.closed = true
	if c, ok := sess.value.(io.Closer); ok {
		if err := c.Close(); err != nil {
			s.log.Warn("closing session", "vu", sess.vu, "error", err)
		}
	}
	return &pluginv1.CloseResponse{}, nil
}

// recoverInto turns a panic in a plugin's own code into an RPC error, so
// one bad call does not end the process.
func (s *server) recoverInto(what string, err *error) {
	if r := recover(); r != nil {
		s.log.Error("plugin panicked", "in", what, "panic", fmt.Sprint(r), "stack", string(debug.Stack()))
		*err = fmt.Errorf("plugin panicked in %s: %v", what, r)
	}
}

func failed(class string, err error) *pluginv1.ExecuteResponse {
	r := &pluginv1.ExecuteResponse{ErrorClass: class}
	if err != nil {
		r.Error = err.Error()
	}
	return r
}

func (s *server) Execute(ctx context.Context, req *pluginv1.ExecuteRequest) (*pluginv1.ExecuteResponse, error) {
	st := s.steps[req.GetStep()]
	if st == nil {
		return failed("unknown step", fmt.Errorf("plugin %s has no step %q", s.p.Name, req.GetStep())), nil
	}
	s.mu.Lock()
	sess := s.sessions[req.GetSession()]
	s.mu.Unlock()
	if sess == nil {
		return failed("unknown session", fmt.Errorf("session %q is not open", req.GetSession())), nil
	}
	if err := ValidateConfig(st.schema, req.GetConfig()); err != nil {
		return failed("invalid config", err), nil
	}

	timeout := time.Duration(req.GetTimeoutNs())
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	call := &Call{
		Step: req.GetStep(), VU: sess.vu, Iteration: req.GetIteration(),
		Config: req.GetConfig(), Timeout: timeout,
		Traceparent: req.GetTraceparent(), Baggage: req.GetBaggage(),
	}

	sess.mu.Lock()
	defer sess.mu.Unlock()
	if sess.closed {
		return failed("unknown session", errors.New("session is closed")), nil
	}
	call.Session = sess.value
	start := time.Now()
	res, err := s.run(cctx, st.step, call)
	took := time.Since(start)
	return s.response(res, err, took), nil
}

// run calls the step, turning a panic into a failure.
func (s *server) run(ctx context.Context, st *Step, c *Call) (res *Result, err error) {
	defer func() {
		if r := recover(); r != nil {
			s.log.Error("step panicked", "step", st.Name, "panic", fmt.Sprint(r), "stack", string(debug.Stack()))
			res, err = nil, Failf("plugin panic", "%v", r)
		}
	}()
	return st.Run(ctx, c)
}

func (s *server) response(res *Result, err error, took time.Duration) *pluginv1.ExecuteResponse {
	out := &pluginv1.ExecuteResponse{Ok: err == nil, LatencyNs: int64(took)}
	if err != nil {
		out.ErrorClass, out.Error = classify(s.p.Name, err), err.Error()
	}
	if res == nil {
		return out
	}
	if res.Latency > 0 {
		out.LatencyNs = int64(res.Latency)
	}
	out.PhasesNs = res.Phases.toMap()
	out.BytesIn, out.BytesOut, out.Events, out.Skipped = res.BytesIn, res.BytesOut, res.Events, res.Skipped && err == nil
	if res.Values != nil {
		b, merr := json.Marshal(res.Values)
		switch {
		case merr != nil:
			return failed("invalid values", merr)
		case len(b) == 0 || b[0] != '{':
			return failed("invalid values", errors.New("values must marshal to a JSON object"))
		case len(b) > MaxValuesBytes:
			return failed("values too large", fmt.Errorf("%d bytes of values (limit %d)", len(b), MaxValuesBytes))
		}
		out.Values = b
	}
	return out
}
