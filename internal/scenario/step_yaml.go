package scenario

import (
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"sort"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"
)

// httpMethods maps the step keys that make a request to their HTTP method.
var httpMethods = map[string]string{
	"get": "GET", "post": "POST", "put": "PUT", "patch": "PATCH",
	"delete": "DELETE", "head": "HEAD", "options": "OPTIONS",
}

// kindKeys are the step keys that name a step kind, besides HTTP methods,
// in the order they are listed in error messages.
var kindKeys = []string{"think", "branch", "loop", "while", "group", "script", "graphql", "sse", "ws", "send", "expect", "grpc"}

// requestKeys are the HTTP request parts shared by request-like steps.
var requestKeys = []string{"headers", "query", "json", "body", "form", "check", "extract", "timeout"}

// stepKeys lists the keys each kind of step accepts besides its own key,
// name and if. HTTP methods share the "request" entry.
var stepKeys = map[string][]string{
	"request": requestKeys,
	"think":   nil,
	"branch":  nil,
	"script":  nil,
	"loop":    {"steps"},
	"while":   {"steps", "max"},
	"group":   {"steps"},
	"graphql": {"query", "variables", "operationName", "persisted", "headers", "check", "extract", "timeout"},
	"sse":     append([]string{"method", "until"}, requestKeys...),
	"ws":      {"headers", "subprotocols", "timeout", "steps"},
	"send":    nil,
	"expect":  {"extract"},
	"grpc":    {"target", "message", "metadata", "protoset", "proto", "importPaths", "check", "extract", "timeout"},
}

