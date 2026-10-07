package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeScenario(t *testing.T, yaml string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "s.yaml")
	if err := os.WriteFile(path, []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestValidateDryRun(t *testing.T) {
	srv := target(t)
	path := writeScenario(t, `metadata: {name: items}
target: {baseURL: "${env.TARGET_URL}"}
journeys:
  - name: browse
    steps:
      - get: /api/items
        check: { status: 200 }
        extract: { itemId: "$.items[0].id" }
      - get: /api/items/${itemId}
        check: { status: 200 }
  - name: wrong status
    steps:
      - get: /api/items
        check: { status: 201 }
load: {mode: rate, rate: 100/s, duration: 10m}
`)

	// Statically valid either way; only the dry run finds the bad check.
	if out, err := runCLI(t, "validate", path); err != nil || !strings.Contains(out, "✓ "+path) {
		t.Fatalf("static: %v %s", err, out)
	}
	out, err := runCLI(t, "validate", path, "--dry-run", "-e", "TARGET_URL="+srv.URL)
	if err == nil || err.Error() != "1 journey(s) failed their dry run" {
		t.Errorf("dry run error: %v", err)
	}
	if !strings.Contains(out, "  ✓ browse\n") || !strings.Contains(out, "  ✗ wrong status: ") {
		t.Errorf("dry run output: %s", out)
	}

	// --base-url sets the target as for stampede run.
	ok := writeScenario(t, "metadata: {name: ok}\ntarget: {baseURL: http://example.invalid}\njourneys:\n  - name: browse\n    steps:\n      - get: /api/items\n        check: { status: 200 }\nload: {iterations: 5}\n")
	if out, err := runCLI(t, "validate", ok, "--dry-run", "--base-url", srv.URL); err != nil || !strings.Contains(out, "  ✓ browse\n") {
		t.Errorf("--base-url: %v %s", err, out)
	}
	// Without a target there is nothing to run against.
	if _, err := runCLI(t, "validate", path, "--dry-run"); err == nil || !strings.Contains(err.Error(), "1 of 1 scenarios are invalid") {
		t.Errorf("no target: %v", err)
	}
	if _, err := runCLI(t, "validate", path, "-e", "TARGET_URL=x"); err == nil || !strings.Contains(err.Error(), "--env applies only with --dry-run") {
		t.Errorf("-e without --dry-run: %v", err)
	}
}

func TestRunEnvSecrets(t *testing.T) {
	t.Setenv("TOKEN", "plain")
	t.Setenv("STAMPEDE_SECRET_TOKEN", "secret")
	env, secrets, err := runEnv([]string{"A=1"})
	if err != nil || env["A"] != "1" || secrets["TOKEN"] != "secret" || secrets["A"] != "1" {
		t.Errorf("env %v secrets[TOKEN]=%q err %v", env["A"], secrets["TOKEN"], err)
	}
	if _, _, err := runEnv([]string{"A"}); err == nil {
		t.Error("KEY without =VALUE accepted")
	}
}
