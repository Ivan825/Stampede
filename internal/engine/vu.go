package engine

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"cel.dev/cel-go/interpreter"
	"github.com/PuerkitoBio/goquery"
	"github.com/andybalholm/cascadia"
	"github.com/tidwall/gjson"
	"golang.org/x/net/html"

	"github.com/Ivan825/Stampede/internal/metrics"
	"github.com/Ivan825/Stampede/internal/pluginhost"
	"github.com/Ivan825/Stampede/internal/protocol/httpx"
	"github.com/Ivan825/Stampede/internal/scenario"
)

// errAbortIteration ends the current iteration early after a failed step.
var errAbortIteration = errors.New("iteration aborted")

// VU is one virtual user. A VU runs one iteration at a time.
type VU struct {
	ID     int
	e      *Engine
	client *http.Client
	vars   *vuVars
	iter   int64
	rng    *rand.Rand
	// trace context for the current iteration
	traceID [16]byte
	// ws is the connection of the ws block being run, if any.
	ws *wsConn
	// plugins holds the user's session in each plugin it has used.
	plugins map[string]*pluginhost.Session
}

func newVU(e *Engine, id int) *VU {
	v := &VU{ID: id, e: e, rng: rand.New(rand.NewPCG(uint64(id), uint64(time.Now().UnixNano())))}
	t := e.sharedTransport
	if t == nil {
		t = httpx.NewTransport(e.httpOpts)
	}
	v.client = httpx.NewClient(t, e.httpOpts)
	v.vars = &vuVars{env: e.opts.Env, secret: e.opts.Secrets, static: e.prog.Scenario.Vars, vu: int64(id)}
	return v
}

// close ends the user's plugin sessions and releases idle connections
// held by a per-VU transport.
func (v *VU) close() {
	v.closePluginSessions()
	if v.e.sharedTransport == nil {
		v.client.CloseIdleConnections()
	}
}

// pickJourney chooses a journey by weight.
func (v *VU) pickJourney() *scenario.CJourney {
	js := v.e.prog.Journeys
	if len(js) == 1 {
		return js[0]
	}
	n := v.rng.IntN(v.e.prog.TotalWeight)
	for _, j := range js {
		if n < j.Weight {
			return j
		}
		n -= j.Weight
	}
	return js[len(js)-1]
}

// runIteration executes one journey. intended is the scheduled start time
// (open model) or zero (closed model).
func (v *VU) runIteration(ctx context.Context, intended time.Time) error {
	return v.runJourney(ctx, intended, v.pickJourney(), nil)
}

// runReplay sends one recorded request through its endpoint's journey.
func (v *VU) runReplay(ctx context.Context, intended time.Time, a *scenario.Arrival) error {
	return v.runJourney(ctx, intended, v.e.prog.Journeys[a.Endpoint], map[string]any{
		"target": a.Target, "body": a.Body, "contentType": a.ContentType,
	})
}

func (v *VU) runJourney(ctx context.Context, intended time.Time, j *scenario.CJourney, replay map[string]any) error {
	start := time.Now()
	lag := time.Duration(0)
	if !intended.IsZero() {
		lag = start.Sub(intended)
	}

	// New session: fresh variables, cookies and feeder rows.
	v.iter++
	v.vars.reset(v.iter)
	v.vars.replay = replay
	httpx.ResetCookies(v.client)
	for _, name := range v.e.journeyFeeders[j.Index] {
		row, err := v.e.feeders[name].next(v.ID)
		if err != nil {
			return err
		}
		v.vars.data[name] = row
	}
	fillRandom(v.rng, v.traceID[:])

	v.e.collector.IterationStarted(v.ID, j.Index, lag)
	err := v.runSteps(ctx, j.Steps, intended)
	ok := err == nil
	if errors.Is(err, errAbortIteration) {
		err = nil
	}
	v.e.collector.IterationDone(v.ID, j.Index, time.Since(start), ok)
	return err
}

// fillRandom fills b from r. Trace IDs need uniqueness, not secrecy.
func fillRandom(r *rand.Rand, b []byte) {
	for i := 0; i < len(b); i += 8 {
		x := r.Uint64()
		for j := 0; j < 8 && i+j < len(b); j++ {
			b[i+j] = byte(x >> (8 * j))
		}
	}
}

