// Package conformance checks a built plugin against Stampede's plugin
// contract. Plugin authors run it from a test, pointing it at the plugin
// executable and at a few step calls that should work against a test
// server they start:
//
//	func TestConformance(t *testing.T) {
//		addr := startFakeServer(t)
//		bin := conformance.Build(t, ".", "echo")
//		conformance.Run(t, bin, conformance.Options{
//			Cases: []conformance.Case{
//				{Step: "say", Config: map[string]any{"text": "hi", "addr": addr}},
//			},
//		})
//	}
//
// The suite starts the plugin the way Stampede does (a child process
// speaking gRPC through hashicorp/go-plugin) and checks that:
//
//   - it describes itself validly: name, version, unique step names, and a
//     config schema for every step that compiles and accepts the cases;
//   - sessions follow the lifecycle: open, execute, close, close again,
//     and a closed or unknown session fails cleanly;
//   - bad input fails cleanly, as a failed step with a short error class
//     rather than an RPC error or a crash: unknown steps, configs that are
//     not objects, configs with unknown or missing settings;
//   - it honours timeouts: a call with a tiny timeout comes back promptly;
//   - it is safe under concurrency: many virtual users with their own
//     sessions run the cases at once;
//   - it never dies: the process is still running at the end.
package conformance

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hashicorp/go-hclog"
	"github.com/hashicorp/go-plugin"

	pluginv1 "github.com/Ivan825/Stampede/gen/stampede/plugin/v1"
	"github.com/Ivan825/Stampede/pkg/pluginsdk"
)

// Options configures the suite.
type Options struct {
	// Cases are step calls to make. Those without WantClass must succeed
	// against the environment the test set up; at least one is required.
	Cases []Case
	// VUs is how many virtual users run the cases at once (default 50).
	VUs int
	// Iterations is how often each user runs them (default 3).
	Iterations int
	// Timeout bounds each call (default 10s).
	Timeout time.Duration
	// SkipBadInput lists steps the suite must not call with bad configs,
	// for a step whose schema accepts any object and that does something
	// drastic with it. Every other step is called with configs that are
	// not objects or have unknown settings, and must fail cleanly.
	SkipBadInput []string
}

// Case is one step call.
type Case struct {
	Step string
	// Config is the step's config, marshalled to JSON.
	Config any
	// WantClass, when set, means the call must fail with this error class.
	WantClass string
}

// MaxClassLen is the longest error class the suite accepts.
const MaxClassLen = 64

// Build compiles the plugin in dir with `go build` and returns the path of
// the executable, named stampede-plugin-<name> in a temporary directory.
func Build(t testing.TB, dir, name string) string {
	t.Helper()
	out := filepath.Join(t.TempDir(), "stampede-plugin-"+name)
	cmd := exec.Command("go", "build", "-o", out, ".") //nolint:gosec // builds the plugin under test
	cmd.Dir = dir
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, b)
	}
	return out
}

// Run checks the plugin executable at binary.
func Run(t *testing.T, binary string, opts Options) {
	t.Helper()
	if opts.VUs <= 0 {
		opts.VUs = 50
	}
	if opts.Iterations <= 0 {
		opts.Iterations = 3
	}
	if opts.Timeout <= 0 {
		opts.Timeout = 10 * time.Second
	}
	if len(opts.Cases) == 0 {
		t.Fatal("conformance: give at least one case")
	}
	client := plugin.NewClient(&plugin.ClientConfig{
		HandshakeConfig:  pluginsdk.Handshake,
		Plugins:          plugin.PluginSet{pluginsdk.PluginKey: &pluginsdk.GRPCPlugin{}},
		Cmd:              exec.Command(binary),
		AllowedProtocols: []plugin.Protocol{plugin.ProtocolGRPC},
		StartTimeout:     30 * time.Second,
		Logger:           hclog.New(&hclog.LoggerOptions{Name: "plugin", Level: hclog.Warn, Output: os.Stderr}),
	})
	t.Cleanup(client.Kill)
	rpc, err := client.Client()
	if err != nil {
		t.Fatalf("starting the plugin: %v", err)
	}
	raw, err := rpc.Dispense(pluginsdk.PluginKey)
	if err != nil {
		t.Fatalf("dispensing the plugin: %v", err)
	}
	s := &suite{t: t, svc: raw.(pluginv1.PluginServiceClient), opts: opts, binary: binary}

	t.Run("describe", s.describe)
	if s.desc == nil {
		t.FailNow()
	}
	t.Run("lifecycle", s.lifecycle)
	t.Run("clean errors", s.cleanErrors)
	t.Run("timeouts", s.timeouts)
	t.Run("concurrency", s.concurrency)
	t.Run("still running", func(t *testing.T) {
		if client.Exited() {
			t.Fatal("the plugin process exited during the suite")
		}
		if _, err := s.svc.Describe(context.Background(), &pluginv1.DescribeRequest{}); err != nil {
			t.Fatalf("describe after the suite: %v", err)
		}
	})
}

