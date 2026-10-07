package scenario

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"

	"cel.dev/cel-go/interpreter"
	"github.com/andybalholm/cascadia"
	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/Ivan825/Stampede/internal/script"
	"go.yaml.in/yaml/v3"
	"golang.org/x/text/language"
	"golang.org/x/text/message"
)

// Program is a scenario compiled for execution: every template and
// expression is parsed once, ahead of the run.
type Program struct {
	Scenario   *Scenario
	BaseURL    *Template
	Headers    []KV
	Journeys   []*CJourney
	Thresholds []Threshold
	// Steps indexes every request step by ID for metric reporting.
	Steps []*CStep
	// TotalWeight is the sum of journey weights.
	TotalWeight int
}

// CJourney is a compiled journey.
type CJourney struct {
	Index  int
	Name   string
	Weight int
	Steps  []*CStep
}

// KV is a templated name/value pair.
type KV struct {
	Name  string
	Value *Template
}

// CStep is a compiled step.
type CStep struct {
	ID      int
	Kind    StepKind
	Name    string
	Journey string
	If      *Expr

	Req      *CRequest
	Think    *ThinkTime
	Branches []CBranch
	// BranchTotal is the sum of branch weights.
	BranchTotal int
	Loop        *CLoop
	Group       string
	Steps       []*CStep

	// GraphQL and SSE are set for their step kinds, whose HTTP parts are
	// in Req.
	GraphQL *CGraphQL
	SSE     *CSSE
	// WS is set for ws steps: Req holds the handshake and Steps run on
	// the connection.
	WS *CWS
	// Browser is set for browser steps, whose Steps run in the page;
	// Action for the actions inside them.
	Browser *CBrowser
	Action  *CAction
	Send    *CSend
	Expect  *CExpect
	// GRPC is set for grpc steps; Req holds their check (without status),
	// extractors and timeout.
	GRPC *CGRPC
	// Script is set for script steps.
	Script *script.Program
	// Plugin is set for plugin steps; Req holds their check, extractors
	// and timeout.
	Plugin *CPlugin
}

// CPlugin is a compiled plugin step.
type CPlugin struct {
	// Plugin and Step split "mqtt.publish".
	Plugin, Step string
	// With renders the step's config.
	With *JSONTemplate
	// Config is the config as written, for checking it against the
	// plugin's schema; Templated lists the JSON pointers of its strings
	// that contain ${} expressions, whose type is only known at run time.
	Config    map[string]any
	Templated []string
}

// PluginNames returns the plugins a program uses, sorted.
func (p *Program) PluginNames() []string {
	var out []string
	for _, st := range p.Steps {
		if st.Plugin != nil && !slices.Contains(out, st.Plugin.Plugin) {
			out = append(out, st.Plugin.Plugin)
		}
	}
	sort.Strings(out)
	return out
}

// CGRPC is a compiled gRPC call.
type CGRPC struct {
	// Service is the service's full name and Method the method's name.
	Service string
	Method  string
	// Target is nil when calls go to target.baseURL's host.
	Target      *Template
	Message     *JSONTemplate
	Metadata    []KV
	Protoset    string
	Proto       []string
	ImportPaths []string
	// Codes are the accepted status code names (canonical, such as
	// NOT_FOUND); CheckedStatus is set when the scenario listed them.
	Codes         []string
	CheckedStatus bool
}

// CWS is a compiled WebSocket block.
type CWS struct {
	Subprotocols []string
}

// CBrowser is a compiled browser block: the page is opened at URL and the
// block's Steps (actions) run in it.
type CBrowser struct {
	URL      *Template
	Timeout  Duration
	Viewport Viewport
}

// CAction is a compiled browser action.
type CAction struct {
	Target  *Template
	Pairs   []CSelectorValue
	Timeout Duration
}

// CSelectorValue is a selector and its value, both templates.
type CSelectorValue struct {
	Selector, Value *Template
}

// CSend is a compiled WebSocket message: exactly one of Text and JSON.
type CSend struct {
	Text *Template
	JSON *JSONTemplate
}

// CExpect is a compiled wait for a WebSocket message.
type CExpect struct {
	Match   *regexp.Regexp
	JSON    []JSONCheck
	Timeout Duration
	Extract []Extractor
}

// CSSE says when a compiled server-sent events step stops reading.
type CSSE struct {
	Events   int
	Match    *regexp.Regexp
	Duration Duration
}

// CGraphQL is a compiled GraphQL operation.
type CGraphQL struct {
	// Query is nil when only a persisted query hash is sent.
	Query         *Template
	Variables     *JSONTemplate
	OperationName string
	Persisted     bool
	// Hash is the persisted query's SHA-256 in hex when it is known at
	// compile time; empty means hash the rendered query.
	Hash        string
	AllowErrors bool
}

// CBranch is one weighted alternative.
type CBranch struct {
	Weight int
	Name   string
	Steps  []*CStep
}

// CLoop holds loop parameters; the body is CStep.Steps.
type CLoop struct {
	Count int
	Cond  *Expr
	Max   int
}

