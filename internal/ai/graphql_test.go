package ai

import (
	"encoding/json"
	"strings"
	"testing"
)

const shopSDL = `
type Query { products(page: Int): [Product!]!  product(id: ID!): Product  me: User }
type Mutation { login(email: String!, password: String!): AuthPayload!  addToCart(productId: ID!, qty: Int!): Cart! }
type Product { id: ID! name: String! price: Float! }
type AuthPayload { token: String! user: User! }
type User { id: ID! email: String! }
type Cart { items: [Product!]! }
`

func TestGraphQLFromSDL(t *testing.T) {
	u := &Understanding{}
	digest, err := u.fromGraphQL([]byte(shopSDL), "")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"POST /graphql", "login(email: String!, password: String!): AuthPayload! { token user }", "product(id: ID!): Product { id name price }"} {
		if !strings.Contains(digest, want) {
			t.Errorf("digest lacks %q:\n%s", want, digest)
		}
	}
	if !u.HasSpec || len(u.Endpoints) != 1 || u.Endpoints[0].Key() != "POST /graphql" {
		t.Errorf("endpoints %+v", u.Endpoints)
	}
	var token, id bool
	for _, d := range u.Dependencies {
		if d.Kind == "token" && d.Value == "$.data.login.token" {
			token = true
		}
		if d.Kind == "id" && strings.HasSuffix(d.Consumer, "addToCart") {
			id = true
		}
	}
	if !token || !id {
		t.Errorf("dependencies %+v", u.Dependencies)
	}
}

func TestGraphQLFromIntrospection(t *testing.T) {
	named := func(n string) map[string]any { return map[string]any{"kind": "OBJECT", "name": n} }
	nonNull := func(t map[string]any) map[string]any { return map[string]any{"kind": "NON_NULL", "ofType": t} }
	doc := map[string]any{"data": map[string]any{"__schema": map[string]any{
		"queryType": map[string]any{"name": "Query"},
		"types": []any{
			map[string]any{"kind": "OBJECT", "name": "Query", "fields": []any{
				map[string]any{"name": "order", "args": []any{map[string]any{"name": "id", "type": nonNull(map[string]any{"kind": "SCALAR", "name": "ID"})}}, "type": named("Order")},
			}},
			map[string]any{"kind": "OBJECT", "name": "Order", "fields": []any{
				map[string]any{"name": "id", "args": []any{}, "type": nonNull(map[string]any{"kind": "SCALAR", "name": "ID"})},
			}},
			map[string]any{"kind": "OBJECT", "name": "__Type", "fields": []any{}},
		},
	}}}
	b, _ := json.Marshal(doc)
	s, err := parseGraphQL(b)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Queries) != 1 || s.Queries[0].Type != "Order" || s.Queries[0].Args[0] != "id: ID!" {
		t.Fatalf("queries %+v", s.Queries)
	}
	if _, ok := s.Objects["__Type"]; ok {
		t.Error("introspection types must be skipped")
	}
}

func TestGraphQLInputAccepted(t *testing.T) {
	u, err := Understand(Inputs{GraphQL: []byte(shopSDL), GraphQLPath: "/api/graphql"}, NewRedactor())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(u.Context, "## GraphQL API at POST /api/graphql") {
		t.Errorf("context:\n%s", u.Context)
	}
	if _, err := parseGraphQL([]byte("type {")); err == nil {
		t.Error("bad SDL should fail")
	}
}