func (v *VU) runSteps(ctx context.Context, steps []*scenario.CStep, intended time.Time) error {
	for i, st := range steps {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		// Only the first request of an open-model iteration carries the
		// intended time; later steps start when the previous one ended.
		it := time.Time{}
		if i == 0 {
			it = intended
		}
		if err := v.runStep(ctx, st, it); err != nil {
			return err
		}
	}
	return nil
}

func (v *VU) runStep(ctx context.Context, st *scenario.CStep, intended time.Time) error {
	if st.If != nil {
		okv, err := st.If.EvalBool(v.vars)
		if err != nil {
			v.e.logStepError(st, err)
			return errAbortIteration
		}
		if !okv {
			return nil
		}
	}
	switch st.Kind {
	case scenario.StepRequest:
		return v.request(ctx, st, intended)
	case scenario.StepGraphQL:
		return v.graphql(ctx, st, intended)
	case scenario.StepSSE:
		return v.sse(ctx, st, intended)
	case scenario.StepWS:
		return v.websocket(ctx, st, intended)
	case scenario.StepSend:
		return v.wsSend(ctx, st)
	case scenario.StepExpect:
		return v.wsExpect(ctx, st)
	case scenario.StepGRPC:
		return v.grpcCall(ctx, st, intended)
	case scenario.StepPlugin:
		return v.pluginStep(ctx, st, intended)
	case scenario.StepThink:
		return sleepCtx(ctx, v.think(st.Think))
	case scenario.StepBranch:
		n := v.rng.IntN(st.BranchTotal)
		for _, b := range st.Branches {
			if n < b.Weight {
				return v.runSteps(ctx, b.Steps, intended)
			}
			n -= b.Weight
		}
	case scenario.StepLoop:
		for i := 0; i < st.Loop.Count; i++ {
			if err := v.runSteps(ctx, st.Steps, time.Time{}); err != nil {
				return err
			}
		}
	case scenario.StepWhile:
		for i := 0; i < st.Loop.Max; i++ {
			okv, err := st.Loop.Cond.EvalBool(v.vars)
			if err != nil {
				v.e.logStepError(st, err)
				return errAbortIteration
			}
			if !okv {
				break
			}
			if err := v.runSteps(ctx, st.Steps, time.Time{}); err != nil {
				return err
			}
		}
	case scenario.StepGroup:
		return v.runSteps(ctx, st.Steps, intended)
	}
	return nil
}

