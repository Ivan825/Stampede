// Command apidocs writes the REST API reference (docs/reference/api.md)
// from the OpenAPI document, so the page cannot drift from the API:
//
//	go run ./scripts/apidocs -in api/openapi.yaml -out docs/reference/api.md
//
// Operations are grouped by their first tag, in the order the document
// lists its tags, then by path in document order. Every schema in
// components is listed after the operations.
package main

import (
	"bytes"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"unicode"

	"go.yaml.in/yaml/v3"
)

func main() {
	in := flag.String("in", "api/openapi.yaml", "OpenAPI document")
	out := flag.String("out", "docs/reference/api.md", "Markdown file to write (- for stdout)")
	flag.Parse()
	src, err := os.ReadFile(*in)
	if err != nil {
		fail(err)
	}
	var b bytes.Buffer
	if err := render(&b, src, *in); err != nil {
		fail(fmt.Errorf("%s: %w", *in, err))
	}
	if *out == "-" {
		_, err = os.Stdout.Write(b.Bytes())
	} else {
		err = os.WriteFile(*out, b.Bytes(), 0o644) //nolint:gosec // a docs page
	}
	if err != nil {
		fail(err)
	}
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "apidocs:", err)
	os.Exit(1)
}

// ordered is a YAML mapping that keeps its keys in document order.
type ordered[T any] struct {
	keys []string
	vals map[string]T
}

func (o *ordered[T]) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind != yaml.MappingNode {
		return fmt.Errorf("line %d: expected a mapping", n.Line)
	}
	o.vals = map[string]T{}
	for i := 0; i+1 < len(n.Content); i += 2 {
		var v T
		if err := n.Content[i+1].Decode(&v); err != nil {
			return err
		}
		k := n.Content[i].Value
		o.keys = append(o.keys, k)
		o.vals[k] = v
	}
	return nil
}

type document struct {
	Info struct {
		Title       string `yaml:"title"`
		Version     string `yaml:"version"`
		Description string `yaml:"description"`
	} `yaml:"info"`
	Servers []struct {
		URL string `yaml:"url"`
	} `yaml:"servers"`
	Tags []struct {
		Name        string `yaml:"name"`
		Description string `yaml:"description"`
	} `yaml:"tags"`
	Paths      ordered[pathItem] `yaml:"paths"`
	Components struct {
		SecuritySchemes ordered[struct {
			Type        string `yaml:"type"`
			Scheme      string `yaml:"scheme"`
			In          string `yaml:"in"`
			Name        string `yaml:"name"`
			Description string `yaml:"description"`
		}] `yaml:"securitySchemes"`
		Parameters ordered[parameter] `yaml:"parameters"`
		Responses  ordered[response]  `yaml:"responses"`
		Schemas    ordered[*schema]   `yaml:"schemas"`
	} `yaml:"components"`
}

type pathItem struct {
	Parameters []parameter `yaml:"parameters"`
	Get        *operation  `yaml:"get"`
	Put        *operation  `yaml:"put"`
	Post       *operation  `yaml:"post"`
	Patch      *operation  `yaml:"patch"`
	Delete     *operation  `yaml:"delete"`
}

func (p pathItem) operations() []struct {
	method string
	op     *operation
} {
	var out []struct {
		method string
		op     *operation
	}
	for _, m := range []struct {
		method string
		op     *operation
	}{{"GET", p.Get}, {"POST", p.Post}, {"PUT", p.Put}, {"PATCH", p.Patch}, {"DELETE", p.Delete}} {
		if m.op != nil {
			out = append(out, m)
		}
	}
	return out
}

type operation struct {
	Tags        []string `yaml:"tags"`
	OperationID string   `yaml:"operationId"`
	Summary     string   `yaml:"summary"`
	Description string   `yaml:"description"`
	// Security is nil when the document's default applies and empty when
	// the operation needs no authentication.
	Security    *[]map[string][]string `yaml:"security"`
	Parameters  []parameter            `yaml:"parameters"`
	RequestBody *struct {
		Required    bool               `yaml:"required"`
		Description string             `yaml:"description"`
		Content     ordered[mediaType] `yaml:"content"`
	} `yaml:"requestBody"`
	Responses ordered[response] `yaml:"responses"`
}

type parameter struct {
	Ref         string  `yaml:"$ref"`
	Name        string  `yaml:"name"`
	In          string  `yaml:"in"`
	Required    bool    `yaml:"required"`
	Description string  `yaml:"description"`
	Schema      *schema `yaml:"schema"`
}

type response struct {
	Ref         string             `yaml:"$ref"`
	Description string             `yaml:"description"`
	Content     ordered[mediaType] `yaml:"content"`
}

type mediaType struct {
	Schema *schema `yaml:"schema"`
}