type suite struct {
	t      *testing.T
	svc    pluginv1.PluginServiceClient
	opts   Options
	binary string
	desc   *pluginv1.DescribeResponse
}

func (s *suite) describe(t *testing.T) {
	d, err := s.svc.Describe(context.Background(), &pluginv1.DescribeRequest{HostVersion: "conformance"})
	if err != nil {
		t.Fatalf("describe: %v", err)
	}
	if err := pluginsdk.ValidateDescription(d); err != nil {
		t.Fatalf("invalid description: %v", err)
	}
	base := strings.TrimSuffix(filepath.Base(s.binary), ".exe")
	if want := "stampede-plugin-" + d.GetName(); strings.HasPrefix(base, "stampede-plugin-") && base != want {
		t.Errorf("the executable is %s but the plugin calls itself %q; Stampede looks for %s", base, d.GetName(), want)
	}
	steps := map[string]*pluginv1.StepType{}
	for _, st := range d.GetSteps() {
		steps[st.GetName()] = st
		if strings.TrimSpace(st.GetDescription()) == "" {
			t.Logf("step %s has no description (shown by `stampede plugin list`)", st.GetName())
		}
	}
	for i, c := range s.opts.Cases {
		st := steps[c.Step]
		if st == nil {
			t.Fatalf("case %d: the plugin has no step %q", i, c.Step)
		}
		sch, _ := pluginsdk.CompileSchema(st.GetName(), st.GetConfigSchema())
		if err := pluginsdk.ValidateConfig(sch, mustJSON(t, c.Config)); err != nil && c.WantClass == "" {
			t.Fatalf("case %d (%s): its config does not fit the step's schema: %v", i, c.Step, err)
		}
	}
	s.desc = d
}

func (s *suite) open(t *testing.T, vu int64) string {
	t.Helper()
	r, err := s.svc.Open(context.Background(), &pluginv1.OpenRequest{Vu: vu, RunId: "conformance"})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if r.GetSession() == "" {
		t.Fatal("open returned an empty session id")
	}
	return r.GetSession()
}

func (s *suite) exec(t *testing.T, session, step string, config []byte, timeout time.Duration) *pluginv1.ExecuteResponse {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout+5*time.Second)
	defer cancel()
	r, err := s.svc.Execute(ctx, &pluginv1.ExecuteRequest{
		Session: session, Step: step, Config: config, TimeoutNs: int64(timeout), Iteration: 1,
		Traceparent: "00-0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331-01",
	})
	if err != nil {
		t.Fatalf("execute %s: an RPC error instead of a failed step: %v", step, err)
	}
	checkResponse(t, step, r)
	return r
}

// checkResponse checks what every response must look like.
func checkResponse(t *testing.T, step string, r *pluginv1.ExecuteResponse) {
	t.Helper()
	if !r.GetOk() {
		c := r.GetErrorClass()
		if c == "" {
			t.Errorf("%s failed without an error class", step)
		}
		if len(c) > MaxClassLen || strings.ContainsAny(c, "\n\r") {
			t.Errorf("%s: error class %q is not a short one-line label", step, c)
		}
	}
	if r.GetLatencyNs() < 0 || r.GetBytesIn() < 0 || r.GetBytesOut() < 0 || r.GetEvents() < 0 {
		t.Errorf("%s: negative measurements: %+v", step, r)
	}
	for name, ns := range r.GetPhasesNs() {
		if !validPhase(name) || ns < 0 {
			t.Errorf("%s: phase %s = %d is not one of dns, connect, tls, wait, download, firstEvent with a positive duration", step, name, ns)
		}
	}
	if v := r.GetValues(); len(v) > 0 {
		var m map[string]any
		if err := json.Unmarshal(v, &m); err != nil {
			t.Errorf("%s: values are not a JSON object: %v", step, err)
		}
	}
}

func validPhase(name string) bool {
	switch name {
	case "dns", "connect", "tls", "wait", "download", "firstEvent":
		return true
	}
	return false
}

func (s *suite) runCases(t *testing.T, session string) {
	t.Helper()
	for i, c := range s.opts.Cases {
		r := s.exec(t, session, c.Step, mustJSON(t, c.Config), s.opts.Timeout)
		switch {
		case c.WantClass == "" && !r.GetOk():
			t.Errorf("case %d (%s) failed: %s: %s", i, c.Step, r.GetErrorClass(), r.GetError())
		case c.WantClass != "" && r.GetOk():
			t.Errorf("case %d (%s) succeeded; want a failure with class %q", i, c.Step, c.WantClass)
		case c.WantClass != "" && r.GetErrorClass() != c.WantClass:
			t.Errorf("case %d (%s) failed with class %q, want %q (%s)", i, c.Step, r.GetErrorClass(), c.WantClass, r.GetError())
		}
	}
}

