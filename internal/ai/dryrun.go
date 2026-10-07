package ai

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"cel.dev/cel-go/interpreter"
	"github.com/PuerkitoBio/goquery"
	"github.com/andybalholm/cascadia"
	"github.com/tidwall/gjson"
	"golang.org/x/net/html"
	"google.golang.org/protobuf/reflect/protoregistry"

	"github.com/Ivan825/Stampede/internal/feed"
	"github.com/Ivan825/Stampede/internal/protocol/grpcx"
	"github.com/Ivan825/Stampede/internal/protocol/httpx"
	"github.com/Ivan825/Stampede/internal/scenario"
	"github.com/Ivan825/Stampede/internal/script"
)

// The dry run executes each journey once with a single user and records
// everything that happened, so a person (and the model, when repairing) can
// see why a journey works or not. It deliberately does not reuse the load
// engine: the engine records aggregate metrics, while a dry run needs a
// full, redacted trace of every request.

// CheckResult is one assertion on a response.
type CheckResult struct {
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail,omitempty"`
}

// StepTrace records one request of a dry run. Everything is redacted.
type StepTrace struct {
	Step            string            `json:"step"`
	Method          string            `json:"method,omitempty"`
	URL             string            `json:"url,omitempty"`
	RequestHeaders  map[string]string `json:"requestHeaders,omitempty"`
	RequestBody     string            `json:"requestBody,omitempty"`
	Status          int               `json:"status,omitempty"`
	ResponseHeaders map[string]string `json:"responseHeaders,omitempty"`
	ResponseBody    string            `json:"responseBody,omitempty"`
	DurationMs      float64           `json:"durationMs"`
	Extracted       map[string]string `json:"extracted,omitempty"`
	Checks          []CheckResult     `json:"checks,omitempty"`
	OK              bool              `json:"ok"`
	Error           string            `json:"error,omitempty"`
	// Note explains a step that was not executed as a request.
	Note string `json:"note,omitempty"`
}

// Trace is one pass through a journey.
type Trace struct {
	// Pass numbers passes from 1. A journey with branches gets one pass
	// per alternative so every branch is exercised.
	Pass     int         `json:"pass"`
	Branches []string    `json:"branches,omitempty"`
	OK       bool        `json:"ok"`
	Error    string      `json:"error,omitempty"`
	Steps    []StepTrace `json:"steps"`
}

// DryRunner runs journeys once each.
type DryRunner struct {
	Program *scenario.Program
	// BaseURL is the target the relative paths join.
	BaseURL string
	Env     map[string]string
	Secrets map[string]string
	// Allow vets every request URL; it returns a reason when the request
	// must not be sent.
	Allow func(*url.URL) string
	// DataDir resolves relative feeder files. With ConfineData, files must
	// stay inside DataDir (server mode).
	DataDir     string
	ConfineData bool
	Redactor    *Redactor
	// Transport overrides the HTTP transport (tests).
	Transport http.RoundTripper
	// BodyLimit bounds recorded bodies (default 2 KiB).
	BodyLimit int
	// MaxRequests bounds requests per pass (default 100).
	MaxRequests int
	// GRPCFiles are descriptors for grpc steps (from the generator's .proto
	// inputs). Steps whose method is not there load their own proto or
	// protoset files, or ask the server's reflection service.
	GRPCFiles *protoregistry.Files

	grpcOnce  sync.Once
	grpcPool  *grpcx.Pool
	grpcDescs *grpcx.Descriptors
}

const (
	maxPasses     = 4
	maxLoopRounds = 3
)

// RunJourney executes one journey once per branch alternative (up to 4).
func (d *DryRunner) RunJourney(ctx context.Context, j *scenario.CJourney) []Trace {
	passes := 1
	walkCompiled(j.Steps, func(st *scenario.CStep) {
		if st.Kind == scenario.StepBranch && len(st.Branches) > passes {
			passes = len(st.Branches)
		}
	})
	passes = min(passes, maxPasses)
	out := make([]Trace, 0, passes)
	for p := 0; p < passes; p++ {
		out = append(out, d.runPass(ctx, j, p))
	}
	return out
}

func walkCompiled(steps []*scenario.CStep, fn func(*scenario.CStep)) {
	for _, st := range steps {
		fn(st)
		for _, b := range st.Branches {
			walkCompiled(b.Steps, fn)
		}
		walkCompiled(st.Steps, fn)
	}
}