// CRequest is a compiled HTTP request.
type CRequest struct {
	Method  string
	URL     *Template
	Headers []KV
	Query   []KV
	JSON    *JSONTemplate
	Body    *Template
	Form    []KV
	Check   *CCheck
	Extract []Extractor
	Timeout Duration
}

// CCheck is a compiled response check.
type CCheck struct {
	Status       StatusMatcher
	BodyContains *Template
	JSON         []JSONCheck
	MaxLatency   Duration
	Expr         *Expr
	// Schema validates the body; see SchemaProblem.
	Schema *jsonschema.Schema
	// NeedsJSON is set when the check reads the parsed body.
	NeedsJSON bool
}

// SchemaProblem validates body against the check's schema and returns ""
// when it passes, or the first problem.
func (c *CCheck) SchemaProblem(body []byte) string {
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(body))
	if err != nil {
		return "the body is not JSON"
	}
	if err := c.Schema.Validate(doc); err != nil {
		var ve *jsonschema.ValidationError
		if errors.As(err, &ve) {
			if leaf := firstLeaf(ve); leaf != nil {
				loc := "/" + strings.Join(leaf.InstanceLocation, "/")
				return loc + ": " + leaf.ErrorKind.LocalizedString(schemaPrinter)
			}
		}
		return err.Error()
	}
	return ""
}

var schemaPrinter = message.NewPrinter(language.English)

// firstLeaf returns the deepest first cause, the most specific problem.
func firstLeaf(ve *jsonschema.ValidationError) *jsonschema.ValidationError {
	for len(ve.Causes) > 0 {
		ve = ve.Causes[0]
	}
	return ve
}

// JSONCheck asserts a value at a path.
type JSONCheck struct {
	Path     string // original JSONPath
	GJSON    string
	Exists   bool
	Expected any
}

// Extractor kinds.
const (
	ExtractJSON   = "json"
	ExtractHeader = "header"
	ExtractCookie = "cookie"
	ExtractRegex  = "regex"
	ExtractCSS    = "css"
	ExtractStatus = "status"
	ExtractBody   = "body"
)

// Extractor pulls a value out of a response into a variable.
type Extractor struct {
	Var  string
	Kind string
	Src  string
	// GJSON path for json extractors.
	GJSON string
	Regex *regexp.Regexp
	CSS   cascadia.Sel
	// Attr is the attribute read by CSS extractors (text when empty).
	Attr string
}

// JSONTemplate is a JSON value whose strings may contain templates.
type JSONTemplate struct {
	tmpl *Template
	obj  []jsonField
	arr  []*JSONTemplate
	lit  any
	kind byte // 't' template, 'o' object, 'a' array, 'l' literal
}

type jsonField struct {
	key string
	val *JSONTemplate
}

var identRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// Compile validates the scenario's dynamic parts and returns an
// executable program. It checks that every variable is defined before it
// is used.
//
// A replay scenario's recording is loaded first if it is not yet, so call
// Compile after ResolvePaths.
func Compile(s *Scenario) (*Program, error) {
	if err := s.LoadReplay(); err != nil {
		return nil, err
	}
	return compile(s)
}

func compile(s *Scenario) (*Program, error) {
	c := &compiler{prog: &Program{Scenario: s}}
	return c.compile()
}

type compiler struct {
	prog *Program
	errs []string
	// static are the variables known before any step runs (vars).
	static []string
	// inWS is set while compiling the steps of a ws block.
	inWS bool
	// inBrowser is set while compiling the steps of a browser block.
	inBrowser bool
}

func (c *compiler) errf(path, format string, args ...any) {
	c.errs = append(c.errs, path+": "+fmt.Sprintf(format, args...))
}

func (c *compiler) compile() (*Program, error) {
	s := c.prog.Scenario
	initial := make([]string, 0, len(s.Vars))
	for k := range s.Vars {
		if !identRe.MatchString(k) || IsReserved(k) {
			c.errf("vars."+k, "variable names must be identifiers and not one of %s", strings.Join(append(builtinRoots, responseRoots...), ", "))
			continue
		}
		initial = append(initial, k)
	}
	scope, err := NewScope(initial...)
	if err != nil {
		return nil, err
	}
	c.static = initial

	c.prog.BaseURL = c.template(scope, "target.baseURL", s.Target.BaseURL)
	c.prog.Headers = c.kvs(scope, "target.headers", s.Target.Headers)

	for i, j := range s.Journeys {
		cj := &CJourney{Index: i, Name: j.Name, Weight: j.Weight}
		cj.Steps, _ = c.steps(fmt.Sprintf("journeys[%s]", j.Name), j.Name, j.Steps, initial)
		c.prog.Journeys = append(c.prog.Journeys, cj)
		c.prog.TotalWeight += j.Weight
	}

	for i, src := range s.Targets {
		t, err := ParseThreshold(src)
		if err != nil {
			c.errf(fmt.Sprintf("targets[%d]", i), "%v", err)
			continue
		}
		if !c.knownScope(t.Scope) {
			c.errf(fmt.Sprintf("targets[%d]", i), "target %q refers to unknown journey or step %q", src, t.Scope)
			continue
		}
		c.prog.Thresholds = append(c.prog.Thresholds, t)
	}
	for _, j := range s.Journeys {
		c.prog.Thresholds = append(c.prog.Thresholds, JourneyThresholds(j)...)
	}

	if len(c.errs) > 0 {
		return nil, &ValidationError{Problems: c.errs}
	}
	return c.prog, nil
}