func (v *VU) think(t *scenario.ThinkTime) time.Duration {
	if t.Max <= t.Min {
		return t.Min.D()
	}
	return t.Min.D() + time.Duration(v.rng.Int64N(int64(t.Max-t.Min)+1))
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// stepRun records the outcome of one step as a single sample. Every step
// kind that talks to the target goes through it, so failures are
// recorded, labelled and logged the same way.
type stepRun struct {
	v  *VU
	st *scenario.CStep
	s  metrics.Sample
}

func (v *VU) begin(st *scenario.CStep, intended time.Time) stepRun {
	return stepRun{v: v, st: st, s: metrics.Sample{Step: st.ID, Intended: intended, TraceID: v.traceID}}
}

// fail records the step as failed with a bounded error class, logs err
// (rate-limited) when given, and aborts the iteration: a real user would
// not carry on after an error.
func (r *stepRun) fail(class string, err error) error {
	now := time.Now()
	if r.s.Start.IsZero() {
		r.s.Start = now
	}
	if r.s.End.IsZero() {
		r.s.End = now
	}
	r.s.Err, r.s.Failed = class, true
	r.v.e.collector.Record(r.v.ID, &r.s)
	if err != nil {
		r.v.e.logStepError(r.st, err)
	}
	return errAbortIteration
}

// done records the step as successful.
func (r *stepRun) done() error {
	r.v.e.collector.Record(r.v.ID, &r.s)
	return nil
}

// fromHTTP copies an exchange's timings and sizes into the sample.
func (r *stepRun) fromHTTP(res *httpx.Result) {
	r.s.Start, r.s.End, r.s.Phases = res.Start, res.End, res.Phases
	r.s.Status, r.s.BytesIn, r.s.BytesOut = res.Status, res.BytesIn, res.BytesOut
	r.s.Proto = res.Proto
}

// blocked reports a target outside the safety policy as a failure.
func (r *stepRun) blocked(u *url.URL) error {
	if r.v.e.allowHost != nil && !r.v.e.allowHost(u) {
		return r.fail("blocked by safety", fmt.Errorf("host %s is not an allowed target", u.Host))
	}
	return nil
}

// stepTimeout is a step's own timeout or the target default.
func (v *VU) stepTimeout(d scenario.Duration) time.Duration {
	if d > 0 {
		return d.D()
	}
	return v.e.prog.Scenario.Target.Timeout.D()
}

// request performs one HTTP step and records its sample. A failed step
// aborts the iteration, as a real user would not carry on after an error.
func (v *VU) request(ctx context.Context, st *scenario.CStep, intended time.Time) error {
	r := st.Req
	run := v.begin(st, intended)

	req, bodyLen, err := v.buildRequest(ctx, r)
	if err != nil {
		return run.fail("template error", err)
	}
	if err := run.blocked(req.URL); err != nil {
		return err
	}
	rctx, cancel := context.WithTimeout(ctx, v.stepTimeout(r.Timeout))
	defer cancel()
	req = req.WithContext(rctx)

	keepBody := len(r.Extract) > 0 || r.Check != nil && (r.Check.BodyContains != nil || r.Check.NeedsJSON || r.Check.Expr != nil)
	res := httpx.Do(v.client, req, bodyLen, keepBody, v.e.maxBody)
	run.fromHTTP(res)
	if res.Err != nil {
		if ctx.Err() != nil {
			// The run is stopping; do not count an aborted request.
			return ctx.Err()
		}
		return run.fail(httpx.ClassifyError(res.Err), nil)
	}
	return v.verify(&run, r, res)
}

// verify applies a step's checks (or the default status rule) and
// extractors to a response, then records the sample.
func (v *VU) verify(run *stepRun, r *scenario.CRequest, res *httpx.Result) error {
	if r.Check != nil {
		if label := v.check(r.Check, res, &run.s); label != "" {
			return run.fail(label, nil)
		}
	} else if res.Status >= 400 {
		return run.fail(httpx.StatusError(res.Status), nil)
	}
	if err := v.extractAll(run, r.Extract, res); err != nil {
		return err
	}
	return run.done()
}

// extractAll stores every extracted variable, failing the step on the
// first that is missing.
func (v *VU) extractAll(run *stepRun, exs []scenario.Extractor, res *httpx.Result) error {
	for _, ex := range exs {
		val, ok := extract(ex, res)
		if !ok {
			return run.fail("extract "+ex.Var, nil)
		}
		v.vars.local[ex.Var] = val
	}
	return nil
}

// resolveURL renders a step URL, joins relative paths to the base URL
// and adds templated query parameters.
func (v *VU) resolveURL(tmpl *scenario.Template, query []scenario.KV) (*url.URL, error) {
	raw, err := tmpl.Render(v.vars)
	if err != nil {
		return nil, err
	}
	if !scenario.IsAbsoluteURL(raw) {
		raw = strings.TrimRight(v.e.baseURL, "/") + "/" + strings.TrimLeft(raw, "/")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("invalid URL %q: %w", raw, err)
	}
	if len(query) > 0 {
		q := u.Query()
		for _, kv := range query {
			s, err := kv.Value.Render(v.vars)
			if err != nil {
				return nil, err
			}
			q.Add(kv.Name, s)
		}
		u.RawQuery = q.Encode()
	}
	return u, nil
}

// renderHeaders sets the engine, target and step headers on h, in that
// order so the most specific wins.
func (v *VU) renderHeaders(h http.Header, step []scenario.KV) error {
	for _, kv := range v.e.headers {
		h.Set(kv.Name, kv.Value)
	}
	for _, kvs := range [][]scenario.KV{v.e.prog.Headers, step} {
		for _, kv := range kvs {
			s, err := kv.Value.Render(v.vars)
			if err != nil {
				return err
			}
			h.Set(kv.Name, s)
		}
	}
	return nil
}

func (v *VU) buildRequest(ctx context.Context, r *scenario.CRequest) (*http.Request, int64, error) {
	u, err := v.resolveURL(r.URL, r.Query)
	if err != nil {
		return nil, 0, err
	}

	var body io.Reader
	var bodyLen int64
	contentType := ""
	switch {
	case r.JSON != nil:
		val, err := r.JSON.Value(v.vars)
		if err != nil {
			return nil, 0, err
		}
		b, err := json.Marshal(val)
		if err != nil {
			return nil, 0, err
		}
		body, bodyLen, contentType = bytes.NewReader(b), int64(len(b)), "application/json"
	case len(r.Form) > 0:
		f := url.Values{}
		for _, kv := range r.Form {
			s, err := kv.Value.Render(v.vars)
			if err != nil {
				return nil, 0, err
			}
			f.Add(kv.Name, s)
		}
		enc := f.Encode()
		body, bodyLen, contentType = strings.NewReader(enc), int64(len(enc)), "application/x-www-form-urlencoded"
	case r.Body != nil:
		s, err := r.Body.Render(v.vars)
		if err != nil {
			return nil, 0, err
		}
		body, bodyLen = strings.NewReader(s), int64(len(s))
	}
	req, err := v.newRequest(ctx, r.Method, u, body, contentType, r.Headers)
	return req, bodyLen, err
}

// newRequest builds a request with Stampede's user agent, the scenario's
// headers and trace context.
func (v *VU) newRequest(ctx context.Context, method string, u *url.URL, body io.Reader, contentType string, headers []scenario.KV) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, u.String(), body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", v.e.userAgent)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if err := v.renderHeaders(req.Header, headers); err != nil {
		return nil, err
	}
	v.addTraceHeaders(req.Header)
	return req, nil
}