// UnmarshalYAML reads the compact step syntax, for example
//
//   - get: /api/products
//     check: { status: 200 }
//   - think: 2s..6s
func (s *Step) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind != yaml.MappingNode {
		return fmt.Errorf("line %d: a step must be a mapping such as {get: /path}", n.Line)
	}
	fields := map[string]*yaml.Node{}
	for i := 0; i+1 < len(n.Content); i += 2 {
		k := n.Content[i].Value
		if _, dup := fields[k]; dup {
			return fmt.Errorf("line %d: duplicate key %q in step", n.Content[i].Line, k)
		}
		fields[k] = n.Content[i+1]
	}

	var kinds []string
	for k := range fields {
		if _, ok := httpMethods[k]; ok {
			kinds = append(kinds, k)
		}
	}
	for _, k := range kindKeys {
		if _, ok := fields[k]; ok {
			kinds = append(kinds, k)
		}
	}
	sort.Strings(kinds)
	switch len(kinds) {
	case 0:
		return fmt.Errorf("line %d: step needs one of get, post, put, patch, delete, head, options, %s", n.Line, strings.Join(kindKeys, ", "))
	case 1:
	default:
		return fmt.Errorf("line %d: step has more than one action (%s); split it into separate steps", n.Line, strings.Join(kinds, ", "))
	}
	kind := kinds[0]
	if err := checkStepKeys(n, kind); err != nil {
		return err
	}

	out := Step{}
	if v, ok := fields["name"]; ok {
		out.Name = v.Value
	}
	if v, ok := fields["if"]; ok {
		out.If = v.Value
	}

	if method, ok := httpMethods[kind]; ok {
		out.Kind = StepRequest
		r := &Request{Method: method, URL: fields[kind].Value}
		if err := decodeRequest(r, fields); err != nil {
			return err
		}
		out.Request = r
		*s = out
		return nil
	}

	switch kind {
	case "think":
		out.Kind = StepThink
		var t ThinkTime
		if err := fields["think"].Decode(&t); err != nil {
			return err
		}
		out.Think = &t
	case "branch":
		out.Kind = StepBranch
		if err := fields["branch"].Decode(&out.Branch); err != nil {
			return err
		}
	case "loop":
		out.Kind = StepLoop
		c, err := strconv.Atoi(fields["loop"].Value)
		if err != nil || c < 1 {
			return fmt.Errorf("line %d: loop takes a positive count, for example loop: 3", fields["loop"].Line)
		}
		l := &Loop{Count: c}
		if err := decodeSteps(fields, &l.Steps); err != nil {
			return err
		}
		out.Loop = l
	case "while":
		out.Kind = StepWhile
		l := &Loop{Cond: fields["while"].Value, Max: 1000}
		if v, ok := fields["max"]; ok {
			m, err := strconv.Atoi(v.Value)
			if err != nil || m < 1 {
				return fmt.Errorf("line %d: max must be a positive integer", v.Line)
			}
			l.Max = m
		}
		if err := decodeSteps(fields, &l.Steps); err != nil {
			return err
		}
		out.Loop = l
	case "group":
		out.Kind = StepGroup
		g := &Group{Name: fields["group"].Value}
		if err := decodeSteps(fields, &g.Steps); err != nil {
			return err
		}
		out.Group = g
	case "script":
		out.Kind = StepScript
		out.Script = fields["script"].Value
	case "graphql":
		out.Kind = StepGraphQL
		g, err := decodeGraphQL(fields)
		if err != nil {
			return err
		}
		out.GraphQL = g
	case "sse":
		out.Kind = StepSSE
		e, err := decodeSSE(fields)
		if err != nil {
			return err
		}
		out.SSE = e
	case "ws":
		out.Kind = StepWS
		w, err := decodeWS(fields)
		if err != nil {
			return err
		}
		out.WS = w
	case "send":
		out.Kind = StepSend
		v := fields["send"]
		out.Send = &Send{}
		if v.Kind == yaml.ScalarNode {
			out.Send.Text = v.Value
		} else {
			var j any
			if err := v.Decode(&j); err != nil {
				return err
			}
			out.Send.JSON = normalizeYAML(j)
		}
	case "expect":
		out.Kind = StepExpect
		v := fields["expect"]
		out.Expect = &Expect{}
		switch {
		case v.Kind == yaml.ScalarNode && v.Tag != "!!null":
			out.Expect.Match = v.Value
		case v.Kind == yaml.MappingNode:
			if err := decodeStrict(v, "expect", out.Expect); err != nil {
				return err
			}
		case v.Tag != "!!null":
			return fmt.Errorf("line %d: expect takes a regex or {match, json, timeout}", v.Line)
		}
		if x, ok := fields["extract"]; ok {
			if err := x.Decode(&out.Expect.Extract); err != nil {
				return err
			}
		}
	case "grpc":
		out.Kind = StepGRPC
		g, err := decodeGRPC(fields)
		if err != nil {
			return err
		}
		out.GRPC = g
	}
	*s = out
	return nil
}

// checkStepKeys rejects keys the step's kind does not accept, naming the
// line. A key that belongs to another kind gets a more helpful message.
func checkStepKeys(n *yaml.Node, kind string) error {
	entry := kind
	if _, ok := httpMethods[kind]; ok {
		entry = "request"
	}
	allowed := map[string]bool{"name": true, "if": true, kind: true}
	for _, k := range stepKeys[entry] {
		allowed[k] = true
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		k := n.Content[i]
		if allowed[k.Value] {
			continue
		}
		for other, keys := range stepKeys {
			if other != entry && slices.Contains(keys, k.Value) {
				return fmt.Errorf("line %d: %q does not apply to %s steps", k.Line, k.Value, kind)
			}
		}
		return fmt.Errorf("line %d: unknown key %q in %s step", k.Line, k.Value, kind)
	}
	return nil
}

func decodeSteps(fields map[string]*yaml.Node, dst *[]Step) error {
	v, ok := fields["steps"]
	if !ok {
		return fmt.Errorf("missing steps")
	}
	return v.Decode(dst)
}