func (c *compiler) knownScope(scope string) bool {
	if scope == ScopeAll {
		return true
	}
	jn, step, hasStep := strings.Cut(scope, "/")
	for _, j := range c.prog.Journeys {
		if j.Name != jn {
			continue
		}
		if !hasStep {
			return true
		}
		for _, st := range c.prog.Steps {
			if st.Journey == jn && st.Name == step {
				return true
			}
		}
	}
	return false
}

// steps compiles a step list. vars are the names defined on entry; the
// returned slice is the names defined on exit.
func (c *compiler) steps(path, journey string, in []Step, vars []string) ([]*CStep, []string) {
	vars = append([]string(nil), vars...)
	out := make([]*CStep, 0, len(in))
	for i, st := range in {
		p := fmt.Sprintf("%s.steps[%d]", path, i)
		cs, after := c.step(p, journey, st, vars)
		vars = after
		if cs != nil {
			out = append(out, cs)
		}
	}
	return out, vars
}

func (c *compiler) step(path, journey string, st Step, vars []string) (*CStep, []string) {
	scope, err := NewScope(vars...)
	if err != nil {
		c.errf(path, "%v", err)
		return nil, vars
	}
	cs := &CStep{Kind: st.Kind, Name: st.Name, Journey: journey}
	if st.If != "" {
		cs.If = c.condition(scope, path+".if", st.If)
	}

	switch st.Kind {
	case StepRequest:
		r := st.Request
		if cs.Name == "" {
			cs.Name = r.Method + " " + r.URL
		}
		c.noAllowErrors(path, r.Check)
		cs.Req, vars = c.request(path, strings.ToLower(r.Method), scope, r, vars)
		c.record(cs)
	case StepGraphQL:
		vars = c.graphql(path, scope, cs, st.GraphQL, vars)
		c.record(cs)
	case StepSSE:
		vars = c.sse(path, scope, cs, st.SSE, vars)
		c.record(cs)
	case StepWS:
		w := st.WS
		if c.inWS {
			c.errf(path, "ws blocks cannot be nested; close the first connection before opening another")
			break
		}
		if cs.Name == "" {
			cs.Name = "WS " + w.URL
		}
		cs.Req, _ = c.request(path, "ws", scope, &Request{Method: "GET", URL: w.URL, Headers: w.Headers, Timeout: w.Timeout}, vars)
		cs.WS = &CWS{Subprotocols: w.Subprotocols}
		c.record(cs)
		if len(w.Steps) == 0 {
			c.errf(path, "ws needs steps to run on the connection")
		}
		c.inWS = true
		cs.Steps, vars = c.steps(path, journey, w.Steps, vars)
		c.inWS = false
	case StepBrowser:
		b := st.Browser
		if c.inBrowser || c.inWS {
			c.errf(path, "a browser block cannot be nested in a browser or ws block")
			break
		}
		if cs.Name == "" {
			cs.Name = "browser " + b.URL
		}
		cb := &CBrowser{URL: c.template(scope, path+".browser", b.URL), Timeout: b.Timeout, Viewport: Viewport{Width: 1280, Height: 800}}
		if v := b.Viewport; v != nil {
			if v.Width < 200 || v.Height < 200 || v.Width > 8000 || v.Height > 8000 {
				c.errf(path+".viewport", "width and height must be between 200 and 8000")
			}
			cb.Viewport = *v
		}
		cs.Browser = cb
		c.record(cs)
		if len(b.Steps) == 0 {
			c.errf(path, "browser needs steps to run in the page")
		}
		c.inBrowser = true
		cs.Steps, vars = c.steps(path, journey, b.Steps, vars)
		c.inBrowser = false
	case StepGoto, StepClick, StepFill, StepPress, StepWaitFor, StepAssert:
		a := st.Action
		if !c.inBrowser {
			c.errf(path, "%s only works inside a browser block", st.Kind)
			break
		}
		ca := &CAction{Timeout: a.Timeout}
		if a.Target != "" {
			ca.Target = c.template(scope, fmt.Sprintf("%s.%s", path, st.Kind), a.Target)
		}
		for i, p := range a.Pairs {
			pp := fmt.Sprintf("%s.%s[%d]", path, st.Kind, i)
			ca.Pairs = append(ca.Pairs, CSelectorValue{Selector: c.template(scope, pp, p.Selector), Value: c.template(scope, pp, p.Value)})
		}
		if cs.Name == "" {
			switch {
			case a.Target != "":
				cs.Name = string(st.Kind) + " " + a.Target
			default:
				var sels []string
				for _, p := range a.Pairs {
					sels = append(sels, p.Selector)
				}
				cs.Name = string(st.Kind) + " " + strings.Join(sels, ", ")
			}
		}
		cs.Action = ca
		c.record(cs)
	case StepSend:
		if !c.inWS {
			c.errf(path, "send only works inside a ws block")
		}
		if cs.Name == "" {
			cs.Name = "send"
		}
		cs.Send = &CSend{}
		switch {
		case st.Send.JSON != nil:
			cs.Send.JSON = c.jsonTemplate(scope, path+".send", st.Send.JSON)
		case st.Send.Text == "":
			c.errf(path, "send needs a message")
		default:
			cs.Send.Text = c.template(scope, path+".send", st.Send.Text)
		}
		c.record(cs)
	case StepGRPC:
		vars = c.grpc(path, scope, cs, st.GRPC, vars)
		c.record(cs)
	case StepPlugin:
		vars = c.plugin(path, scope, cs, st.Plugin, vars)
		c.record(cs)
	case StepExpect:
		if !c.inWS {
			c.errf(path, "expect only works inside a ws block")
		}
		cs.Expect, vars = c.expect(path, st.Expect, vars)
		if cs.Name == "" {
			cs.Name = "expect"
			if st.Expect.Match != "" {
				cs.Name += " " + st.Expect.Match
			}
		}
		c.record(cs)
	case StepThink:
		cs.Think = st.Think
	case StepBranch:
		if len(st.Branch) == 0 {
			c.errf(path, "branch needs at least one alternative")
		}
		var union []string
		for i, b := range st.Branch {
			if b.Weight <= 0 {
				c.errf(fmt.Sprintf("%s.branch[%d]", path, i), "weight must be positive")
			}
			steps, after := c.steps(fmt.Sprintf("%s.branch[%d]", path, i), journey, b.Steps, vars)
			cs.Branches = append(cs.Branches, CBranch{Weight: b.Weight, Name: b.Name, Steps: steps})
			cs.BranchTotal += b.Weight
			union = append(union, after...)
		}
		vars = dedupe(append(vars, union...))
	case StepLoop, StepWhile:
		cs.Loop = &CLoop{Count: st.Loop.Count, Max: st.Loop.Max}
		if st.Kind == StepWhile {
			cs.Loop.Cond = c.condition(scope, path+".while", st.Loop.Cond)
		}
		if len(st.Loop.Steps) == 0 {
			c.errf(path, "loop needs steps")
		}
		cs.Steps, vars = c.steps(path, journey, st.Loop.Steps, vars)
	case StepGroup:
		cs.Group = st.Group.Name
		if cs.Name == "" {
			cs.Name = st.Group.Name
		}
		cs.Steps, vars = c.steps(path, journey, st.Group.Steps, vars)
	case StepScript:
		p, err := script.Compile(path, st.Script)
		if err != nil {
			c.errf(path, "%v", err)
			break
		}
		cs.Script = p
		if cs.Name == "" {
			cs.Name = "script"
		}
		for _, name := range st.Sets {
			if !identRe.MatchString(name) || IsReserved(name) {
				c.errf(path+".sets", "%q is not a variable name (identifiers only, not one of %s)", name, strings.Join(append(builtinRoots, responseRoots...), ", "))
				continue
			}
			vars = append(vars, name)
		}
	}
	return cs, vars
}