// addTraceHeaders propagates W3C trace context and tags each request with
// the run, journey and step so any OpenTelemetry backend can filter by run.
func (v *VU) addTraceHeaders(h http.Header) {
	if h.Get("traceparent") != "" {
		return
	}
	tp, baggage := v.traceContext()
	h.Set("traceparent", tp)
	if baggage != "" {
		h.Set("baggage", baggage)
	}
}

// traceContext returns a W3C traceparent for a new span in the
// iteration's trace, and the run baggage. gRPC sends them as metadata.
func (v *VU) traceContext() (traceparent, baggage string) {
	var span [8]byte
	fillRandom(v.rng, span[:])
	traceparent = "00-" + hex.EncodeToString(v.traceID[:]) + "-" + hex.EncodeToString(span[:]) + "-01"
	if v.e.opts.RunID != "" {
		baggage = "stampede.run_id=" + url.QueryEscape(v.e.opts.RunID) + ",stampede.vu=" + strconv.Itoa(v.ID)
	}
	return traceparent, baggage
}

// check evaluates a response check and returns a failure label or "".
func (v *VU) check(c *scenario.CCheck, res *httpx.Result, s *metrics.Sample) string {
	fail := func(what string) string {
		s.ChecksFailed++
		return "check " + what
	}
	if !c.Status.Empty() {
		if !c.Status.Match(res.Status) {
			if res.Status >= 400 {
				return fail("status (" + httpx.StatusError(res.Status) + ")")
			}
			return fail("status (got " + strconv.Itoa(res.Status) + ")")
		}
		s.ChecksPassed++
	} else if res.Status >= 400 {
		return fail("status (" + httpx.StatusError(res.Status) + ")")
	}
	if c.MaxLatency > 0 {
		if res.End.Sub(res.Start) > c.MaxLatency.D() {
			return fail("latency")
		}
		s.ChecksPassed++
	}
	if c.BodyContains != nil {
		want, err := c.BodyContains.Render(v.vars)
		if err != nil || !bytes.Contains(res.Body, []byte(want)) {
			return fail("body")
		}
		s.ChecksPassed++
	}
	for _, jc := range c.JSON {
		got := gjson.GetBytes(res.Body, jc.GJSON)
		if !got.Exists() {
			return fail("json " + jc.Path)
		}
		if !jc.Exists && !jsonEqual(got.Value(), jc.Expected) {
			return fail("json " + jc.Path)
		}
		s.ChecksPassed++
	}
	if c.Expr != nil {
		ra := &respVars{parent: v.vars, res: res}
		ok, err := c.Expr.EvalBool(ra)
		if err != nil || !ok {
			return fail("expr")
		}
		s.ChecksPassed++
	}
	return ""
}

func jsonEqual(got, want any) bool {
	// Compare through JSON so 1 (int from YAML) equals 1.0 (float from JSON).
	g, _ := json.Marshal(got)
	w, _ := json.Marshal(want)
	if bytes.Equal(g, w) {
		return true
	}
	gf, gok := toFloat(got)
	wf, wok := toFloat(want)
	return gok && wok && gf == wf
}

