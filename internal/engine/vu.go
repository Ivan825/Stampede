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

// close releases idle connections held by a per-VU transport.
func (v *VU) close() {
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
	j := v.pickJourney()
	start := time.Now()
	lag := time.Duration(0)
	if !intended.IsZero() {
		lag = start.Sub(intended)
	}

	// New session: fresh variables, cookies and feeder rows.
	v.iter++
	v.vars.reset(v.iter)
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

// request performs one HTTP step and records its sample. A failed step
// aborts the iteration, as a real user would not carry on after an error.
func (v *VU) request(ctx context.Context, st *scenario.CStep, intended time.Time) error {
	r := st.Req
	sample := metrics.Sample{Step: st.ID, Intended: intended}
	fail := func(class string, err error) error {
		now := time.Now()
		if sample.Start.IsZero() {
			sample.Start = now
		}
		if sample.End.IsZero() {
			sample.End = now
		}
		sample.Err, sample.Failed = class, true
		v.e.collector.Record(v.ID, &sample)
		if err != nil {
			v.e.logStepError(st, err)
		}
		return errAbortIteration
	}

	req, bodyLen, err := v.buildRequest(ctx, r)
	if err != nil {
		return fail("template error", err)
	}
	if v.e.allowHost != nil && !v.e.allowHost(req.URL) {
		return fail("blocked by safety", fmt.Errorf("host %s is not an allowed target", req.URL.Host))
	}

	timeout := r.Timeout.D()
	if timeout == 0 {
		timeout = v.e.prog.Scenario.Target.Timeout.D()
	}
	rctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req = req.WithContext(rctx)

	keepBody := len(r.Extract) > 0 || r.Check != nil && (r.Check.BodyContains != nil || r.Check.NeedsJSON || r.Check.Expr != nil)
	res := httpx.Do(v.client, req, bodyLen, keepBody, v.e.maxBody)
	sample.Start, sample.End, sample.Phases = res.Start, res.End, res.Phases
	sample.Status, sample.BytesIn, sample.BytesOut = res.Status, res.BytesIn, res.BytesOut

	if res.Err != nil {
		if ctx.Err() != nil {
			// The run is stopping; do not count an aborted request.
			return ctx.Err()
		}
		return fail(httpx.ClassifyError(res.Err), nil)
	}

	if r.Check != nil {
		if label := v.check(r.Check, res, &sample); label != "" {
			return fail(label, nil)
		}
	} else if res.Status >= 400 {
		return fail(httpx.StatusError(res.Status), nil)
	}

	for _, ex := range r.Extract {
		val, ok := extract(ex, res)
		if !ok {
			return fail("extract "+ex.Var, nil)
		}
		v.vars.local[ex.Var] = val
	}
	v.e.collector.Record(v.ID, &sample)
	return nil
}

func (v *VU) buildRequest(ctx context.Context, r *scenario.CRequest) (*http.Request, int64, error) {
	raw, err := r.URL.Render(v.vars)
	if err != nil {
		return nil, 0, err
	}
	if !strings.HasPrefix(raw, "http://") && !strings.HasPrefix(raw, "https://") {
		raw = strings.TrimRight(v.e.baseURL, "/") + "/" + strings.TrimLeft(raw, "/")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return nil, 0, fmt.Errorf("invalid URL %q: %w", raw, err)
	}
	if len(r.Query) > 0 {
		q := u.Query()
		for _, kv := range r.Query {
			s, err := kv.Value.Render(v.vars)
			if err != nil {
				return nil, 0, err
			}
			q.Add(kv.Name, s)
		}
		u.RawQuery = q.Encode()
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

	req, err := http.NewRequestWithContext(ctx, r.Method, u.String(), body)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("User-Agent", v.e.userAgent)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	for _, kv := range v.e.headers {
		req.Header.Set(kv.Name, kv.Value)
	}
	for _, kvs := range [][]scenario.KV{v.e.prog.Headers, r.Headers} {
		for _, kv := range kvs {
			s, err := kv.Value.Render(v.vars)
			if err != nil {
				return nil, 0, err
			}
			req.Header.Set(kv.Name, s)
		}
	}
	v.addTraceHeaders(req)
	return req, bodyLen, nil
}

// addTraceHeaders propagates W3C trace context and tags each request with
// the run, journey and step so any OpenTelemetry backend can filter by run.
func (v *VU) addTraceHeaders(req *http.Request) {
	if req.Header.Get("traceparent") != "" {
		return
	}
	var span [8]byte
	fillRandom(v.rng, span[:])
	req.Header.Set("traceparent", "00-"+hex.EncodeToString(v.traceID[:])+"-"+hex.EncodeToString(span[:])+"-01")
	if v.e.opts.RunID != "" {
		req.Header.Set("baggage", "stampede.run_id="+url.QueryEscape(v.e.opts.RunID)+",stampede.vu="+strconv.Itoa(v.ID))
	}
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
