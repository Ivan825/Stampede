package ai

import (
	"strings"
	"testing"

	"github.com/Ivan825/Stampede/internal/scenario"
)

const covSpec = `openapi: 3.0.3
info: {title: Shop, version: "1"}
servers: [{url: /api}]
paths:
  /products:
    get: {responses: {"200": {description: ok}}}
  /products/{productId}:
    get: {responses: {"200": {description: ok}}}
  /cart:
    post: {responses: {"201": {description: ok}}}
  /orders:
    get: {responses: {"200": {description: ok}}}
`

const covSpecV2 = `openapi: 3.0.3
info: {title: Shop, version: "2"}
servers: [{url: /api}]
paths:
  /products:
    get: {responses: {"200": {description: ok}}}
  /products/{id}:
    get: {responses: {"200": {description: ok}}}
  /basket:
    post: {responses: {"201": {description: ok}}}
  /orders:
    get: {responses: {"200": {description: ok}}}
`

const covScenario = `
metadata: {name: shop}
target: {baseURL: "http://shop.test"}
vars: {next: "/api/orders"}
journeys:
  - name: browse
    steps:
      - get: /api/products?page=2
        extract: {id: "$.items[0].id"}
      - get: /api/products/${id}
  - name: buy
    steps:
      - post: /api/cart
        json: {id: 1}
      - get: /api/health
      - get: ${vars.next}
load: {vus: 1, duration: 1s}`

func understandSpec(t *testing.T, spec string) *Understanding {
	t.Helper()
	u, err := Understand(Inputs{OpenAPI: []byte(spec)}, NewRedactor())
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func TestCoverage(t *testing.T) {
	s, err := scenario.Parse([]byte(covScenario))
	if err != nil {
		t.Fatal(err)
	}
	c := CoverageOf(s, understandSpec(t, covSpec))
	if c.Total != 4 || c.Covered != 3 || c.Templated != 1 {
		t.Fatalf("coverage = %+v", c)
	}
	byKey := map[string][]string{}
	for _, e := range c.Endpoints {
		byKey[e.Key()] = e.Journeys
	}
	if strings.Join(byKey["GET /products/{productId}"], ",") != "browse" || strings.Join(byKey["POST /cart"], ",") != "buy" || len(byKey["GET /orders"]) != 0 {
		t.Errorf("endpoints = %v", byKey)
	}
	if len(c.Unmatched) != 1 || c.Unmatched[0].URL != "/api/health" || c.Unmatched[0].Journey != "buy" {
		t.Errorf("unmatched = %+v", c.Unmatched)
	}
	if p := c.Percent(); p != 75 {
		t.Errorf("percent = %v", p)
	}
}

func TestDiffSpecs(t *testing.T) {
	s, err := scenario.Parse([]byte(covScenario))
	if err != nil {
		t.Fatal(err)
	}
	d := DiffSpecs(s, understandSpec(t, covSpec), understandSpec(t, covSpecV2))
	if len(d.Removed) != 1 || d.Removed[0].Key() != "POST /cart" || len(d.Added) != 1 || d.Added[0].Key() != "POST /basket" {
		t.Fatalf("diff = %+v", d)
	}
	if len(d.Broken) != 1 || len(d.Broken["buy"]) != 1 {
		t.Errorf("broken = %+v", d.Broken)
	}
}