func toFloat(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case int:
		return float64(x), true
	case int64:
		return float64(x), true
	case uint64:
		return float64(x), true
	}
	return 0, false
}

// extract pulls a variable out of a response.
func extract(ex scenario.Extractor, res *httpx.Result) (any, bool) {
	switch ex.Kind {
	case scenario.ExtractJSON:
		g := gjson.GetBytes(res.Body, ex.GJSON)
		if !g.Exists() {
			return nil, false
		}
		return gjsonValue(g), true
	case scenario.ExtractHeader:
		h := res.Header.Get(ex.Src)
		return h, h != ""
	case scenario.ExtractCookie:
		for _, c := range res.Cookies {
			if c.Name == ex.Src {
				return c.Value, true
			}
		}
		return nil, false
	case scenario.ExtractRegex:
		m := ex.Regex.FindSubmatch(res.Body)
		if m == nil {
			return nil, false
		}
		if len(m) > 1 {
			return string(m[1]), true
		}
		return string(m[0]), true
	case scenario.ExtractCSS:
		doc, err := goquery.NewDocumentFromReader(bytes.NewReader(res.Body))
		if err != nil {
			return nil, false
		}
		sel := doc.FindMatcher(cssMatcher{ex.CSS}).First()
		if sel.Length() == 0 {
			return nil, false
		}
		if ex.Attr != "" {
			return sel.Attr(ex.Attr)
		}
		return strings.TrimSpace(sel.Text()), true
	case scenario.ExtractStatus:
		return int64(res.Status), true
	case scenario.ExtractBody:
		return string(res.Body), true
	}
	return nil, false
}

func gjsonValue(g gjson.Result) any {
	switch g.Type {
	case gjson.Number:
		if f := g.Float(); f == float64(int64(f)) && !strings.ContainsAny(g.Raw, ".eE") {
			return g.Int()
		}
		return g.Float()
	case gjson.String:
		return g.Str
	case gjson.True:
		return true
	case gjson.False:
		return false
	case gjson.Null:
		return nil
	default:
		return g.Value()
	}
}

// vuVars is the expression activation for a virtual user.
type vuVars struct {
	env    map[string]string
	secret map[string]string
	static map[string]any
	data   map[string]any
	local  map[string]any
	vu     int64
	iter   int64
	// replay is the recorded request of a replay iteration.
	replay map[string]any
}

func (a *vuVars) reset(iter int64) {
	a.iter = iter
	a.local = make(map[string]any, len(a.static)+4)
	for k, v := range a.static {
		a.local[k] = v
	}
	a.data = map[string]any{}
}

func (a *vuVars) ResolveName(name string) (any, bool) {
	switch name {
	case "env":
		return a.env, true
	case "secret":
		return a.secret, true
	case "data":
		return a.data, true
	case "vars":
		return a.static, true
	case "vu":
		return a.vu, true
	case "iter":
		return a.iter, true
	case "replay":
		return a.replay, a.replay != nil
	}
	v, ok := a.local[name]
	return v, ok
}

func (a *vuVars) Parent() interpreter.Activation { return nil }

// respVars adds response variables for check expressions.
type respVars struct {
	parent *vuVars
	res    *httpx.Result
	json   any
	parsed bool
}

func (r *respVars) ResolveName(name string) (any, bool) {
	switch name {
	case "status":
		return int64(r.res.Status), true
	case "body":
		return string(r.res.Body), true
	case "latencyMs":
		return float64(r.res.End.Sub(r.res.Start)) / 1e6, true
	case "headers":
		h := make(map[string]string, len(r.res.Header))
		for k := range r.res.Header {
			h[strings.ToLower(k)] = r.res.Header.Get(k)
		}
		return h, true
	case "json":
		if !r.parsed {
			r.parsed = true
			_ = json.Unmarshal(r.res.Body, &r.json)
		}
		return r.json, true
	}
	return r.parent.ResolveName(name)
}

func (r *respVars) Parent() interpreter.Activation { return nil }

// cssMatcher adapts a cascadia selector to goquery's Matcher.
type cssMatcher struct{ cascadia.Sel }

func (m cssMatcher) MatchAll(n *html.Node) []*html.Node { return cascadia.QueryAll(n, m.Sel) }

func (m cssMatcher) Filter(ns []*html.Node) []*html.Node { return cascadia.Filter(ns, m.Sel) }
