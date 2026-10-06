package ai

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/vektah/gqlparser/v2"
	"github.com/vektah/gqlparser/v2/ast"
)

// IntrospectionQuery asks a GraphQL server for its schema.
const IntrospectionQuery = `query IntrospectionQuery { __schema { queryType { name } mutationType { name } types { kind name fields(includeDeprecated: false) { name args { name type { ...T } } type { ...T } } } } }
fragment T on __Type { kind name ofType { kind name ofType { kind name ofType { kind name } } } }`

// gqlField is a field with its arguments and type, in SDL notation.
type gqlField struct {
	Name string
	Args []string // "id: ID!"
	Type string   // "Product!"
	Base string   // "Product"
}

// gqlSchema is the part of a GraphQL schema the generator needs.
type gqlSchema struct {
	Queries   []gqlField
	Mutations []gqlField
	// Objects maps a type name to its fields (name -> type).
	Objects map[string][]gqlField
}

// parseGraphQL reads either an introspection result (JSON, with or
// without the {"data": ...} envelope) or SDL text.
func parseGraphQL(src []byte) (*gqlSchema, error) {
	trimmed := strings.TrimSpace(string(src))
	if strings.HasPrefix(trimmed, "{") {
		return fromIntrospection(src)
	}
	return fromSDL(trimmed)
}

type introType struct {
	Kind   string     `json:"kind"`
	Name   string     `json:"name"`
	OfType *introType `json:"ofType"`
}

func (t *introType) sdl() (string, string) {
	if t == nil {
		return "", ""
	}
	switch t.Kind {
	case "NON_NULL":
		s, b := t.OfType.sdl()
		return s + "!", b
	case "LIST":
		s, b := t.OfType.sdl()
		return "[" + s + "]", b
	default:
		return t.Name, t.Name
	}
}

func fromIntrospection(src []byte) (*gqlSchema, error) {
	var doc struct {
		Data   json.RawMessage `json:"data"`
		Schema json.RawMessage `json:"__schema"`
	}
	if err := json.Unmarshal(src, &doc); err != nil {
		return nil, fmt.Errorf("GraphQL introspection JSON: %w", err)
	}
	raw := doc.Schema
	if len(doc.Data) > 0 {
		var d struct {
			Schema json.RawMessage `json:"__schema"`
		}
		if err := json.Unmarshal(doc.Data, &d); err != nil {
			return nil, err
		}
		raw = d.Schema
	}
	if len(raw) == 0 {
		return nil, errors.New("GraphQL introspection JSON has no __schema")
	}
	var s struct {
		QueryType    *struct{ Name string } `json:"queryType"`
		MutationType *struct{ Name string } `json:"mutationType"`
		Types        []struct {
			Kind   string `json:"kind"`
			Name   string `json:"name"`
			Fields []struct {
				Name string `json:"name"`
				Args []struct {
					Name string     `json:"name"`
					Type *introType `json:"type"`
				} `json:"args"`
				Type *introType `json:"type"`
			} `json:"fields"`
		} `json:"types"`
	}
	if err := json.Unmarshal(raw, &s); err != nil {
		return nil, fmt.Errorf("GraphQL __schema: %w", err)
	}
	out := &gqlSchema{Objects: map[string][]gqlField{}}
	qName, mName := "Query", "Mutation"
	if s.QueryType != nil {
		qName = s.QueryType.Name
	}
	if s.MutationType != nil {
		mName = s.MutationType.Name
	}
	for _, t := range s.Types {
		if t.Kind != "OBJECT" || strings.HasPrefix(t.Name, "__") {
			continue
		}
		var fields []gqlField
		for _, f := range t.Fields {
			gf := gqlField{Name: f.Name}
			gf.Type, gf.Base = f.Type.sdl()
			for _, a := range f.Args {
				ts, _ := a.Type.sdl()
				gf.Args = append(gf.Args, a.Name+": "+ts)
			}
			fields = append(fields, gf)
		}
		switch t.Name {
		case qName:
			out.Queries = fields
		case mName:
			out.Mutations = fields
		default:
			out.Objects[t.Name] = fields
		}
	}
	return out, nil
}

