package scenario

import (
	"encoding/json"
	"fmt"
	"reflect"
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

var kindKeys = []string{"think", "branch", "loop", "while", "group", "script"}

// requestOnlyKeys may only appear on request steps.
var requestOnlyKeys = map[string]bool{
	"headers": true, "query": true, "json": true, "body": true, "form": true,
	"check": true, "extract": true, "timeout": true,
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
		return fmt.Errorf("line %d: step needs one of get, post, put, patch, delete, head, options, think, branch, loop, while, group or script", n.Line)
	case 1:
	default:
		return fmt.Errorf("line %d: step has more than one action (%s); split it into separate steps", n.Line, strings.Join(kinds, ", "))
	}
	kind := kinds[0]

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
	} else {
		for k, v := range fields {
			if requestOnlyKeys[k] {
				return fmt.Errorf("line %d: %q only applies to request steps, not %s", v.Line, k, kind)
			}
		}
	}

	allowed := map[string]bool{"name": true, "if": true, kind: true}
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
		allowed["steps"] = true
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
		allowed["steps"], allowed["max"] = true, true
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
		allowed["steps"] = true
		g := &Group{Name: fields["group"].Value}
		if err := decodeSteps(fields, &g.Steps); err != nil {
			return err
		}
		out.Group = g
	case "script":
		out.Kind = StepScript
		out.Script = fields["script"].Value
	default:
		for k := range requestOnlyKeys {
			allowed[k] = true
		}
	}

	for k, v := range fields {
		if !allowed[k] {
			return fmt.Errorf("line %d: unknown key %q in %s step", v.Line, k, kind)
		}
	}
	*s = out
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
	if v, ok := f["headers"]; ok {
		if err := v.Decode(&r.Headers); err != nil {
			return err
		}
	}
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
		if len(r.Headers) > 0 {
			m["headers"] = r.Headers
		}
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
		if r.Check != nil {
			m["check"] = r.Check
		}
		if len(r.Extract) > 0 {
			m["extract"] = r.Extract
		}
		if r.Timeout > 0 {
			m["timeout"] = r.Timeout
		}
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
	}
	return m
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