func decodeRequest(r *Request, f map[string]*yaml.Node) error {
	if v, ok := f["query"]; ok {
		if err := v.Decode(&r.Query); err != nil {
			return err
		}
	}
	bodies := 0
	if v, ok := f["json"]; ok {
		bodies++
		var j any
		if err := v.Decode(&j); err != nil {
			return err
		}
		r.JSON = normalizeYAML(j)
	}
	if v, ok := f["body"]; ok {
		bodies++
		r.Body = v.Value
	}
	if v, ok := f["form"]; ok {
		bodies++
		if err := v.Decode(&r.Form); err != nil {
			return err
		}
	}
	if bodies > 1 {
		return fmt.Errorf("request to %s sets more than one of json, body and form", r.URL)
	}
	return decodeExchange(r, f)
}

// decodeExchange reads the parts every request-like step shares:
// headers, check, extract and timeout.
func decodeExchange(r *Request, f map[string]*yaml.Node) error {
	if v, ok := f["headers"]; ok {
		if err := v.Decode(&r.Headers); err != nil {
			return err
		}
	}
	if v, ok := f["check"]; ok {
		r.Check = &Check{}
		if err := decodeStrict(v, "check", r.Check); err != nil {
			return err
		}
	}
	if v, ok := f["extract"]; ok {
		if err := v.Decode(&r.Extract); err != nil {
			return err
		}
	}
	if v, ok := f["timeout"]; ok {
		if err := v.Decode(&r.Timeout); err != nil {
			return err
		}
	}
	return nil
}

func decodeGraphQL(f map[string]*yaml.Node) (*GraphQL, error) {
	g := &GraphQL{Request: Request{Method: "POST", URL: f["graphql"].Value}}
	if err := decodeExchange(&g.Request, f); err != nil {
		return nil, err
	}
	if v, ok := f["query"]; ok {
		g.Query = v.Value
	}
	if v, ok := f["operationName"]; ok {
		g.OperationName = v.Value
	}
	if v, ok := f["variables"]; ok {
		var vars any
		if err := v.Decode(&vars); err != nil {
			return nil, err
		}
		g.Variables = normalizeYAML(vars)
	}
	if v, ok := f["persisted"]; ok {
		switch {
		case v.Kind == yaml.ScalarNode && v.Tag == "!!bool":
			var on bool
			if err := v.Decode(&on); err != nil {
				return nil, err
			}
			if on {
				g.Persisted = &Persisted{}
			}
		case v.Kind == yaml.MappingNode:
			g.Persisted = &Persisted{}
			if err := decodeStrict(v, "persisted", g.Persisted); err != nil {
				return nil, err
			}
		default:
			return nil, fmt.Errorf("line %d: persisted takes true or {sha256: <hex>}", v.Line)
		}
	}
	return g, nil
}

func decodeSSE(f map[string]*yaml.Node) (*SSE, error) {
	e := &SSE{Request: Request{URL: f["sse"].Value}}
	if err := decodeRequest(&e.Request, f); err != nil {
		return nil, err
	}
	e.Method = "GET"
	if e.JSON != nil || e.Body != "" || len(e.Form) > 0 {
		e.Method = "POST"
	}
	if v, ok := f["method"]; ok {
		m, ok := httpMethods[strings.ToLower(v.Value)]
		if !ok {
			return nil, fmt.Errorf("line %d: method must be an HTTP method such as GET or POST", v.Line)
		}
		e.Method = m
	}
	if v, ok := f["until"]; ok {
		if err := decodeStrict(v, "until", &e.Until); err != nil {
			return nil, err
		}
	}
	return e, nil
}

