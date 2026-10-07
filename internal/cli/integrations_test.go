package cli

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Ivan825/Stampede/internal/api/gen"
)

func TestIntegrationsCommands(t *testing.T) {
	signedIn(t)
	if out := mustCLI(t, "integrations", "list"); !strings.Contains(out, "No integrations") {
		t.Errorf("empty list: %s", out)
	}
	if out := mustCLI(t, "integrations", "create", "prom", "--kind", "prometheus", "--url", "http://127.0.0.1:9090"); out != "Added prometheus integration prom (http://127.0.0.1:9090).\n" {
		t.Errorf("create: %q", out)
	}
	const tok = "agent-token-0123456789"
	if out, err := runCLIIn(t, tok+"\n", "integrations", "create", "shop-agent", "--kind", "agent", "--url", "http://127.0.0.1:7070", "--token-stdin"); err != nil || strings.Contains(out, tok) {
		t.Errorf("create agent: %v %q", err, out)
	}
	if _, err := runCLI(t, "integrations", "create", "x", "--kind", "prometheus"); err == nil || !strings.Contains(err.Error(), "--kind and --url are required") {
		t.Errorf("create without a URL: %v", err)
	}
	if _, err := runCLI(t, "integrations", "create", "prom", "--kind", "prometheus", "--url", "http://127.0.0.1:9091"); err == nil {
		t.Error("a second integration named prom was added")
	}
	out := mustCLI(t, "integrations", "list")
	jsonOut := mustCLI(t, "integrations", "list", "--json")
	if strings.Contains(out, tok) || strings.Contains(jsonOut, tok) {
		t.Errorf("a token was printed:\n%s\n%s", out, jsonOut)
	}
	for _, want := range []string{"prom", "prometheus", "http://127.0.0.1:9090", "shop-agent", "agent", "token stored"} {
		if !strings.Contains(out, want) {
			t.Errorf("list lacks %q:\n%s", want, out)
		}
	}
	var is []gen.Integration
	cliJSON(t, &is, "integrations", "list")
	if len(is) != 2 {
		t.Errorf("list --json: %+v", is)
	}
	if out := mustCLI(t, "integrations", "delete", "prom"); out != "Deleted integration prom.\n" {
		t.Errorf("delete: %q", out)
	}
	if _, err := runCLI(t, "integrations", "delete", "prom"); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Errorf("delete again: %v", err)
	}
}

func TestNotifyCommands(t *testing.T) {
	signedIn(t)
	var hits atomic.Int32
	var sig atomic.Value
	hook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		sig.Store(r.Header.Get("X-Stampede-Signature"))
		_, _ = io.Copy(io.Discard, r.Body)
		w.WriteHeader(204)
	}))
	t.Cleanup(hook.Close)

	if out := mustCLI(t, "notify", "channels", "list"); !strings.Contains(out, "No channels") {
		t.Errorf("empty list: %s", out)
	}
	if _, err := runCLI(t, "notify", "channels", "create", "ci", "--kind", "webhook", "--url", hook.URL); err == nil || !strings.Contains(err.Error(), "private") {
		t.Errorf("a loopback destination without --allow-private: %v", err)
	}
	out := mustCLI(t, "notify", "channels", "create", "ci", "--kind", "webhook", "--url", hook.URL, "--allow-private", "--event", "run.finished", "--event", "run.killed")
	if !strings.Contains(out, "Added webhook channel ci (http://127.0.0.1") || !strings.Contains(out, "Signing secret, shown once: ") {
		t.Errorf("create:\n%s", out)
	}
	secret := strings.TrimSpace(out[strings.Index(out, "shown once: ")+len("shown once: "):])
	t.Setenv("SLACK_URL", "https://203.0.113.20/services/T000/B000/XXXXSECRET")
	if out := mustCLI(t, "notify", "channels", "create", "team", "--kind", "slack", "--url-env", "SLACK_URL"); strings.Contains(out, "XXXXSECRET") || strings.Contains(out, "Signing secret") {
		t.Errorf("create slack: %q", out)
	}
	if _, err := runCLI(t, "notify", "channels", "create", "x", "--kind", "slack"); err == nil || !strings.Contains(err.Error(), "--url or --url-env") {
		t.Errorf("create without a URL: %v", err)
	}

	out = mustCLI(t, "notify", "channels", "list")
	jsonOut := mustCLI(t, "notify", "channels", "list", "--json")
	for _, o := range []string{out, jsonOut} {
		if strings.Contains(o, secret) || strings.Contains(o, "XXXXSECRET") || strings.Contains(o, hook.URL+"/") {
			t.Errorf("a secret or URL was printed:\n%s", o)
		}
	}
	for _, want := range []string{"ci", "webhook", "run.finished,run.killed", "team", "slack", "https://203.0.113.20"} {
		if !strings.Contains(out, want) {
			t.Errorf("list lacks %q:\n%s", want, out)
		}
	}

	out = mustCLI(t, "notify", "channels", "test", "ci")
	if !strings.HasPrefix(out, "Test to ci: ok (HTTP 204)") || hits.Load() != 1 || sig.Load().(string) == "" {
		t.Errorf("test: %q (%d hits)", out, hits.Load())
	}
	out = mustCLI(t, "notify", "deliveries", "ci")
	if !strings.Contains(out, "WHEN") || !strings.Contains(out, "ok") || !strings.Contains(out, "204") {
		t.Errorf("deliveries:\n%s", out)
	}
	var ds []gen.NotificationDelivery
	cliJSON(t, &ds, "notify", "deliveries", "ci", "--limit", "5")
	if len(ds) != 1 || !ds[0].Ok || ds[0].StatusCode != 204 {
		t.Errorf("deliveries --json: %+v", ds)
	}
	if out := mustCLI(t, "notify", "channels", "list"); !strings.Contains(out, "ok (HTTP 204)") {
		t.Errorf("list shows no last delivery:\n%s", out)
	}

	hook.Close()
	if _, err := runCLI(t, "notify", "channels", "test", "ci"); err == nil || !strings.Contains(err.Error(), "failed") {
		t.Errorf("test of a closed hook: %v", err)
	}
	if out := mustCLI(t, "notify", "channels", "delete", "ci"); out != "Deleted channel ci.\n" {
		t.Errorf("delete: %q", out)
	}
	if _, err := runCLI(t, "notify", "deliveries", "ci"); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Errorf("deliveries of a deleted channel: %v", err)
	}
}
