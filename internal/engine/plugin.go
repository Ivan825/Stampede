package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pluginv1 "github.com/Ivan825/Stampede/gen/stampede/plugin/v1"
	"github.com/Ivan825/Stampede/internal/metrics"
	"github.com/Ivan825/Stampede/internal/pluginhost"
	"github.com/Ivan825/Stampede/internal/protocol/httpx"
	"github.com/Ivan825/Stampede/internal/scenario"
)

// maxClassLen bounds an error class a plugin reports.
const maxClassLen = 64

// preparePlugins starts the plugins the program uses and checks every
// plugin step against them, so a missing plugin or a bad config fails
// the run before it starts.
func (e *Engine) preparePlugins() error {
	names := e.prog.PluginNames()
	if len(names) == 0 {
		return nil
	}
	set, err := pluginhost.Load(context.Background(), e.opts.PluginDir, names, e.log)
	if err != nil {
		return fmt.Errorf("this scenario uses plugin steps: %w", err)
	}
	if err := pluginhost.CheckProgram(e.prog, set); err != nil {
		set.Kill()
		return err
	}
	e.plugins = set
	return nil
}

// closePlugins stops the plugin processes once no user needs them.
func (e *Engine) closePlugins() {
	e.pluginOnce.Do(func() {
		if e.plugins != nil {
			e.plugins.Kill()
		}
	})
}

// pluginSession returns the user's session in a plugin, opening one on
// first use and again after the plugin process was restarted.
func (v *VU) pluginSession(ctx context.Context, p *pluginhost.Plugin) (*pluginhost.Session, error) {
	if s := v.plugins[p.Name]; s != nil && !s.Stale() {
		return s, nil
	}
	s, err := p.Open(ctx, int64(v.ID), v.e.opts.RunID)
	if err != nil {
		return nil, err
	}
	if v.plugins == nil {
		v.plugins = map[string]*pluginhost.Session{}
	}
	v.plugins[p.Name] = s
	return s, nil
}

// closePluginSessions ends the user's sessions when it retires.
func (v *VU) closePluginSessions() {
	for name, s := range v.plugins {
		s.Close(context.Background())
		delete(v.plugins, name)
	}
}

// pluginStep runs a step implemented by a plugin. The plugin measures the
// step's latency; the sample ends when the call returns, so in open-model
// runs the time to reach the plugin counts as queueing, never as service
// time. Checks and extractors see the step's returned values as the
// response body.
func (v *VU) pluginStep(ctx context.Context, st *scenario.CStep, intended time.Time) error {
	cp := st.Plugin
	run := v.begin(st, intended)
	run.s.Proto = cp.Plugin
	p := v.e.plugins[cp.Plugin]
	stepType := p.Steps[cp.Step]

	val, err := cp.With.Value(v.vars)
	if err != nil {
		return run.fail("template error", err)
	}
	cfg, _ := val.(map[string]any)
	if err := v.pluginTargetsAllowed(&run, stepType, cfg); err != nil {
		return err
	}
	body, err := json.Marshal(cfg)
	if err != nil {
		return run.fail("template error", err)
	}

	sess, err := v.pluginSession(ctx, p)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return run.fail(pluginCallClass(err), err)
	}
	tp, baggage := v.traceContext()
	start := time.Now()
	resp, err := sess.Execute(ctx, &pluginv1.ExecuteRequest{
		Step: cp.Step, Config: body, TimeoutNs: int64(v.stepTimeout(st.Req.Timeout)),
		Traceparent: tp, Baggage: baggage, Iteration: v.iter,
	})
	end := time.Now()
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		run.s.Start, run.s.End = start, end
		return run.fail(pluginCallClass(err), err)
	}

	values := resp.GetValues()
	if len(values) == 0 {
		values = []byte("{}")
	}
	if resp.GetSkipped() {
		// Nothing was done, so nothing is recorded; extractors still run.
		for _, ex := range st.Req.Extract {
			if x, ok := extract(ex, &httpx.Result{Body: values}); ok {
				v.vars.local[ex.Var] = x
			}
		}
		return nil
	}

	lat := min(max(time.Duration(resp.GetLatencyNs()), 0), end.Sub(start))
	run.s.Start, run.s.End = end.Add(-lat), end
	run.s.BytesIn, run.s.BytesOut = resp.GetBytesIn(), resp.GetBytesOut()
	for name, ns := range resp.GetPhasesNs() {
		if i, ok := phaseIndex[name]; ok && ns > 0 {
			run.s.Phases[i] = time.Duration(ns)
		}
	}
	if n := resp.GetEvents(); n > 0 {
		run.s.Events = int(n)
		if first := run.s.Phases[metrics.PhaseFirstEvent]; first > 0 && first <= lat {
			run.s.StreamTime = lat - first
		}
	}
	if !resp.GetOk() {
		var detail error
		if resp.GetError() != "" {
			detail = errors.New(resp.GetError())
		}
		return run.fail(boundClass(resp.GetErrorClass()), detail)
	}
	return v.verify(&run, st.Req, &httpx.Result{Body: values, Start: run.s.Start, End: run.s.End})
}

// pluginTargetsAllowed applies the target policy to the config properties
// the plugin marks as addresses.
func (v *VU) pluginTargetsAllowed(run *stepRun, st *pluginhost.StepType, cfg map[string]any) error {
	if v.e.allowHost == nil || len(st.Targets) == 0 {
		return nil
	}
	for _, field := range st.Targets {
		hosts, err := pluginhost.TargetHosts(cfg[field])
		if err != nil {
			return run.fail("blocked by safety", fmt.Errorf("%s: %w", field, err))
		}
		for _, h := range hosts {
			if !v.e.allowHost(&url.URL{Scheme: "tcp", Host: hostPort(h)}) {
				return run.fail("blocked by safety", fmt.Errorf("host %s is not an allowed target", h))
			}
		}
	}
	return nil
}

// hostPort puts brackets around an IPv6 address so url.URL reads it.
func hostPort(h string) string {
	if strings.Contains(h, ":") {
		return "[" + h + "]"
	}
	return h
}

var phaseIndex = func() map[string]metrics.Phase {
	m := map[string]metrics.Phase{}
	for i, n := range metrics.PhaseNames {
		m[n] = metrics.Phase(i)
	}
	return m
}()

// pluginCallClass labels a call that did not reach the step.
func pluginCallClass(err error) string {
	switch {
	case errors.Is(err, pluginhost.ErrCrashed):
		return "plugin crashed"
	case status.Code(err) == codes.DeadlineExceeded, errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	}
	return "plugin unavailable"
}

// boundClass keeps a plugin's error class short and on one line, so a
// misbehaving plugin cannot flood reports with distinct labels.
func boundClass(c string) string {
	c = strings.Join(strings.Fields(c), " ")
	if c == "" {
		return "plugin error"
	}
	if len(c) > maxClassLen {
		c = strings.ToValidUTF8(c[:maxClassLen], "")
	}
	return c
}
