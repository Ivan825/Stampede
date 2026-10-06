package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Ivan825/Stampede/internal/engine"
)

const crawledScenario = `{"metadata":{"name":"items"},"journeys":[{"name":"browse","steps":[
 {"get":"/","check":{"status":200}},
 {"get":"/api/items","check":{"status":200}}]}],
 "load":{"mode":"vus","vus":2,"duration":"30s"}}`

func TestGenerateFromCrawl(t *testing.T) {
	if _, err := engine.FindChrome(); err != nil {
		t.Skip(err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<!doctype html><title>Items</title><div id="n"></div>
<form method="post" action="/api/search"><input name="q"></form>
<script>fetch('/api/items').then(r => r.json()).then(d => { document.getElementById('n').textContent = d.items.length; });</script>`)
	})
	mux.HandleFunc("GET /api/items", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"items":[{"id":3}]}`))
	})
	mux.HandleFunc("GET /api/items/{id}", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{"id":3}`)) })
	site := httptest.NewServer(mux)
	defer site.Close()

	// The model sees the crawled traffic and forms; capture its prompt.
	var prompt string
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		prompt = string(b)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []any{map[string]any{"message": map[string]any{"content": crawledScenario}, "finish_reason": "stop"}},
			"usage":   map[string]any{"prompt_tokens": 100, "completion_tokens": 20},
		})
	}))
	defer model.Close()

	out := filepath.Join(t.TempDir(), "items.yaml")
	text, err := runGen(t, "--crawl", site.URL+"/", "--crawl-pages", "3",
		"--provider", "openai-compatible", "--base-url", model.URL+"/v1", "--model", "local", "-o", out)
	if err != nil {
		t.Fatalf("%v\n%s", err, text)
	}
	if !strings.Contains(text, "crawled 1 pages") {
		t.Errorf("progress: %s", text)
	}
	for _, want := range []string{"GET /api/items", "Forms found while crawling", "/api/search with fields q (text)"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("the model's input lacks %q", want)
		}
	}
	if _, err := os.Stat(out); err != nil {
		t.Errorf("the scenario was not written: %v", err)
	}
}