type pass struct {
	d        *DryRunner
	index    int
	client   *http.Client
	vars     *dryVars
	trace    *Trace
	requests int
}

var errStop = errors.New("journey stopped")

func (d *DryRunner) runPass(ctx context.Context, j *scenario.CJourney, index int) Trace {
	tr := Trace{Pass: index + 1}
	t := d.Transport
	if t == nil {
		h := d.Program.Scenario.Target.HTTP
		t = httpx.NewTransport(httpx.Options{HTTP2: h.HTTP2, InsecureSkipVerify: h.InsecureSkipVerify})
	}
	client := httpx.NewClient(t, httpx.Options{MaxRedirects: d.Program.Scenario.Target.HTTP.MaxRedirects})
	defer client.CloseIdleConnections()
	p := &pass{d: d, index: index, client: client, trace: &tr}
	p.vars = &dryVars{env: d.Env, secret: d.Secrets, static: d.Program.Scenario.Vars, vu: 1, iter: int64(index + 1)}
	p.vars.reset()
	if err := p.loadData(); err != nil {
		tr.Error = err.Error()
		return tr
	}
	err := p.steps(ctx, j.Steps)
	tr.OK = err == nil
	if err != nil && !errors.Is(err, errStop) {
		tr.Error = err.Error()
	}
	if err != nil && tr.Error == "" && len(tr.Steps) > 0 {
		tr.Error = tr.Steps[len(tr.Steps)-1].Error
	}
	return tr
}

func (p *pass) steps(ctx context.Context, steps []*scenario.CStep) error {
	for _, st := range steps {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := p.step(ctx, st); err != nil {
			return err
		}
	}
	return nil
}

func (p *pass) note(st *scenario.CStep, ok bool, msg string) {
	p.trace.Steps = append(p.trace.Steps, StepTrace{Step: st.Name, OK: ok, Note: msg})
}

func (p *pass) step(ctx context.Context, st *scenario.CStep) error {
	if st.If != nil {
		ok, err := st.If.EvalBool(p.vars)
		if err != nil {
			p.trace.Steps = append(p.trace.Steps, StepTrace{Step: st.Name, Error: "if: " + err.Error()})
			return errStop
		}
		if !ok {
			p.note(st, true, "skipped: if condition is false")
			return nil
		}
	}
	switch st.Kind {
	case scenario.StepRequest:
		return p.request(ctx, st)
	case scenario.StepGRPC:
		return p.grpcCall(ctx, st)
	case scenario.StepThink:
		// Think times are not slept in a dry run.
		return nil
	case scenario.StepBranch:
		if len(st.Branches) == 0 {
			return nil
		}
		b := st.Branches[p.index%len(st.Branches)]
		name := b.Name
		if name == "" {
			name = "alternative " + strconv.Itoa(p.index%len(st.Branches)+1)
		}
		p.trace.Branches = append(p.trace.Branches, name)
		return p.steps(ctx, b.Steps)
	case scenario.StepLoop:
		for i := 0; i < min(st.Loop.Count, maxLoopRounds); i++ {
			if err := p.steps(ctx, st.Steps); err != nil {
				return err
			}
		}
		return nil
	case scenario.StepWhile:
		for i := 0; i < min(st.Loop.Max, maxLoopRounds); i++ {
			ok, err := st.Loop.Cond.EvalBool(p.vars)
			if err != nil {
				p.trace.Steps = append(p.trace.Steps, StepTrace{Step: st.Name, Error: "while: " + err.Error()})
				return errStop
			}
			if !ok {
				break
			}
			if err := p.steps(ctx, st.Steps); err != nil {
				return err
			}
		}
		return nil
	case scenario.StepGroup:
		return p.steps(ctx, st.Steps)
	case scenario.StepScript:
		var r script.Runner
		out, err := r.Run(ctx, st.Script, script.Input{
			Vars: p.vars.local, Env: p.vars.env, Data: p.vars.data, VU: p.vars.vu, Iteration: p.vars.iter,
		})
		if err != nil {
			p.trace.Steps = append(p.trace.Steps, StepTrace{Step: st.Name, Error: "script: " + p.d.Redactor.Text(err.Error())})
			return errStop
		}
		p.vars.local = out
		p.note(st, true, "script ran")
		return nil
	}
	if st.Req == nil {
		p.note(st, true, fmt.Sprintf("not executed: the dry run does not support %s steps yet", st.Kind))
		return nil
	}
	return p.request(ctx, st)
}