func (s *suite) lifecycle(t *testing.T) {
	session := s.open(t, 1)
	s.runCases(t, session)
	ctx := context.Background()
	if _, err := s.svc.Close(ctx, &pluginv1.CloseRequest{Session: session}); err != nil {
		t.Fatalf("close: %v", err)
	}
	if _, err := s.svc.Close(ctx, &pluginv1.CloseRequest{Session: session}); err != nil {
		t.Fatalf("closing a session twice must not fail: %v", err)
	}
	c := s.opts.Cases[0]
	if r := s.exec(t, session, c.Step, mustJSON(t, c.Config), s.opts.Timeout); r.GetOk() {
		t.Error("a step ran in a closed session")
	}
}

func (s *suite) cleanErrors(t *testing.T) {
	session := s.open(t, 2)
	defer func() { _, _ = s.svc.Close(context.Background(), &pluginv1.CloseRequest{Session: session}) }()
	if r := s.exec(t, session, "no-such-step-in-conformance", []byte(`{}`), s.opts.Timeout); r.GetOk() {
		t.Error("an unknown step succeeded")
	}
	if r := s.exec(t, "no-such-session", s.opts.Cases[0].Step, mustJSON(t, s.opts.Cases[0].Config), s.opts.Timeout); r.GetOk() {
		t.Error("a step ran in a session that was never opened")
	}
	for _, st := range s.desc.GetSteps() {
		if slices.Contains(s.opts.SkipBadInput, st.GetName()) {
			continue
		}
		for _, bad := range []string{`[]`, `"text"`, `not json`, `{"stampede_conformance_unknown": {"deep": [1, 2]}}`} {
			// Only the response's shape is checked: a schema may accept
			// anything, in which case the step runs with its defaults.
			s.exec(t, session, st.GetName(), []byte(bad), s.opts.Timeout)
		}
	}
}

func (s *suite) timeouts(t *testing.T) {
	session := s.open(t, 3)
	defer func() { _, _ = s.svc.Close(context.Background(), &pluginv1.CloseRequest{Session: session}) }()
	for _, c := range s.opts.Cases {
		start := time.Now()
		s.exec(t, session, c.Step, mustJSON(t, c.Config), time.Millisecond)
		if took := time.Since(start); took > 3*time.Second {
			t.Errorf("%s with a 1ms timeout took %v", c.Step, took)
		}
	}
}

func (s *suite) concurrency(t *testing.T) {
	var wg sync.WaitGroup
	var mu sync.Mutex
	var failures []string
	for vu := range s.opts.VUs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx := context.Background()
			o, err := s.svc.Open(ctx, &pluginv1.OpenRequest{Vu: int64(100 + vu), RunId: "conformance"})
			if err != nil {
				mu.Lock()
				failures = append(failures, fmt.Sprintf("vu %d: open: %v", vu, err))
				mu.Unlock()
				return
			}
			defer func() { _, _ = s.svc.Close(ctx, &pluginv1.CloseRequest{Session: o.GetSession()}) }()
			for range s.opts.Iterations {
				for _, c := range s.opts.Cases {
					cfg, _ := json.Marshal(c.Config)
					cctx, cancel := context.WithTimeout(ctx, s.opts.Timeout+5*time.Second)
					r, err := s.svc.Execute(cctx, &pluginv1.ExecuteRequest{Session: o.GetSession(), Step: c.Step, Config: cfg, TimeoutNs: int64(s.opts.Timeout)})
					cancel()
					var msg string
					switch {
					case err != nil:
						msg = fmt.Sprintf("vu %d: %s: %v", vu, c.Step, err)
					case c.WantClass == "" && !r.GetOk():
						msg = fmt.Sprintf("vu %d: %s failed: %s: %s", vu, c.Step, r.GetErrorClass(), r.GetError())
					case c.WantClass != "" && r.GetErrorClass() != c.WantClass:
						msg = fmt.Sprintf("vu %d: %s: class %q, want %q", vu, c.Step, r.GetErrorClass(), c.WantClass)
					}
					if msg != "" {
						mu.Lock()
						failures = append(failures, msg)
						mu.Unlock()
						return
					}
				}
			}
		}()
	}
	wg.Wait()
	for i, f := range failures {
		if i == 10 {
			t.Errorf("... and %d more", len(failures)-10)
			break
		}
		t.Error(f)
	}
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	if v == nil {
		return []byte(`{}`)
	}
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("case config: %v", err)
	}
	return b
}