// record gives a step that talks to the target an ID for metrics.
func (c *compiler) record(cs *CStep) {
	cs.ID = len(c.prog.Steps)
	c.prog.Steps = append(c.prog.Steps, cs)
}

func (c *compiler) noAllowErrors(path string, ch *Check) {
	if ch != nil && ch.AllowErrors {
		c.errf(path+".check.allowErrors", "only applies to graphql steps")
	}
}

var sha256Re = regexp.MustCompile(`^[0-9a-f]{64}$`)

func (c *compiler) graphql(path string, scope *Scope, cs *CStep, g *GraphQL, vars []string) []string {
	if cs.Name == "" {
		cs.Name = "graphql " + g.URL
		if g.OperationName != "" {
			cs.Name = "graphql " + g.OperationName
		}
	}
	cs.Req, vars = c.request(path, "graphql", scope, &g.Request, vars)
	cg := &CGraphQL{OperationName: g.OperationName, Persisted: g.Persisted != nil}
	if g.Check != nil {
		cg.AllowErrors = g.Check.AllowErrors
	}
	if g.Persisted != nil && g.Persisted.SHA256 != "" {
		h := strings.ToLower(g.Persisted.SHA256)
		if !sha256Re.MatchString(h) {
			c.errf(path+".persisted.sha256", "must be 64 hex digits")
		}
		cg.Hash = h
	}
	switch {
	case strings.TrimSpace(g.Query) != "":
		cg.Query = c.template(scope, path+".query", g.Query)
		if cg.Persisted && cg.Hash == "" && cg.Query != nil && cg.Query.IsLiteral() {
			sum := sha256.Sum256([]byte(g.Query))
			cg.Hash = hex.EncodeToString(sum[:])
		}
	case cg.Hash == "":
		c.errf(path, "graphql needs a query (or persisted: {sha256: ...} to send only a known hash)")
	}
	if g.Variables != nil {
		if _, ok := g.Variables.(map[string]any); !ok {
			c.errf(path+".variables", "must be a mapping of variable names to values")
		} else {
			cg.Variables = c.jsonTemplate(scope, path+".variables", g.Variables)
		}
	}
	cs.GraphQL = cg
	return vars
}