func (p *pass) request(ctx context.Context, st *scenario.CStep) error {
	d := p.d
	red := d.Redactor
	r := st.Req
	tr := StepTrace{Step: st.Name, Method: r.Method}
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

	req, body, err := p.build(ctx, r)
	if req != nil {
		tr.URL = red.URL(req.URL.String())
		tr.RequestHeaders = red.Headers(req.Header)
		tr.RequestBody = red.Body(body, d.limit())
	}
	if err != nil {
		return fail("could not build the request: " + err.Error())
	}
	if d.Allow != nil {
		if why := d.Allow(req.URL); why != "" {
			return fail("not sent: " + why)
		}
	}
	timeout := r.Timeout.D()
	if timeout == 0 {
		timeout = d.Program.Scenario.Target.Timeout.D()
	}
	if timeout == 0 || timeout > 30*time.Second {
		timeout = 30 * time.Second
	}
	rctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	res := httpx.Do(p.client, req.WithContext(rctx), int64(len(body)), true, 10<<20)
	tr.DurationMs = float64(res.End.Sub(res.Start).Microseconds()) / 1000
	if res.Err != nil {
		return fail(httpx.ClassifyError(res.Err) + ": " + red.Text(res.Err.Error()))
	}
	tr.Status = res.Status
	tr.ResponseHeaders = red.Headers(res.Header)
	return p.verify(tr, r, res)
}

// verify runs a response's extractors and checks and records the step.
func (p *pass) verify(tr StepTrace, r *scenario.CRequest, res *httpx.Result) error {
	d := p.d
	red := d.Redactor
	fail := func(msg string) error {
		tr.Error = msg
		p.trace.Steps = append(p.trace.Steps, tr)
		return errStop
	}
	// Extract first so secrets in the response are registered before the
	// body is redacted and recorded.
	extracted := map[string]any{}
	var extractFail []string
	for _, ex := range r.Extract {
		v, ok := extract(ex, res)
		if !ok {
			extractFail = append(extractFail, ex.Var+" ("+ex.Src+")")
			continue
		}
		extracted[ex.Var] = v
		if s, isStr := v.(string); isStr && (SensitiveName(ex.Var) || SensitiveName(ex.Src) || len(s) >= 24 && !strings.ContainsAny(s, " \n")) {
			red.AddSecret(s)
		}
	}
	tr.ResponseBody = red.Body(res.Body, d.limit())

	checks, failed := p.check(r.Check, res)
	tr.Checks = checks
	if failed != "" {
		return fail(failed)
	}
	if len(extractFail) > 0 {
		return fail("could not extract " + strings.Join(extractFail, ", ") + " from the response")
	}
	if len(extracted) > 0 {
		tr.Extracted = map[string]string{}
		for k, v := range extracted {
			p.vars.local[k] = v
			s := scenario.Stringify(v)
			if SensitiveName(k) {
				s = redacted
			} else {
				s = Truncate(red.Text(s), 200)
			}
			tr.Extracted[k] = s
		}
	}
	tr.OK = true
	p.trace.Steps = append(p.trace.Steps, tr)
	return nil
}

func (d *DryRunner) limit() int {
	if d.BodyLimit <= 0 {
		return 2048
	}
	return d.BodyLimit
}

// build renders a request like the engine does.
func (p *pass) build(ctx context.Context, r *scenario.CRequest) (*http.Request, []byte, error) {
	v := p.vars
	raw, err := r.URL.Render(v)
	if err != nil {
		return nil, nil, err
	}
	if !strings.HasPrefix(raw, "http://") && !strings.HasPrefix(raw, "https://") {
		raw = strings.TrimRight(p.d.BaseURL, "/") + "/" + strings.TrimLeft(raw, "/")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return nil, nil, fmt.Errorf("invalid URL %q: %w", raw, err)
	}
	if len(r.Query) > 0 {
		q := u.Query()
		for _, kv := range r.Query {
			s, err := kv.Value.Render(v)
			if err != nil {
				return nil, nil, err
			}
			q.Add(kv.Name, s)
		}
		u.RawQuery = q.Encode()
	}
	var body []byte
	contentType := ""
	switch {
	case r.JSON != nil:
		val, err := r.JSON.Value(v)
		if err != nil {
			return nil, nil, err
		}
		if body, err = json.Marshal(val); err != nil {
			return nil, nil, err
		}
		contentType = "application/json"
	case len(r.Form) > 0:
		f := url.Values{}
		for _, kv := range r.Form {
			s, err := kv.Value.Render(v)
			if err != nil {
				return nil, nil, err
			}
			f.Add(kv.Name, s)
		}
		body, contentType = []byte(f.Encode()), "application/x-www-form-urlencoded"
	case r.Body != nil:
		s, err := r.Body.Render(v)
		if err != nil {
			return nil, nil, err
		}
		body = []byte(s)
	}
	req, err := http.NewRequestWithContext(ctx, r.Method, u.String(), bytes.NewReader(body))
	if err != nil {
		return nil, nil, err
	}
	req.Header.Set("User-Agent", "stampede-dry-run")
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	for _, kvs := range [][]scenario.KV{p.d.Program.Headers, r.Headers} {
		for _, kv := range kvs {
			s, err := kv.Value.Render(v)
			if err != nil {
				return req, body, err
			}
			req.Header.Set(kv.Name, s)
		}
	}
	return req, body, nil
}

