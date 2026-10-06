package cli

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

// fakeModel is an OpenAI-compatible server that replies with the given
// scenarios in turn.
func fakeModel(t *testing.T, replies ...string) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		i := int(n.Add(1)) - 1
		if r.URL.Path != "/v1/chat/completions" || i >= len(replies) {
			http.Error(w, `{"error":{"message":"unexpected call"}}`, 400)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []any{map[string]any{"message": map[string]any{"content": replies[i]}, "finish_reason": "stop"}},
			"usage":   map[string]any{"prompt_tokens": 100, "completion_tokens": 20},
		})
	}))
	t.Cleanup(srv.Close)
	return srv, &n
}

func target(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/items", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{"items":[{"id":3}]}`)) })
	mux.HandleFunc("GET /api/items/{id}", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{"id":3}`)) })
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

const goodScenario = `{"metadata":{"name":"items"},"journeys":[{"name":"browse","weight":3,"steps":[
 {"get":"/api/items","check":{"status":200},"extract":{"itemId":"$.items[0].id"}},
 {"think":"1s"},
 {"get":"/api/items/${itemId}","check":{"status":200}}]}],
 "load":{"mode":"vus","vus":2,"duration":"30s"}}`

const badScenario = `{"metadata":{"name":"items"},"journeys":[{"name":"browse","steps":[
 {"get":"/api/items","check":{"status":201}}]}],"load":{"vus":1,"duration":"10s"}}`

func runGen(t *testing.T, args ...string) (string, error) {
	t.Helper()
	root := NewRoot()
	var out, errOut bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&errOut)
	root.SetArgs(append([]string{"generate"}, args...))
	err := root.Execute()
	return out.String() + errOut.String(), err
}

func TestGenerateWritesAValidatedScenario(t *testing.T) {
	tg := target(t)
	model, calls := fakeModel(t, goodScenario)
	out := filepath.Join(t.TempDir(), "items.yaml")
	text, err := runGen(t, "--describe", "people browse items", "--target", tg.URL,
		"--provider", "openai-compatible", "--base-url", model.URL+"/v1", "--model", "local", "-o", out)
	if err != nil {
		t.Fatalf("%v\n%s", err, text)
	}
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"baseURL: " + tg.URL, "name: browse", "- get: /api/items/${itemId}", "1 of 1 journeys passed"} {
		if !strings.Contains(string(b), want) {
			t.Errorf("scenario lacks %q:\n%s", want, b)
		}
	}
	if !strings.Contains(text, "✓ browse") || !strings.Contains(text, "tokens used: 100 in, 20 out") || calls.Load() != 1 {
		t.Errorf("output:\n%s", text)
	}
}

func TestGenerateRefusesUnvalidatedUnlessAllowed(t *testing.T) {
	tg := target(t)
	model, _ := fakeModel(t, badScenario, badScenario)
	dir := t.TempDir()
	out := filepath.Join(dir, "items.yaml")
	traces := filepath.Join(dir, "traces.json")
	args := []string{"--describe", "browse", "--target", tg.URL, "--provider", "openai-compatible",
		"--base-url", model.URL + "/v1", "--model", "local", "--max-repairs", "1", "-o", out, "--traces", traces}
	text, err := runGen(t, args...)
	if err == nil || ExitCode(err) != ExitUnvalidated {
		t.Fatalf("expected exit %d, got %v\n%s", ExitUnvalidated, err, text)
	}
	if _, statErr := os.Stat(out); statErr == nil {
		t.Fatal("an unvalidated scenario must not be written")
	}
	if !strings.Contains(text, "✗ browse") || !strings.Contains(text, "got 200, expected 201") {
		t.Errorf("summary should show the failing step:\n%s", text)
	}
	tb, _ := os.ReadFile(traces)
	if !strings.Contains(string(tb), `"status": "flagged"`) {
		t.Errorf("traces file: %s", tb)
	}

	model2, _ := fakeModel(t, badScenario)
	args[7] = model2.URL + "/v1"
	text, err = runGen(t, append(args, "--allow-unvalidated", "--max-repairs", "0")...)
	if err != nil {
		t.Fatalf("%v\n%s", err, text)
	}
	b, _ := os.ReadFile(out)
	if !strings.Contains(string(b), "# FLAGGED FOR REVIEW") {
		t.Errorf("flagged journeys must be marked:\n%s", b)
	}
}

func TestGenerateFlagErrors(t *testing.T) {
	if _, err := runGen(t, "--describe", "x", "--target", "http://localhost:1"); err == nil || !strings.Contains(err.Error(), "--out") {
		t.Errorf("missing -o: %v", err)
	}
	if _, err := runGen(t, "--describe", "x", "-o", "x.yaml"); err == nil || !strings.Contains(err.Error(), "--target") {
		t.Errorf("missing target: %v", err)
	}
	t.Setenv("ANTHROPIC_API_KEY", "")
	if _, err := runGen(t, "--describe", "x", "-o", "x.yaml", "--no-dry-run"); err == nil || !strings.Contains(err.Error(), "ANTHROPIC_API_KEY") {
		t.Errorf("missing key: %v", err)
	}
	if _, err := runGen(t, "--describe", "x", "-o", "x.yaml", "--no-dry-run", "--provider", "palm"); err == nil {
		t.Error("unknown provider accepted")
	}
}