func (c *compiler) sse(path string, scope *Scope, cs *CStep, e *SSE, vars []string) []string {
	if cs.Name == "" {
		cs.Name = "SSE " + e.URL
	}
	c.noAllowErrors(path, e.Check)
	cs.Req, vars = c.request(path, "sse", scope, &e.Request, vars)
	ce := &CSSE{Events: e.Until.Events, Duration: e.Until.Duration}
	if e.Until.Events < 0 {
		c.errf(path+".until.events", "must be positive")
	}
	if e.Until.Duration < 0 {
		c.errf(path+".until.duration", "must be positive")
	}
	if e.Until.Match != "" {
		re, err := regexp.Compile(e.Until.Match)
		if err != nil {
			c.errf(path+".until.match", "invalid regex: %v", err)
		}
		ce.Match = re
	}
	cs.SSE = ce
	return vars
}

var (
	grpcMethodRe = regexp.MustCompile(`^/?([A-Za-z_][A-Za-z0-9_]*(?:\.[A-Za-z_][A-Za-z0-9_]*)*)/([A-Za-z_][A-Za-z0-9_]*)$`)
	grpcCodes    = []string{
		"OK", "CANCELLED", "UNKNOWN", "INVALID_ARGUMENT", "DEADLINE_EXCEEDED", "NOT_FOUND",
		"ALREADY_EXISTS", "PERMISSION_DENIED", "RESOURCE_EXHAUSTED", "FAILED_PRECONDITION",
		"ABORTED", "OUT_OF_RANGE", "UNIMPLEMENTED", "INTERNAL", "UNAVAILABLE", "DATA_LOSS",
		"UNAUTHENTICATED",
	}
)

// GRPCCodeName returns the canonical name of a gRPC status code given in
// any case, with or without underscores (NOT_FOUND, NotFound, not_found).
func GRPCCodeName(s string) (string, bool) {
	key := strings.ReplaceAll(strings.ToUpper(strings.TrimSpace(s)), "_", "")
	if key == "CANCELED" {
		key = "CANCELLED"
	}
	for _, c := range grpcCodes {
		if strings.ReplaceAll(c, "_", "") == key {
			return c, true
		}
	}
	return "", false
}

func (c *compiler) grpc(path string, scope *Scope, cs *CStep, g *GRPC, vars []string) []string {
	if cs.Name == "" {
		cs.Name = "gRPC " + strings.TrimPrefix(g.Method, "/")
	}
	cg := &CGRPC{Protoset: g.Protoset, Proto: g.Proto, ImportPaths: g.ImportPaths, Codes: []string{"OK"}}
	if m := grpcMethodRe.FindStringSubmatch(g.Method); m != nil {
		cg.Service, cg.Method = m[1], m[2]
	} else {
		c.errf(path, "grpc method %q must look like package.Service/Method", g.Method)
	}
	if g.Protoset != "" && len(g.Proto) > 0 {
		c.errf(path, "set protoset or proto, not both")
	}
	if len(g.ImportPaths) > 0 && len(g.Proto) == 0 {
		c.errf(path+".importPaths", "only applies with proto")
	}
	if g.Target != "" {
		// The target is rendered once per run, before any step has run.
		static, _ := NewScope(c.static...)
		cg.Target = c.template(static, path+".target", g.Target)
		if cg.Target != nil && cg.Target.IsLiteral() {
			if u, err := url.Parse(g.Target); err != nil || (u.Scheme != "grpc" && u.Scheme != "grpcs") || u.Host == "" {
				c.errf(path+".target", "must look like grpc://host:port or grpcs://host:port")
			}
		}
	}
	if g.Message != nil {
		if _, ok := g.Message.(map[string]any); !ok {
			c.errf(path+".message", "must be a mapping (the request message as JSON)")
		} else {
			cg.Message = c.jsonTemplate(scope, path+".message", g.Message)
		}
	}
	cg.Metadata = c.kvs(scope, path+".metadata", g.Metadata)

	cs.Req = &CRequest{Timeout: g.Timeout}
	if ch := g.Check; ch != nil {
		if len(ch.Status) > 0 {
			cg.Codes, cg.CheckedStatus = nil, true
			for _, s := range ch.Status {
				name, ok := GRPCCodeName(s)
				if !ok {
					c.errf(path+".check.status", "unknown gRPC status %q (use names such as OK, NOT_FOUND, UNAVAILABLE)", s)
					continue
				}
				cg.Codes = append(cg.Codes, name)
			}
		}
		cs.Req.Check = c.check(path+".check", &Check{JSON: ch.JSON, MaxLatency: ch.MaxLatency, Expr: ch.Expr}, vars)
	}
	cs.Req.Extract, vars = c.extractors(path, g.Extract, vars, ExtractJSON, ExtractRegex, ExtractBody, ExtractHeader)
	cs.GRPC = cg
	return vars
}

