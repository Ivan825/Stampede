package scenario

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"cel.dev/cel-go/interpreter"
	"github.com/andybalholm/cascadia"
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
	// NeedsJSON is set when the check reads the parsed body.
	NeedsJSON bool
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
func Compile(s *Scenario) (*Program, error) {
	c := &compiler{prog: &Program{Scenario: s}}
	return c.compile()
}

type compiler struct {
	prog *Program
	errs []string
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
		cs.Req, vars = c.request(path, scope, r, vars)
		cs.ID = len(c.prog.Steps)
		c.prog.Steps = append(c.prog.Steps, cs)
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
		c.errf(path, "script steps are planned and not available in this build")
	}
	return cs, vars
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

func (c *compiler) request(path string, scope *Scope, r *Request, vars []string) (*CRequest, []string) {
	cr := &CRequest{Method: r.Method, Timeout: r.Timeout}
	if strings.TrimSpace(r.URL) == "" {
		c.errf(path, "%s needs a path or URL", strings.ToLower(r.Method))
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

	names := make([]string, 0, len(r.Extract))
	for k := range r.Extract {
		names = append(names, k)
	}
	sort.Strings(names)
	for _, name := range names {
		ex, err := ParseExtractor(name, r.Extract[name])
		if err != nil {
			c.errf(path+".extract."+name, "%v", err)
			continue
		}
		cr.Extract = append(cr.Extract, ex)
		vars = dedupe(append(vars, name))
	}
	return cr, vars
}

func (c *compiler) check(path string, ch *Check, vars []string) *CCheck {
	cc := &CCheck{Status: ch.Status, MaxLatency: ch.MaxLatency}
	scope, _ := NewScope(vars...)
	if ch.BodyContains != "" {
		cc.BodyContains = c.template(scope, path+".bodyContains", ch.BodyContains)
	}
	keys := make([]string, 0, len(ch.JSON))
	for k := range ch.JSON {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		g, err := JSONPathToGJSON(k)
		if err != nil {
			c.errf(path+".json", "%v", err)
			continue
		}
		jc := JSONCheck{Path: k, GJSON: g, Expected: normalizeYAML(ch.JSON[k])}
		if s, ok := jc.Expected.(string); ok && s == "exists" {
			jc.Exists = true
		}
		cc.JSON = append(cc.JSON, jc)
		cc.NeedsJSON = true
	}
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
	return cc
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
		return fmt.Errorf("%w (is %q extracted in an earlier step, or defined under vars?)", err, m[1])
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
