package cli

import (
	"strings"
	"testing"

	"github.com/Ivan825/Stampede/internal/api/gen"
)

func TestTargetsCommands(t *testing.T) {
	signedIn(t)
	tg := target(t)
	mustCLI(t, "projects", "create", "Shop")
	if out := mustCLI(t, "targets", "list"); !strings.Contains(out, "No targets") {
		t.Errorf("empty list: %s", out)
	}
	if out := mustCLI(t, "targets", "create", "local", "--base-url", tg.URL, "--max-rate", "50"); out != "Created target local ("+tg.URL+", private).\n" {
		t.Errorf("create: %q", out)
	}
	if _, err := runCLI(t, "targets", "create", "nourl"); err == nil || !strings.Contains(err.Error(), "--base-url is required") {
		t.Errorf("create without a URL: %v", err)
	}
	if _, err := runCLI(t, "targets", "create", "bad", "--base-url", "ftp://x"); err == nil || !strings.Contains(err.Error(), "baseURL must be") {
		t.Errorf("create with a bad URL: %v", err)
	}
	// A public address (a documentation range, so nothing is contacted)
	// prints how to verify it.
	out := mustCLI(t, "targets", "create", "public", "--base-url", "http://203.0.113.10", "--allow-host", "cdn.example.com")
	if !strings.Contains(out, "Created target public (http://203.0.113.10, unverified).") || !strings.Contains(out, "stampede-verify=") || !strings.Contains(out, "http://203.0.113.10/.well-known/stampede-verify.txt") {
		t.Errorf("create public:\n%s", out)
	}

	out = mustCLI(t, "targets", "list")
	for _, want := range []string{"NAME", "local", tg.URL, "private", "rate ≤ 50/s", "public", "unverified", "cdn.example.com"} {
		if !strings.Contains(out, want) {
			t.Errorf("list lacks %q:\n%s", want, out)
		}
	}
	var ts []gen.Target
	cliJSON(t, &ts, "targets", "list")
	if len(ts) != 2 {
		t.Fatalf("list --json: %+v", ts)
	}
	out = mustCLI(t, "targets", "show", "http://203.0.113.10")
	for _, want := range []string{"public  http://203.0.113.10", "Ownership:    unverified", "Also allows:  cdn.example.com", "Caps:         none", "stampede-verify="} {
		if !strings.Contains(out, want) {
			t.Errorf("show lacks %q:\n%s", want, out)
		}
	}

	if out := mustCLI(t, "targets", "update", "public", "--name", "edge", "--allow-host", "", "--max-vus", "10"); out != "Updated target edge (http://203.0.113.10, unverified; caps VUs ≤ 10).\n" {
		t.Errorf("update: %q", out)
	}
	var tgt gen.Target
	cliJSON(t, &tgt, "targets", "update", "local", "--max-rate", "0", "--max-duration", "10m")
	if tgt.Caps.MaxRate != nil || *tgt.Caps.MaxDurationSeconds != 600 || tgt.BaseURL != tg.URL {
		t.Errorf("update --json: %+v", tgt)
	}
	cliJSON(t, &tgt, "targets", "show", "edge")
	if tgt.AllowHosts != nil && len(*tgt.AllowHosts) != 0 {
		t.Errorf("allowed hosts not emptied: %v", *tgt.AllowHosts)
	}
	if _, err := runCLI(t, "targets", "update", "edge"); err == nil || !strings.Contains(err.Error(), "nothing to change") {
		t.Errorf("update without flags: %v", err)
	}

	// verify: a private target needs none; stampede target verify still
	// works, and still checks a URL that is not a target locally.
	if out := mustCLI(t, "targets", "verify", "local"); out != "local is a private address; no verification is needed.\n" {
		t.Errorf("verify: %q", out)
	}
	if out := mustCLI(t, "target", "verify", tg.URL); out != "local is a private address; no verification is needed.\n" {
		t.Errorf("target verify <url>: %q", out)
	}
	if out := mustCLI(t, "target", "verify", "http://127.0.0.2:9"); !strings.Contains(out, "127.0.0.2 is a private address") || !strings.Contains(out, "not a target on the server") {
		t.Errorf("target verify of another URL: %q", out)
	}
	if out := mustCLI(t, "target", "verify", tg.URL, "--local"); !strings.HasSuffix(out, "127.0.0.1 is a private address; no verification is needed.\n") {
		t.Errorf("verify --local: %q", out)
	}

	if out := mustCLI(t, "targets", "delete", "edge"); out != "Deleted target edge.\n" {
		t.Errorf("delete: %q", out)
	}
	if _, err := runCLI(t, "targets", "delete", "edge"); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Errorf("delete again: %v", err)
	}
}

func TestTargetVerifyWithoutServer(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("STAMPEDE_SERVER", "")
	t.Setenv("STAMPEDE_TOKEN", "")
	if out := mustCLI(t, "target", "verify", "http://localhost:8090"); out != "localhost is a private address; no verification is needed.\n" {
		t.Errorf("verify: %q", out)
	}
	if _, err := runCLI(t, "targets", "verify", "staging"); err == nil || !strings.Contains(err.Error(), "not signed in") {
		t.Errorf("verify a name without a server: %v", err)
	}
}
