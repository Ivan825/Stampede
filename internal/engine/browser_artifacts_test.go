package engine

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// A failed browser step keeps a screenshot, the page's console errors
// and a HAR of its requests, with secrets scrubbed.
func TestBrowserFailureArtifacts(t *testing.T) {
	if _, err := FindChrome(); err != nil {
		t.Skip(err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<!doctype html><title>Shop</title><h1>Shop</h1>
<script>console.error("cart service down, token tok_SECRET99"); fetch("/api/cart");</script>`)
	})
	mux.HandleFunc("GET /api/cart", func(w http.ResponseWriter, r *http.Request) { http.Error(w, "down", 503) })
	srv := httptest.NewServer(mux)
	defer srv.Close()
	out := run(t, fmt.Sprintf(`
metadata: {name: shot}
target: {baseURL: %q}
journeys:
  - name: a
    steps:
      - browser: /
        timeout: 3s
        steps:
          - waitFor: "h1"
          - assert: {selector: "h1", text: "Checkout"}
load: {iterations: 1}`, srv.URL), func(o *Options) { o.Secrets = map[string]string{"T": "tok_SECRET99"} })
	var ex *struct {
		shot    []byte
		console []string
		har     string
	}
	for _, st := range out.total.Steps {
		for _, list := range st.Examples {
			for _, e := range list {
				ex = &struct {
					shot    []byte
					console []string
					har     string
				}{e.Screenshot, e.Console, e.HAR}
			}
		}
	}
	if ex == nil {
		t.Fatalf("no example: %+v", out.total.Steps)
	}
	if !bytes.HasPrefix(ex.shot, []byte{0xFF, 0xD8}) {
		t.Errorf("screenshot is not a JPEG (%d bytes)", len(ex.shot))
	}
	if joined := strings.Join(ex.console, "\n"); !strings.Contains(joined, "cart service down") || strings.Contains(joined, "tok_SECRET99") {
		t.Errorf("console %q", ex.console)
	}
	var har struct {
		Log struct {
			Entries []struct {
				Request  struct{ URL string } `json:"request"`
				Response struct{ Status int } `json:"response"`
			} `json:"entries"`
		} `json:"log"`
	}
	if err := json.Unmarshal([]byte(ex.har), &har); err != nil {
		t.Fatalf("HAR: %v", err)
	}
	found := false
	for _, e := range har.Log.Entries {
		if strings.HasSuffix(e.Request.URL, "/api/cart") && e.Response.Status == 503 {
			found = true
		}
	}
	if !found {
		t.Errorf("HAR entries %+v", har.Log.Entries)
	}
}
