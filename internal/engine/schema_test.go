package engine

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
)

func TestSchemaCheck(t *testing.T) {
	var good atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/good":
			good.Add(1)
			fmt.Fprint(w, `{"id": 7, "name": "shoe", "tags": ["a"]}`)
		case "/bad":
			fmt.Fprint(w, `{"id": "7", "name": "shoe"}`)
		default:
			fmt.Fprint(w, `not json`)
		}
	}))
	defer srv.Close()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "item.yaml"), []byte(`
type: object
required: [id, name]
properties:
  id: {type: integer}
  name: {type: string}
`), 0o600); err != nil {
		t.Fatal(err)
	}
	out := run(t, fmt.Sprintf(`
metadata: {name: schema}
target: {baseURL: %q}
journeys:
  - name: inline
    steps:
      - get: /good
        check: {schema: {type: object, required: [id], properties: {id: {type: integer}, tags: {type: array, items: {type: string}}}}}
  - name: file
    steps:
      - get: /bad
        check: {schema: %q}
  - name: notjson
    steps:
      - get: /text
        check: {schema: {type: object}}
load: {iterations: 30, vus: 1}`, srv.URL, filepath.Join(dir, "item.yaml")), nil)
	// Only the valid body passes; a wrong type and a non-JSON body fail.
	got := out.total.Totals()
	if bad := got.Requests - uint64(good.Load()); got.Requests != 30 || got.Failed != bad || got.Errors["check schema"] != bad || bad == 0 {
		t.Fatalf("requests %d (good %d) failed %d errors %v", got.Requests, good.Load(), got.Failed, got.Errors)
	}
}