var (
	pluginUseRe = regexp.MustCompile(`^([a-z][a-z0-9-]{0,62})\.([A-Za-z][A-Za-z0-9_-]{0,62})$`)
)

func (c *compiler) plugin(path string, scope *Scope, cs *CStep, p *PluginStep, vars []string) []string {
	if cs.Name == "" {
		cs.Name = p.Use
	}
	cp := &CPlugin{Config: p.With}
	if m := pluginUseRe.FindStringSubmatch(p.Use); m != nil {
		cp.Plugin, cp.Step = m[1], m[2]
	} else {
		c.errf(path, "plugin %q must look like <plugin>.<step>, such as mqtt.publish", p.Use)
	}
	if cp.Config == nil {
		cp.Config = map[string]any{}
	}
	cp.With = c.jsonTemplate(scope, path+".with", cp.Config)
	cp.Templated = templatedPointers("", cp.Config, nil)
	cs.Req = &CRequest{Timeout: p.Timeout}
	if ch := p.Check; ch != nil {
		if !ch.Status.Empty() {
			c.errf(path+".check.status", "does not apply to plugin steps; check the returned values with json or expr")
		}
		c.noAllowErrors(path, ch)
		cs.Req.Check = c.check(path+".check", ch, vars)
	}
	cs.Req.Extract, vars = c.extractors(path, p.Extract, vars, ExtractJSON, ExtractRegex, ExtractBody)
	cs.Plugin = cp
	return vars
}

// templatedPointers lists the JSON pointers (RFC 6901) of the strings in v
// that contain ${} expressions.
func templatedPointers(ptr string, v any, out []string) []string {
	switch x := v.(type) {
	case string:
		if strings.Contains(x, "${") {
			out = append(out, ptr)
		}
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			esc := strings.ReplaceAll(strings.ReplaceAll(k, "~", "~0"), "/", "~1")
			out = templatedPointers(ptr+"/"+esc, x[k], out)
		}
	case []any:
		for i, e := range x {
			out = templatedPointers(fmt.Sprintf("%s/%d", ptr, i), e, out)
		}
	}
	return out
}

func (c *compiler) expect(path string, e *Expect, vars []string) (*CExpect, []string) {
	ce := &CExpect{Timeout: e.Timeout}
	if e.Match != "" {
		re, err := regexp.Compile(e.Match)
		if err != nil {
			c.errf(path+".expect.match", "invalid regex: %v", err)
		}
		ce.Match = re
	}
	ce.JSON = c.jsonChecks(path+".expect.json", e.JSON)
	ce.Extract, vars = c.extractors(path, e.Extract, vars, ExtractJSON, ExtractRegex, ExtractBody)
	return ce, vars
}

// jsonChecks compiles JSONPath assertions, sorted by path.
func (c *compiler) jsonChecks(path string, m map[string]any) []JSONCheck {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var out []JSONCheck
	for _, k := range keys {
		g, err := JSONPathToGJSON(k)
		if err != nil {
			c.errf(path, "%v", err)
			continue
		}
		jc := JSONCheck{Path: k, GJSON: g, Expected: normalizeYAML(m[k])}
		if s, ok := jc.Expected.(string); ok && s == "exists" {
			jc.Exists = true
		}
		out = append(out, jc)
	}
	return out
}

// extractors compiles extraction rules and adds their variables to vars.
// allowed limits the extractor kinds when the step has no HTTP response
// (a WebSocket message has no headers, cookies or status); none means all.
func (c *compiler) extractors(path string, m map[string]string, vars []string, allowed ...string) ([]Extractor, []string) {
	names := make([]string, 0, len(m))
	for k := range m {
		names = append(names, k)
	}
	sort.Strings(names)
	var out []Extractor
	for _, name := range names {
		ex, err := ParseExtractor(name, m[name])
		if err != nil {
			c.errf(path+".extract."+name, "%v", err)
			continue
		}
		if len(allowed) > 0 && !slices.Contains(allowed, ex.Kind) {
			c.errf(path+".extract."+name, "%s extractors do not apply here; use $.path, regex: or body", ex.Kind)
			continue
		}
		out = append(out, ex)
		vars = dedupe(append(vars, name))
	}
	return out, vars
}

func (c *compiler) condition(scope *Scope, path, src string) *Expr {
	src = strings.TrimSpace(src)
	if strings.HasPrefix(src, "${") && strings.HasSuffix(src, "}") {
		src = strings.TrimSpace(src[2 : len(src)-1])
	}
	e, err := scope.CompileExpr(src)
	if err != nil {
		c.errf(path, "%v", err)
	}
	return e
}

