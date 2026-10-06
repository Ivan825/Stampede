package cli

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func runRoot(t *testing.T, args ...string) (string, error) {
	t.Helper()
	root := NewRoot()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs(args)
	err := root.Execute()
	return out.String(), err
}

const driftScenario = `
metadata: {name: shop}
target: {baseURL: "http://shop.test"}
journeys:
  - name: browse
    steps:
      - get: /api/products
        check: {status: 200}
  - name: buy
    steps:
      - post: /api/cart
        json: {id: 1}
        check: {status: 201}
load: {vus: 1, duration: 1s}`

const specV1 = `openapi: 3.0.3
info: {title: Shop, version: "1"}
paths:
  /api/products: {get: {responses: {"200": {description: ok}}}}
  /api/cart: {post: {responses: {"201": {description: ok}}}}
`

const specV2 = `openapi: 3.0.3
info: {title: Shop, version: "2"}
paths:
  /api/products: {get: {responses: {"200": {description: ok}}}}
  /api/basket: {post: {responses: {"201": {description: ok}}}}
`

func TestCoverageAndDrift(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	sc, v1, v2 := write("shop.yaml", driftScenario), write("v1.yaml", specV1), write("v2.yaml", specV2)

	out, err := runRoot(t, "coverage", sc, "--from-openapi", v1)
	if err != nil || !strings.Contains(out, "2 of 2 endpoints covered (100%)") {
		t.Fatalf("coverage: %v\n%s", err, out)
	}

	// The new version renamed the cart: the static check finds it, and the
	// dry run against the new server confirms the journey fails.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/products" {
			w.WriteHeader(200)
			return
		}
		w.WriteHeader(404)
	}))
	defer srv.Close()
	out, err = runRoot(t, "drift", sc, "--from-openapi", v2, "--previous-openapi", v1, "--target", srv.URL)
	var ee *exitError
	if !errors.As(err, &ee) || ee.code != ExitDrift {
		t.Fatalf("drift should exit %d: %v\n%s", ExitDrift, err, out)
	}
	for _, want := range []string{
		"1 endpoints added, 1 removed", "- POST /api/cart", "+ POST /api/basket",
		"✗ journey buy calls removed endpoints: POST /api/cart",
		"✗ buy: POST /api/cart is not an endpoint of the current API",
		"✓ browse passes its dry run", "✗ buy fails its dry run",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("drift output lacks %q:\n%s", want, out)
		}
	}

	// Nothing drifted against the old version.
	if out, err := runRoot(t, "drift", sc, "--from-openapi", v1); err != nil {
		t.Errorf("no drift expected: %v\n%s", err, out)
	}
}