type schema struct {
	Ref                  string           `yaml:"$ref"`
	Type                 string           `yaml:"type"`
	Format               string           `yaml:"format"`
	Description          string           `yaml:"description"`
	Enum                 []any            `yaml:"enum"`
	Nullable             bool             `yaml:"nullable"`
	Default              any              `yaml:"default"`
	Required             []string         `yaml:"required"`
	Items                *schema          `yaml:"items"`
	Properties           ordered[*schema] `yaml:"properties"`
	AdditionalProperties yaml.Node        `yaml:"additionalProperties"`
	AllOf                []*schema        `yaml:"allOf"`
	OneOf                []*schema        `yaml:"oneOf"`
	AnyOf                []*schema        `yaml:"anyOf"`
}

// refName returns the last element of a local $ref.
func refName(ref string) string { return ref[strings.LastIndex(ref, "/")+1:] }

// slugger makes heading anchors the way GitHub and Starlight do: lower
// case, punctuation dropped, spaces to hyphens, and -1, -2 ... suffixes
// for repeats.
type slugger map[string]int

func (s slugger) slug(heading string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(heading) {
		switch {
		case r == ' ':
			b.WriteByte('-')
		case r == '-' || r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(r)
		}
	}
	base := b.String()
	n := s[base]
	s[base] = n + 1
	if n == 0 {
		return base
	}
	return fmt.Sprintf("%s-%d", base, n)
}

type renderer struct {
	doc     *document
	w       io.Writer
	schemas map[string]string // schema name to anchor
}

func render(w io.Writer, src []byte, path string) error {
	var doc document
	if err := yaml.Unmarshal(src, &doc); err != nil {
		return err
	}
	r := &renderer{doc: &doc, w: w, schemas: map[string]string{}}

	// Group operations by tag; anchors are assigned in page order.
	type op struct {
		method, path string
		item         pathItem
		op           *operation
		anchor       string
	}
	tagOrder := []string{}
	tagDesc := map[string]string{}
	for _, t := range doc.Tags {
		tagOrder = append(tagOrder, t.Name)
		tagDesc[t.Name] = t.Description
	}
	byTag := map[string][]*op{}
	for _, p := range doc.Paths.keys {
		item := doc.Paths.vals[p]
		for _, m := range item.operations() {
			tag := "other"
			if len(m.op.Tags) > 0 {
				tag = m.op.Tags[0]
			}
			if _, ok := tagDesc[tag]; !ok {
				tagOrder = append(tagOrder, tag)
				tagDesc[tag] = ""
			}
			byTag[tag] = append(byTag[tag], &op{method: m.method, path: p, item: item, op: m.op})
		}
	}
	sl := slugger{}
	sl.slug(doc.Info.Title) // the page title, removed by the site but kept on GitHub
	for _, h := range []string{"Authentication", "Operations"} {
		sl.slug(h)
	}
	tagAnchor := map[string]string{}
	for _, t := range tagOrder {
		if len(byTag[t]) == 0 {
			continue
		}
		tagAnchor[t] = sl.slug(title(t))
		for _, o := range byTag[t] {
			o.anchor = sl.slug(o.method + " " + o.path)
		}
	}
	sl.slug("Schemas")
	for _, name := range doc.Components.Schemas.keys {
		r.schemas[name] = sl.slug(name)
	}

	r.printf("# REST API reference\n\n")
	r.printf("<!-- Generated from %s by `make api-docs`. Do not edit by hand. -->\n\n", path)
	r.printf("%s, version %s.", doc.Info.Title, doc.Info.Version)
	if len(doc.Servers) > 0 {
		r.printf(" Every path below is relative to `%s` on the server, for example `http://localhost:8080%s/version`.", doc.Servers[0].URL, doc.Servers[0].URL)
	}
	r.printf(" The OpenAPI document itself is [api/openapi.yaml](../../api/openapi.yaml).\n\n")
	if d := strings.TrimSpace(doc.Info.Description); d != "" {
		r.printf("%s\n\n", d)
	}

	r.printf("## Authentication\n\n")
	r.printf("| Scheme | Type | Details |\n|---|---|---|\n")
	for _, k := range doc.Components.SecuritySchemes.keys {
		s := doc.Components.SecuritySchemes.vals[k]
		details := s.Description
		switch {
		case s.Type == "http":
			details = strings.TrimSpace(fmt.Sprintf("`Authorization: %s ...` %s", titleCase(s.Scheme), s.Description))
		case s.Type == "apiKey":
			details = strings.TrimSpace(fmt.Sprintf("%s `%s` %s", s.In, s.Name, s.Description))
		}
		r.printf("| `%s` | %s | %s |\n", k, s.Type, cell(details))
	}
	r.printf("\nOperations marked **no authentication** can be called without either.\n\n")

	r.printf("## Operations\n\n")
	for _, t := range tagOrder {
		if len(byTag[t]) == 0 {
			continue
		}
		r.printf("- [%s](#%s):", title(t), tagAnchor[t])
		for i, o := range byTag[t] {
			sep := ","
			if i == 0 {
				sep = ""
			}
			r.printf("%s [`%s %s`](#%s)", sep, o.method, o.path, o.anchor)
		}
		r.printf("\n")
	}
	r.printf("\n")

	for _, t := range tagOrder {
		if len(byTag[t]) == 0 {
			continue
		}
		r.printf("## %s\n\n", title(t))
		if d := strings.TrimSpace(tagDesc[t]); d != "" {
			r.printf("%s\n\n", d)
		}
		for _, o := range byTag[t] {
			if err := r.operation(o.method, o.path, o.item, o.op); err != nil {
				return fmt.Errorf("%s %s: %w", o.method, o.path, err)
			}
		}
	}

	r.printf("## Schemas\n\n")
	for _, name := range doc.Components.Schemas.keys {
		r.schema(name, doc.Components.Schemas.vals[name])
	}
	return nil
}