func (c *compiler) template(scope *Scope, path, src string) *Template {
	t, err := scope.CompileTemplate(src)
	if err != nil {
		c.errf(path, "%v", explainUndeclared(err))
		return nil
	}
	return t
}

func (c *compiler) kvs(scope *Scope, path string, m map[string]string) []KV {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]KV, 0, len(m))
	for _, k := range keys {
		out = append(out, KV{Name: k, Value: c.template(scope, path+"."+k, m[k])})
	}
	return out
}

// request compiles the HTTP parts of a request-like step; kind names the
// step key in error messages.
func (c *compiler) request(path, kind string, scope *Scope, r *Request, vars []string) (*CRequest, []string) {
	cr := &CRequest{Method: r.Method, Timeout: r.Timeout}
	if strings.TrimSpace(r.URL) == "" {
		c.errf(path, "%s needs a path or URL", kind)
	}
	cr.URL = c.template(scope, path+".url", r.URL)
	cr.Headers = c.kvs(scope, path+".headers", r.Headers)
	cr.Query = c.kvs(scope, path+".query", r.Query)
	cr.Form = c.kvs(scope, path+".form", r.Form)
	if r.JSON != nil {
		cr.JSON = c.jsonTemplate(scope, path+".json", r.JSON)
	}
	if r.Body != "" {
		cr.Body = c.template(scope, path+".body", r.Body)
	}
	if r.Check != nil {
		cr.Check = c.check(path+".check", r.Check, vars)
	}
	cr.Extract, vars = c.extractors(path, r.Extract, vars)
	return cr, vars
}

func (c *compiler) check(path string, ch *Check, vars []string) *CCheck {
	cc := &CCheck{Status: ch.Status, MaxLatency: ch.MaxLatency}
	scope, _ := NewScope(vars...)
	if ch.BodyContains != "" {
		cc.BodyContains = c.template(scope, path+".bodyContains", ch.BodyContains)
	}
	cc.JSON = c.jsonChecks(path+".json", ch.JSON)
	cc.NeedsJSON = len(cc.JSON) > 0
	if ch.Expr != "" {
		rs, err := NewResponseScope(vars...)
		if err != nil {
			c.errf(path, "%v", err)
			return cc
		}
		cc.Expr = c.condition(rs, path+".expr", ch.Expr)
		if strings.Contains(ch.Expr, "json") {
			cc.NeedsJSON = true
		}
	}
	if ch.Schema != nil {
		cc.Schema = c.responseSchema(path+".schema", ch.Schema)
		cc.NeedsJSON = true
	}
	return cc
}

// responseSchema compiles a check's JSON Schema, inline or from a file.
func (c *compiler) responseSchema(path string, v any) *jsonschema.Schema {
	var raw []byte
	loc := "stampede-check:///" + path + ".json"
	switch x := v.(type) {
	case string:
		b, err := os.ReadFile(x) //nolint:gosec // a file the scenario names; the server confines it to its data directory
		if err != nil {
			c.errf(path, "%v", err)
			return nil
		}
		if ext := strings.ToLower(filepath.Ext(x)); ext == ".yaml" || ext == ".yml" {
			var doc any
			if err := yaml.Unmarshal(b, &doc); err != nil {
				c.errf(path, "%s: %v", x, err)
				return nil
			}
			if b, err = json.Marshal(doc); err != nil {
				c.errf(path, "%s: %v", x, err)
				return nil
			}
		}
		raw = b
		loc = (&url.URL{Scheme: "file", Path: filepath.ToSlash(x)}).String()
	case map[string]any:
		b, err := json.Marshal(x)
		if err != nil {
			c.errf(path, "%v", err)
			return nil
		}
		raw = b
	default:
		c.errf(path, "must be an inline JSON Schema object or the path of a schema file")
		return nil
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		c.errf(path, "not JSON: %v", err)
		return nil
	}
	comp := jsonschema.NewCompiler()
	comp.DefaultDraft(jsonschema.Draft2020)
	if err := comp.AddResource(loc, doc); err != nil {
		c.errf(path, "%v", err)
		return nil
	}
	s, err := comp.Compile(loc)
	if err != nil {
		c.errf(path, "invalid JSON Schema: %v", err)
		return nil
	}
	return s
}

func (c *compiler) jsonTemplate(scope *Scope, path string, v any) *JSONTemplate {
	switch x := v.(type) {
	case string:
		t := c.template(scope, path, x)
		if t == nil {
			return &JSONTemplate{kind: 'l', lit: x}
		}
		if t.IsLiteral() {
			return &JSONTemplate{kind: 'l', lit: t.lits[0]}
		}
		return &JSONTemplate{kind: 't', tmpl: t}
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		jt := &JSONTemplate{kind: 'o'}
		for _, k := range keys {
			jt.obj = append(jt.obj, jsonField{key: k, val: c.jsonTemplate(scope, path+"."+k, x[k])})
		}
		return jt
	case []any:
		jt := &JSONTemplate{kind: 'a'}
		for i, e := range x {
			jt.arr = append(jt.arr, c.jsonTemplate(scope, fmt.Sprintf("%s[%d]", path, i), e))
		}
		return jt
	default:
		return &JSONTemplate{kind: 'l', lit: x}
	}
}