func decodeWS(f map[string]*yaml.Node) (*WebSocket, error) {
	w := &WebSocket{URL: f["ws"].Value}
	if v, ok := f["headers"]; ok {
		if err := v.Decode(&w.Headers); err != nil {
			return nil, err
		}
	}
	if v, ok := f["subprotocols"]; ok {
		if err := v.Decode(&w.Subprotocols); err != nil {
			return nil, err
		}
	}
	if v, ok := f["timeout"]; ok {
		if err := v.Decode(&w.Timeout); err != nil {
			return nil, err
		}
	}
	if _, ok := f["steps"]; !ok {
		return nil, fmt.Errorf("line %d: ws needs steps to run on the connection", f["ws"].Line)
	}
	return w, decodeSteps(f, &w.Steps)
}

func decodeGRPC(f map[string]*yaml.Node) (*GRPC, error) {
	g := &GRPC{Method: f["grpc"].Value}
	if v, ok := f["target"]; ok {
		g.Target = v.Value
	}
	if v, ok := f["message"]; ok {
		var m any
		if err := v.Decode(&m); err != nil {
			return nil, err
		}
		g.Message = normalizeYAML(m)
	}
	if v, ok := f["metadata"]; ok {
		if err := v.Decode(&g.Metadata); err != nil {
			return nil, err
		}
	}
	if v, ok := f["protoset"]; ok {
		g.Protoset = v.Value
	}
	if v, ok := f["proto"]; ok {
		if v.Kind == yaml.ScalarNode {
			g.Proto = []string{v.Value}
		} else if err := v.Decode(&g.Proto); err != nil {
			return nil, err
		}
	}
	if v, ok := f["importPaths"]; ok {
		if err := v.Decode(&g.ImportPaths); err != nil {
			return nil, err
		}
	}
	if v, ok := f["check"]; ok {
		g.Check = &GRPCCheck{}
		if err := decodeStrict(v, "check", g.Check); err != nil {
			return nil, err
		}
	}
	if v, ok := f["extract"]; ok {
		if err := v.Decode(&g.Extract); err != nil {
			return nil, err
		}
	}
	if v, ok := f["timeout"]; ok {
		if err := v.Decode(&g.Timeout); err != nil {
			return nil, err
		}
	}
	return g, nil
}

// UnmarshalYAML accepts one code name or a list.
func (c *GRPCCodes) UnmarshalYAML(n *yaml.Node) error {
	switch n.Kind {
	case yaml.ScalarNode:
		*c = GRPCCodes{n.Value}
		return nil
	case yaml.SequenceNode:
		var l []string
		if err := n.Decode(&l); err != nil {
			return err
		}
		*c = l
		return nil
	}
	return fmt.Errorf("line %d: status must be a code name such as OK or a list of them", n.Line)
}

// decodeStrict decodes a mapping into the struct v, rejecting keys that
// v does not define. yaml.v3 ignores KnownFields when a node is decoded on
// its own, so without this a typo such as "stauts" inside a check would be
// silently dropped and the check would pass vacuously.
func decodeStrict(n *yaml.Node, what string, v any) error {
	if n.Kind == yaml.MappingNode {
		known := yamlKeys(reflect.TypeOf(v))
		for i := 0; i+1 < len(n.Content); i += 2 {
			k := n.Content[i]
			if !known[k.Value] {
				return fmt.Errorf("line %d: unknown key %q in %s", k.Line, k.Value, what)
			}
		}
	}
	return n.Decode(v)
}

// yamlKeys lists the YAML keys a struct type (or pointer to one) accepts.
func yamlKeys(t reflect.Type) map[string]bool {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	out := map[string]bool{}
	if t.Kind() != reflect.Struct {
		return out
	}
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		name, _, _ := strings.Cut(f.Tag.Get("yaml"), ",")
		switch {
		case name == "-":
		case name != "":
			out[name] = true
		case f.IsExported():
			out[strings.ToLower(f.Name)] = true
		}
	}
	return out
}