func (r *renderer) printf(format string, args ...any) { fmt.Fprintf(r.w, format, args...) }

func (r *renderer) operation(method, path string, item pathItem, op *operation) error {
	r.printf("### %s %s\n\n", method, path)
	if s := strings.TrimSpace(op.Summary); s != "" {
		r.printf("%s\n\n", sentence(s))
	}
	if d := strings.TrimSpace(op.Description); d != "" {
		r.printf("%s\n\n", d)
	}
	meta := []string{"Operation `" + op.OperationID + "`"}
	if op.Security != nil && len(*op.Security) == 0 {
		meta = append(meta, "**no authentication**")
	}
	r.printf("%s.\n\n", strings.Join(meta, ", "))

	var params []parameter
	for _, p := range append(append([]parameter{}, item.Parameters...), op.Parameters...) {
		if p.Ref != "" {
			ref, ok := r.doc.Components.Parameters.vals[refName(p.Ref)]
			if !ok {
				return fmt.Errorf("unknown parameter %s", p.Ref)
			}
			p = ref
		}
		params = append(params, p)
	}
	if len(params) > 0 {
		r.printf("| Parameter | In | Type | Required | Description |\n|---|---|---|---|---|\n")
		for _, p := range params {
			r.printf("| `%s` | %s | %s | %s | %s |\n", p.Name, p.In, r.typeOf(p.Schema), yesNo(p.Required || p.In == "path"), cell(p.Description+constraints(p.Schema)))
		}
		r.printf("\n")
	}

	if rb := op.RequestBody; rb != nil {
		req := "optional"
		if rb.Required {
			req = "required"
		}
		r.printf("**Request body** (%s):", req)
		for _, ct := range rb.Content.keys {
			r.printf(" `%s` %s", ct, r.typeOf(rb.Content.vals[ct].Schema))
		}
		r.printf("\n\n")
		if d := strings.TrimSpace(rb.Description); d != "" {
			r.printf("%s\n\n", d)
		}
		for _, ct := range rb.Content.keys {
			if s := inline(rb.Content.vals[ct].Schema); s != nil {
				r.fields(s.Properties, s.Required)
				break
			}
		}
	}

	codes := append([]string{}, op.Responses.keys...)
	sort.SliceStable(codes, func(i, j int) bool { return codes[i] < codes[j] })
	r.printf("| Status | Description | Body |\n|---|---|---|\n")
	for _, code := range codes {
		resp := op.Responses.vals[code]
		if resp.Ref != "" {
			ref, ok := r.doc.Components.Responses.vals[refName(resp.Ref)]
			if !ok {
				return fmt.Errorf("unknown response %s", resp.Ref)
			}
			resp = ref
		}
		var bodies []string
		for _, ct := range resp.Content.keys {
			bodies = append(bodies, fmt.Sprintf("`%s` %s", ct, r.typeOf(resp.Content.vals[ct].Schema)))
		}
		body := strings.Join(bodies, "<br>")
		if body == "" {
			body = "—"
		}
		r.printf("| %s | %s | %s |\n", code, cell(resp.Description), body)
	}
	r.printf("\n")
	// Objects written inline in a response get their fields listed here.
	for _, code := range codes {
		resp := op.Responses.vals[code]
		for _, ct := range resp.Content.keys {
			if s := inline(resp.Content.vals[ct].Schema); s != nil {
				r.printf("Fields of the %s response:\n\n", code)
				r.fields(s.Properties, s.Required)
				break
			}
		}
	}
	return nil
}