// ParseExtractor parses an extraction rule:
//
//	$.path            JSONPath into the response body
//	header:Name       response header
//	cookie:name       cookie set by the response
//	regex:pattern     first capture group (or whole match) in the body
//	css:selector      text of the first match; css:selector@attr for an attribute
//	status | body     the status code or the whole body
func ParseExtractor(name, rule string) (Extractor, error) {
	ex := Extractor{Var: name, Src: rule}
	if !identRe.MatchString(name) {
		return ex, fmt.Errorf("variable name %q must be an identifier (letters, digits, underscore)", name)
	}
	if IsReserved(name) {
		return ex, fmt.Errorf("%q is a built-in name; choose another variable name", name)
	}
	rule = strings.TrimSpace(rule)
	switch {
	case strings.HasPrefix(rule, "$"):
		g, err := JSONPathToGJSON(rule)
		if err != nil {
			return ex, err
		}
		ex.Kind, ex.GJSON = ExtractJSON, g
	case strings.HasPrefix(rule, "header:"):
		ex.Kind, ex.Src = ExtractHeader, strings.TrimSpace(strings.TrimPrefix(rule, "header:"))
	case strings.HasPrefix(rule, "cookie:"):
		ex.Kind, ex.Src = ExtractCookie, strings.TrimSpace(strings.TrimPrefix(rule, "cookie:"))
	case strings.HasPrefix(rule, "regex:"):
		re, err := regexp.Compile(strings.TrimPrefix(rule, "regex:"))
		if err != nil {
			return ex, fmt.Errorf("invalid regex: %w", err)
		}
		ex.Kind, ex.Regex = ExtractRegex, re
	case strings.HasPrefix(rule, "css:"):
		sel := strings.TrimSpace(strings.TrimPrefix(rule, "css:"))
		if at := strings.LastIndex(sel, "@"); at > 0 {
			ex.Attr = sel[at+1:]
			sel = sel[:at]
		}
		cs, err := cascadia.Parse(sel)
		if err != nil {
			return ex, fmt.Errorf("invalid CSS selector: %w", err)
		}
		ex.Kind, ex.CSS = ExtractCSS, cs
	case rule == "status":
		ex.Kind = ExtractStatus
	case rule == "body":
		ex.Kind = ExtractBody
	default:
		return ex, fmt.Errorf("unknown extractor %q (use $.path, header:, cookie:, regex:, css:, status or body)", rule)
	}
	return ex, nil
}

func dedupe(in []string) []string {
	seen := make(map[string]bool, len(in))
	out := make([]string, 0, len(in))
	for _, v := range in {
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	return out
}

var undeclaredRe = regexp.MustCompile(`undeclared reference to '([^']+)'`)

func explainUndeclared(err error) error {
	if m := undeclaredRe.FindStringSubmatch(err.Error()); m != nil {
		return fmt.Errorf("%w (is %q extracted in an earlier step, listed in a script step's sets, or defined under vars?)", err, m[1])
	}
	return err
}

// ValidationError lists every problem found in a scenario.
type ValidationError struct {
	Problems []string
}

func (e *ValidationError) Error() string {
	if len(e.Problems) == 1 {
		return "invalid scenario: " + e.Problems[0]
	}
	return "invalid scenario:\n  - " + strings.Join(e.Problems, "\n  - ")
}

// Value evaluates the JSON template into plain Go values.
func (j *JSONTemplate) Value(vars interpreter.Activation) (any, error) {
	switch j.kind {
	case 't':
		return j.tmpl.Value(vars)
	case 'o':
		m := make(map[string]any, len(j.obj))
		for _, f := range j.obj {
			v, err := f.val.Value(vars)
			if err != nil {
				return nil, err
			}
			m[f.key] = v
		}
		return m, nil
	case 'a':
		a := make([]any, len(j.arr))
		for i, e := range j.arr {
			v, err := e.Value(vars)
			if err != nil {
				return nil, err
			}
			a[i] = v
		}
		return a, nil
	default:
		return j.lit, nil
	}
}

// Source returns every template source in the JSON value, joined by
// newlines, for static analysis such as finding referenced feeders.
func (j *JSONTemplate) Source() string {
	var b strings.Builder
	var walk func(*JSONTemplate)
	walk = func(n *JSONTemplate) {
		switch n.kind {
		case 't':
			b.WriteString(n.tmpl.String())
			b.WriteByte('\n')
		case 'o':
			for _, f := range n.obj {
				walk(f.val)
			}
		case 'a':
			for _, e := range n.arr {
				walk(e)
			}
		}
	}
	walk(j)
	return b.String()
}