// normalizeYAML converts map[string]any trees produced by yaml.v3 into
// JSON-compatible values.
func normalizeYAML(v any) any {
	switch t := v.(type) {
	case map[string]any:
		for k, x := range t {
			t[k] = normalizeYAML(x)
		}
		return t
	case map[any]any:
		m := make(map[string]any, len(t))
		for k, x := range t {
			m[fmt.Sprint(k)] = normalizeYAML(x)
		}
		return m
	case []any:
		for i, x := range t {
			t[i] = normalizeYAML(x)
		}
		return t
	default:
		return v
	}
}

// toMap renders a step back into its compact form.
func (s Step) toMap() map[string]any {
	m := map[string]any{}
	if s.Name != "" {
		m["name"] = s.Name
	}
	if s.If != "" {
		m["if"] = s.If
	}
	switch s.Kind {
	case StepRequest:
		r := s.Request
		m[strings.ToLower(r.Method)] = r.URL
		requestToMap(m, r)
	case StepThink:
		m["think"] = *s.Think
	case StepBranch:
		m["branch"] = s.Branch
	case StepLoop:
		m["loop"] = s.Loop.Count
		m["steps"] = s.Loop.Steps
	case StepWhile:
		m["while"] = s.Loop.Cond
		m["max"] = s.Loop.Max
		m["steps"] = s.Loop.Steps
	case StepGroup:
		m["group"] = s.Group.Name
		m["steps"] = s.Group.Steps
	case StepScript:
		m["script"] = s.Script
	case StepGraphQL:
		g := s.GraphQL
		m["graphql"] = g.URL
		if g.Query != "" {
			m["query"] = g.Query
		}
		if g.Variables != nil {
			m["variables"] = g.Variables
		}
		if g.OperationName != "" {
			m["operationName"] = g.OperationName
		}
		if g.Persisted != nil {
			if g.Persisted.SHA256 != "" {
				m["persisted"] = g.Persisted
			} else {
				m["persisted"] = true
			}
		}
		exchangeToMap(m, &g.Request)
	case StepSSE:
		e := s.SSE
		m["sse"] = e.URL
		requestToMap(m, &e.Request)
		m["method"] = e.Method
		if e.Until != (SSEUntil{}) {
			m["until"] = e.Until
		}
	case StepWS:
		w := s.WS
		m["ws"] = w.URL
		if len(w.Headers) > 0 {
			m["headers"] = w.Headers
		}
		if len(w.Subprotocols) > 0 {
			m["subprotocols"] = w.Subprotocols
		}
		if w.Timeout > 0 {
			m["timeout"] = w.Timeout
		}
		m["steps"] = w.Steps
	case StepSend:
		if s.Send.JSON != nil {
			m["send"] = s.Send.JSON
		} else {
			m["send"] = s.Send.Text
		}
	case StepExpect:
		m["expect"] = s.Expect
		if len(s.Expect.Extract) > 0 {
			m["extract"] = s.Expect.Extract
		}
	case StepGRPC:
		g := s.GRPC
		m["grpc"] = g.Method
		for k, v := range map[string]string{"target": g.Target, "protoset": g.Protoset} {
			if v != "" {
				m[k] = v
			}
		}
		if g.Message != nil {
			m["message"] = g.Message
		}
		if len(g.Metadata) > 0 {
			m["metadata"] = g.Metadata
		}
		if len(g.Proto) > 0 {
			m["proto"] = g.Proto
		}
		if len(g.ImportPaths) > 0 {
			m["importPaths"] = g.ImportPaths
		}
		if g.Check != nil {
			m["check"] = g.Check
		}
		if len(g.Extract) > 0 {
			m["extract"] = g.Extract
		}
		if g.Timeout > 0 {
			m["timeout"] = g.Timeout
		}
	}
	return m
}

// requestToMap renders an HTTP request's parts.
func requestToMap(m map[string]any, r *Request) {
	exchangeToMap(m, r)
	if len(r.Query) > 0 {
		m["query"] = r.Query
	}
	if r.JSON != nil {
		m["json"] = r.JSON
	}
	if r.Body != "" {
		m["body"] = r.Body
	}
	if len(r.Form) > 0 {
		m["form"] = r.Form
	}
}