// check evaluates a response like the engine does, recording each result.
// It returns the failure message, or "" when every check passed.
func (p *pass) check(c *scenario.CCheck, res *httpx.Result) ([]CheckResult, string) {
	var out []CheckResult
	failed := ""
	add := func(name string, ok bool, detail string) {
		out = append(out, CheckResult{Name: name, OK: ok, Detail: detail})
		if !ok && failed == "" {
			failed = "check " + name + " failed: " + detail
		}
	}
	if c == nil || c.Status.Empty() {
		ok := res.Status < 400
		add("status", ok, fmt.Sprintf("got %d, expected a status below 400 (no check.status given)", res.Status))
		if ok {
			out[len(out)-1].Detail = fmt.Sprintf("got %d", res.Status)
		}
	} else {
		ok := c.Status.Match(res.Status)
		add("status", ok, fmt.Sprintf("got %d, expected %s", res.Status, c.Status.String()))
	}
	if c == nil {
		return out, failed
	}
	if c.MaxLatency > 0 {
		took := res.End.Sub(res.Start)
		add("maxLatency", took <= c.MaxLatency.D(), fmt.Sprintf("took %s, limit %s", took.Round(time.Millisecond), c.MaxLatency.D()))
	}
	if c.BodyContains != nil {
		want, err := c.BodyContains.Render(p.vars)
		ok := err == nil && bytes.Contains(res.Body, []byte(want))
		add("bodyContains", ok, fmt.Sprintf("looked for %q", p.d.Redactor.Text(want)))
	}
	for _, jc := range c.JSON {
		got := gjson.GetBytes(res.Body, jc.GJSON)
		switch {
		case !got.Exists():
			add("json "+jc.Path, false, "path not found in the response")
		case jc.Exists:
			add("json "+jc.Path, true, "exists")
		default:
			ok := jsonEqual(got.Value(), jc.Expected)
			add("json "+jc.Path, ok, fmt.Sprintf("got %s, expected %v", Truncate(p.d.Redactor.Text(got.Raw), 120), jc.Expected))
		}
	}
	if c.Schema != nil {
		problem := c.SchemaProblem(res.Body)
		detail := "the body matches the schema"
		if problem != "" {
			detail = p.d.Redactor.Text(problem)
		}
		add("schema", problem == "", detail)
	}
	if c.Expr != nil {
		ok, err := c.Expr.EvalBool(&dryResp{parent: p.vars, res: res})
		detail := c.Expr.String()
		if err != nil {
			detail += ": " + err.Error()
		}
		add("expr", err == nil && ok, detail)
	}
	return out, failed
}