func (r *renderer) schema(name string, s *schema) {
	r.printf("### %s\n\n", name)
	if d := strings.TrimSpace(s.Description); d != "" {
		r.printf("%s\n\n", d)
	}
	// allOf of a reference and an object: the reference's fields plus these.
	props, required := s, s.Required
	if len(s.AllOf) > 0 {
		var bases []string
		props = nil
		for _, part := range s.AllOf {
			if part.Ref != "" {
				bases = append(bases, r.link(refName(part.Ref)))
				continue
			}
			props = part
			required = part.Required
		}
		if len(bases) > 0 {
			r.printf("Every field of %s, plus:\n\n", strings.Join(bases, " and "))
		}
	}
	if props == nil || len(props.Properties.keys) == 0 {
		if props != nil && len(s.AllOf) == 0 {
			r.printf("%s%s\n\n", r.typeOf(s), constraints(s))
		}
		return
	}
	r.fields(props.Properties, required)
}

// fields writes a table of an object's properties.
func (r *renderer) fields(props ordered[*schema], required []string) {
	r.printf("| Field | Type | Required | Description |\n|---|---|---|---|\n")
	r.rows("", props, required)
	r.printf("\n")
}

// rows writes one row per property; the properties of an object written
// inline follow as parent.child rows.
func (r *renderer) rows(prefix string, props ordered[*schema], required []string) {
	req := map[string]bool{}
	for _, k := range required {
		req[k] = true
	}
	for _, k := range props.keys {
		p := props.vals[k]
		r.printf("| `%s%s` | %s | %s | %s |\n", prefix, k, r.typeOf(p), yesNo(req[k]), cell(p.Description+constraints(p)))
		if p.Ref == "" && p.Type == "object" && len(p.Properties.keys) > 0 {
			r.rows(prefix+k+".", p.Properties, p.Required)
		}
	}
}

// inline returns the object schema written inline in a body (not a
// reference), possibly as the items of an array, or nil.
func inline(s *schema) *schema {
	if s != nil && s.Type == "array" {
		s = s.Items
	}
	if s == nil || s.Ref != "" || len(s.Properties.keys) == 0 {
		return nil
	}
	return s
}

// link links to a schema's section.
func (r *renderer) link(name string) string {
	if a, ok := r.schemas[name]; ok {
		return fmt.Sprintf("[%s](#%s)", name, a)
	}
	return name
}

// typeOf describes a schema in a few words: a link for a reference,
// "array of X", the type with its format, and enum values.
func (r *renderer) typeOf(s *schema) string {
	if s == nil {
		return "—"
	}
	var t string
	switch {
	case s.Ref != "":
		t = r.link(refName(s.Ref))
	case len(s.AllOf) == 1:
		t = r.typeOf(s.AllOf[0])
	case len(s.AllOf) > 0 || len(s.OneOf) > 0 || len(s.AnyOf) > 0:
		var parts []string
		sep := " and "
		list := s.AllOf
		if len(list) == 0 {
			list, sep = append(s.OneOf, s.AnyOf...), " or "
		}
		for _, p := range list {
			parts = append(parts, r.typeOf(p))
		}
		t = strings.Join(parts, sep)
	case s.Type == "array":
		t = "array of " + r.typeOf(s.Items)
	case s.Type == "object" && s.AdditionalProperties.Kind != 0 && len(s.Properties.keys) == 0:
		var v schema
		if s.AdditionalProperties.Kind == yaml.MappingNode && s.AdditionalProperties.Decode(&v) == nil {
			t = "map of " + r.typeOf(&v)
		} else {
			t = "object"
		}
	case s.Type != "":
		t = s.Type
		if s.Format != "" {
			t += " (" + s.Format + ")"
		}
	default:
		t = "any"
	}
	if len(s.Enum) > 0 {
		var vals []string
		for _, v := range s.Enum {
			if v == nil {
				continue
			}
			vals = append(vals, fmt.Sprintf("`%v`", v))
		}
		t += ": " + strings.Join(vals, ", ")
	}
	if s.Nullable {
		t += ", nullable"
	}
	return t
}

// constraints describes a default value, for the description column.
func constraints(s *schema) string {
	if s == nil || s.Default == nil {
		return ""
	}
	return fmt.Sprintf(" Default `%v`.", s.Default)
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

// cell makes text safe for a table cell.
func cell(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	return strings.ReplaceAll(s, "|", `\|`)
}

func title(tag string) string {
	switch tag {
	case "ai":
		return "AI"
	}
	return titleCase(tag)
}

func titleCase(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// sentence ends s with a full stop.
func sentence(s string) string {
	if strings.HasSuffix(s, ".") {
		return s
	}
	return s + "."
}