// exchangeToMap renders the parts every request-like step shares.
func exchangeToMap(m map[string]any, r *Request) {
	if len(r.Headers) > 0 {
		m["headers"] = r.Headers
	}
	if r.Check != nil {
		m["check"] = r.Check
	}
	if len(r.Extract) > 0 {
		m["extract"] = r.Extract
	}
	if r.Timeout > 0 {
		m["timeout"] = r.Timeout
	}
}

func (s Step) MarshalYAML() (any, error) { return s.toMap(), nil }

func (s Step) MarshalJSON() ([]byte, error) { return json.Marshal(s.toMap()) }

// UnmarshalJSON accepts the same compact form as YAML.
func (s *Step) UnmarshalJSON(b []byte) error {
	var n yaml.Node
	if err := yaml.Unmarshal(b, &n); err != nil {
		return err
	}
	if n.Kind == yaml.DocumentNode && len(n.Content) == 1 {
		return s.UnmarshalYAML(n.Content[0])
	}
	return s.UnmarshalYAML(&n)
}

// StatusMatcher matches HTTP status codes.
type StatusMatcher struct {
	codes   []int
	classes []int // 2 for 2xx
}

// Empty reports whether no status constraint was given.
func (m StatusMatcher) Empty() bool { return len(m.codes) == 0 && len(m.classes) == 0 }

// Match reports whether code satisfies the matcher.
func (m StatusMatcher) Match(code int) bool {
	for _, c := range m.codes {
		if c == code {
			return true
		}
	}
	for _, c := range m.classes {
		if code/100 == c {
			return true
		}
	}
	return false
}

func (m StatusMatcher) String() string {
	var parts []string
	for _, c := range m.codes {
		parts = append(parts, strconv.Itoa(c))
	}
	for _, c := range m.classes {
		parts = append(parts, strconv.Itoa(c)+"xx")
	}
	return strings.Join(parts, ",")
}

func (m *StatusMatcher) add(s string) error {
	s = strings.ToLower(strings.TrimSpace(s))
	if len(s) == 3 && strings.HasSuffix(s, "xx") && s[0] >= '1' && s[0] <= '5' {
		m.classes = append(m.classes, int(s[0]-'0'))
		return nil
	}
	c, err := strconv.Atoi(s)
	if err != nil || c < 100 || c > 599 {
		return fmt.Errorf("invalid status %q (use 200, [200, 201] or \"2xx\")", s)
	}
	m.codes = append(m.codes, c)
	return nil
}

// ParseStatus parses "200", "2xx" or "200,201".
func ParseStatus(s string) (StatusMatcher, error) {
	var m StatusMatcher
	for _, p := range strings.Split(s, ",") {
		if err := m.add(p); err != nil {
			return m, err
		}
	}
	return m, nil
}

func (m *StatusMatcher) UnmarshalYAML(n *yaml.Node) error {
	*m = StatusMatcher{}
	switch n.Kind {
	case yaml.ScalarNode:
		v, err := ParseStatus(n.Value)
		if err != nil {
			return nodeErr(n, err)
		}
		*m = v
	case yaml.SequenceNode:
		for _, c := range n.Content {
			if err := m.add(c.Value); err != nil {
				return nodeErr(c, err)
			}
		}
	default:
		return fmt.Errorf("line %d: status must be a code or list of codes", n.Line)
	}
	return nil
}

func (m StatusMatcher) MarshalYAML() (any, error) { return m.String(), nil }

func (m StatusMatcher) MarshalJSON() ([]byte, error) { return json.Marshal(m.String()) }

func (m *StatusMatcher) UnmarshalJSON(b []byte) error {
	var n yaml.Node
	if err := yaml.Unmarshal(b, &n); err != nil {
		return err
	}
	return m.UnmarshalYAML(n.Content[0])
}