func jsonEqual(got, want any) bool {
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

func extract(ex scenario.Extractor, res *httpx.Result) (any, bool) {
	switch ex.Kind {
	case scenario.ExtractJSON:
		g := gjson.GetBytes(res.Body, ex.GJSON)
		if !g.Exists() {
			return nil, false
		}
		switch g.Type {
		case gjson.Number:
			if f := g.Float(); f == float64(int64(f)) && !strings.ContainsAny(g.Raw, ".eE") {
				return g.Int(), true
			}
			return g.Float(), true
		case gjson.String:
			return g.Str, true
		case gjson.True:
			return true, true
		case gjson.False:
			return false, true
		case gjson.Null:
			return nil, true
		}
		return g.Value(), true
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

type cssMatcher struct{ cascadia.Sel }

func (m cssMatcher) MatchAll(n *html.Node) []*html.Node  { return cascadia.QueryAll(n, m.Sel) }
func (m cssMatcher) Filter(ns []*html.Node) []*html.Node { return cascadia.Filter(ns, m.Sel) }

// Test data: each pass takes the row at its index from every feeder the
// scenario declares.

func (p *pass) loadData() error {
	for name, f := range p.d.Program.Scenario.Data {
		rows, err := p.d.rows(f)
		if err != nil {
			return fmt.Errorf("data.%s: %w", name, err)
		}
		if len(rows) == 0 {
			return fmt.Errorf("data.%s: no rows", name)
		}
		p.vars.data[name] = rows[p.index%len(rows)]
	}
	return nil
}

func (d *DryRunner) rows(f scenario.Feeder) ([]any, error) {
	switch {
	case len(f.Generate) > 0:
		g, err := feed.NewGenerator(f.Generate, 0, 1)
		if err != nil {
			return nil, err
		}
		out := make([]any, maxPasses)
		for i := range out {
			out[i] = g.Row()
		}
		return out, nil
	case f.SQL != nil:
		dsn := feed.Expand(f.SQL.DSN, d.Env, d.Secrets)
		host, err := feed.SQLHost(f.SQL.Driver, dsn)
		if err != nil {
			return nil, err
		}
		if d.Allow != nil {
			if why := d.Allow(&url.URL{Scheme: f.SQL.Driver, Host: host}); why != "" {
				return nil, fmt.Errorf("database host %s: %s", host, why)
			}
		}
		return feed.SQLRows(context.Background(), f.SQL.Driver, dsn, f.SQL.Query, maxPasses)
	case len(f.List) > 0:
		return f.List, nil
	case len(f.Range) == 2:
		out := []any{}
		for v := f.Range[0]; v <= f.Range[1] && len(out) < maxPasses; v++ {
			out = append(out, v)
		}
		return out, nil
	case f.CSV != "" || f.JSON != "":
		path := f.CSV
		if path == "" {
			path = f.JSON
		}
		full, err := d.dataPath(path)
		if err != nil {
			return nil, err
		}
		if f.CSV != "" {
			return readCSVRows(full)
		}
		b, err := os.ReadFile(full)
		if err != nil {
			return nil, err
		}
		var rows []any
		if err := json.Unmarshal(b, &rows); err != nil {
			return nil, fmt.Errorf("JSON feeder must be an array: %w", err)
		}
		return rows, nil
	}
	return nil, errors.New("no source")
}

func (d *DryRunner) dataPath(p string) (string, error) {
	if d.ConfineData {
		if d.DataDir == "" {
			return "", errors.New("file feeders need the server to be started with --data-dir; use list or range feeders")
		}
		full := filepath.Join(d.DataDir, filepath.Clean("/"+p))
		rel, err := filepath.Rel(d.DataDir, full)
		if err != nil || strings.HasPrefix(rel, "..") {
			return "", fmt.Errorf("%q is outside the data directory", p)
		}
		return full, nil
	}
	if filepath.IsAbs(p) || d.DataDir == "" {
		return p, nil
	}
	return filepath.Join(d.DataDir, p), nil
}

func readCSVRows(path string) ([]any, error) {
	fh, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer fh.Close()
	r := csv.NewReader(fh)
	header, err := r.Read()
	if err != nil {
		return nil, fmt.Errorf("read CSV header: %w", err)
	}
	var rows []any
	for len(rows) < maxPasses {
		rec, err := r.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		row := make(map[string]any, len(header))
		for i, h := range header {
			if i < len(rec) {
				row[h] = rec[i]
			}
		}
		rows = append(rows, row)
	}
	return rows, nil
}

// dryVars is the expression activation for the dry-run user.
type dryVars struct {
	env    map[string]string
	secret map[string]string
	static map[string]any
	data   map[string]any
	local  map[string]any
	vu     int64
	iter   int64
}

func (a *dryVars) reset() {
	if a.env == nil {
		a.env = map[string]string{}
	}
	if a.secret == nil {
		a.secret = map[string]string{}
	}
	a.local = make(map[string]any, len(a.static)+4)
	for k, v := range a.static {
		a.local[k] = v
	}
	a.data = map[string]any{}
}

func (a *dryVars) ResolveName(name string) (any, bool) {
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

func (a *dryVars) Parent() interpreter.Activation { return nil }

type dryResp struct {
	parent *dryVars
	res    *httpx.Result
	json   any
	parsed bool
}

func (r *dryResp) ResolveName(name string) (any, bool) {
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

func (r *dryResp) Parent() interpreter.Activation { return nil }
