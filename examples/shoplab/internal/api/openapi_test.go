package api

import (
	"net/http"
	"regexp"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/Ivan825/Stampede/examples/shoplab"
)

// TestOpenAPICoversEveryRoute keeps the embedded spec in sync with the
// router: every route+method must be documented and every documented path
// must exist.
func TestOpenAPICoversEveryRoute(t *testing.T) {
	spec := string(shoplab.OpenAPI)
	if !strings.HasPrefix(spec, "openapi: 3.1.0") {
		t.Fatal("spec must be OpenAPI 3.1")
	}
	docs := documentedOps(spec)
	if len(docs) != 18 {
		t.Fatalf("parsed %d documented operations from openapi.yaml, want 18", len(docs))
	}

	router := NewRouter(Deps{}).(chi.Routes)
	routes := map[string]bool{}
	err := chi.Walk(router, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		route = strings.TrimSuffix(route, "/")
		if route == "" {
			route = "/"
		}
		key := strings.ToLower(method) + " " + route
		routes[key] = true
		if !docs[key] {
			t.Errorf("route %s is not documented in openapi.yaml", key)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for op := range docs {
		if !routes[op] {
			t.Errorf("openapi.yaml documents %s but the router has no such route", op)
		}
	}
	for _, op := range []string{"post /api/login", "get /api/products/{id}", "post /api/checkout"} {
		if !docs[op] {
			t.Errorf("expected %s to be documented", op)
		}
	}
}

var (
	pathLine   = regexp.MustCompile(`^  (/\S*):\s*$`)
	methodLine = regexp.MustCompile(`^    (get|post|put|patch|delete):\s*$`)
)

// documentedOps extracts "method /path" pairs from the paths section using
// the file's fixed indentation (no YAML dependency needed).
func documentedOps(spec string) map[string]bool {
	ops := map[string]bool{}
	inPaths := false
	current := ""
	for line := range strings.Lines(spec) {
		line = strings.TrimRight(line, "\n")
		switch {
		case line == "paths:":
			inPaths = true
			continue
		case inPaths && len(line) > 0 && line[0] != ' ' && line[0] != '#':
			inPaths = false
		}
		if !inPaths {
			continue
		}
		if m := pathLine.FindStringSubmatch(line); m != nil {
			current = m[1]
			continue
		}
		if m := methodLine.FindStringSubmatch(line); m != nil && current != "" {
			ops[m[1]+" "+current] = true
		}
	}
	return ops
}