func fromSDL(src string) (*gqlSchema, error) {
	schema, err := gqlparser.LoadSchema(&ast.Source{Name: "schema.graphql", Input: src})
	if err != nil {
		return nil, fmt.Errorf("GraphQL SDL: %s", err.Error())
	}
	conv := func(d *ast.FieldDefinition) gqlField {
		f := gqlField{Name: d.Name, Type: d.Type.String(), Base: d.Type.Name()}
		for _, a := range d.Arguments {
			f.Args = append(f.Args, a.Name+": "+a.Type.String())
		}
		return f
	}
	out := &gqlSchema{Objects: map[string][]gqlField{}}
	for name, def := range schema.Types {
		if def.Kind != ast.Object || strings.HasPrefix(name, "__") {
			continue
		}
		var fields []gqlField
		for _, fd := range def.Fields {
			if strings.HasPrefix(fd.Name, "__") {
				continue
			}
			fields = append(fields, conv(fd))
		}
		switch {
		case schema.Query != nil && name == schema.Query.Name:
			out.Queries = fields
		case schema.Mutation != nil && name == schema.Mutation.Name:
			out.Mutations = fields
		default:
			out.Objects[name] = fields
		}
	}
	return out, nil
}

// fromGraphQL adds a GraphQL API to the understanding: one endpoint (POST
// path), the operations for the model, and dependencies such as a login
// mutation's token feeding authenticated operations.
func (u *Understanding) fromGraphQL(src []byte, path string) (string, error) {
	s, err := parseGraphQL(src)
	if err != nil {
		return "", err
	}
	if len(s.Queries) == 0 && len(s.Mutations) == 0 {
		return "", errors.New("the GraphQL schema has no queries or mutations")
	}
	if path == "" {
		path = "/graphql"
	}
	u.HasSpec = true
	u.addEndpoint(Endpoint{Method: "POST", Path: path, Summary: "GraphQL API", Source: "graphql"})

	var b strings.Builder
	fmt.Fprintf(&b, "## GraphQL API at POST %s\n\n", path)
	b.WriteString("Use `graphql:` steps (with `query:`, `variables:` and `operationName:`) for these operations; select only the fields a journey needs.\n\n")
	sig := func(f gqlField) string {
		args := ""
		if len(f.Args) > 0 {
			args = "(" + strings.Join(f.Args, ", ") + ")"
		}
		ret := f.Type
		if fields := s.Objects[f.Base]; len(fields) > 0 {
			var names []string
			for _, of := range fields {
				names = append(names, of.Name)
				if len(names) == 8 {
					names = append(names, "…")
					break
				}
			}
			ret += " { " + strings.Join(names, " ") + " }"
		}
		return "- " + f.Name + args + ": " + ret
	}
	for _, sec := range []struct {
		title  string
		fields []gqlField
	}{{"Queries", s.Queries}, {"Mutations", s.Mutations}} {
		if len(sec.fields) == 0 {
			continue
		}
		fmt.Fprintf(&b, "%s:\n", sec.title)
		sorted := append([]gqlField(nil), sec.fields...)
		sort.Slice(sorted, func(i, j int) bool { return sorted[i].Name < sorted[j].Name })
		for _, f := range sorted {
			b.WriteString(sig(f) + "\n")
		}
		b.WriteString("\n")
	}

	// A mutation or query returning an object with a token field is how
	// clients sign in; its token feeds every other operation.
	op := "POST " + path
	for _, f := range append(append([]gqlField(nil), s.Mutations...), s.Queries...) {
		for _, of := range s.Objects[f.Base] {
			name := strings.ToLower(of.Name)
			if name == "token" || name == "accesstoken" || name == "jwt" {
				u.addDependency(Dependency{
					Producer: op + " " + f.Name, Value: "$.data." + f.Name + "." + of.Name,
					Consumer: op + " (signed-in operations)", Via: "Authorization: Bearer", Kind: "token",
				})
			}
		}
	}
	// Operations returning an object with an id feed operations that take
	// an id argument of that object (productId, id on product(...)).
	for _, f := range s.Queries {
		base := s.Objects[f.Base]
		if len(base) == 0 || !hasField(base, "id") {
			continue
		}
		want := strings.ToLower(f.Base) + "id"
		for _, g := range append(append([]gqlField(nil), s.Queries...), s.Mutations...) {
			for _, a := range g.Args {
				an := strings.ToLower(strings.SplitN(a, ":", 2)[0])
				if an == want || (an == "id" && strings.EqualFold(g.Base, f.Base) && g.Name != f.Name) {
					u.addDependency(Dependency{
						Producer: op + " " + f.Name, Value: "$.data." + f.Name + "..id",
						Consumer: op + " " + g.Name, Via: "variable " + strings.SplitN(a, ":", 2)[0], Kind: "id",
					})
				}
			}
		}
	}
	return b.String(), nil
}

func hasField(fs []gqlField, name string) bool {
	for _, f := range fs {
		if f.Name == name {
			return true
		}
	}
	return false
}
