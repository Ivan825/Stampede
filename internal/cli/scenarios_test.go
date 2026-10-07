package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Ivan825/Stampede/internal/api/gen"
)

func TestScenariosCommands(t *testing.T) {
	c := signedIn(t)
	shop(t, c, target(t).URL)
	dir := t.TempDir()
	v2 := "metadata: {name: items, tags: [smoke]}\njourneys:\n  - name: list\n    steps:\n      - get: /api/items\nload: {vus: 2, duration: 30s}\n"
	file := filepath.Join(dir, "items.yaml")
	if err := os.WriteFile(file, []byte(v2), 0o600); err != nil {
		t.Fatal(err)
	}
	mustCLI(t, "push", file, "-m", "two users")

	out := mustCLI(t, "scenarios", "list")
	for _, want := range []string{"NAME", "items", "v2", "smoke", "constant-vus, peak 2 VUs, 30s, 1 journeys, 1 steps"} {
		if !strings.Contains(out, want) {
			t.Errorf("list lacks %q:\n%s", want, out)
		}
	}
	var ss []gen.Scenario
	cliJSON(t, &ss, "scenarios", "list", "--tag", "smoke")
	if len(ss) != 1 || ss[0].LatestVersion.Version != 2 {
		t.Fatalf("list --tag --json: %+v", ss)
	}
	cliJSON(t, &ss, "scenarios", "list", "--tag", "nightly")
	if len(ss) != 0 {
		t.Errorf("list --tag nightly: %+v", ss)
	}

	out = mustCLI(t, "scenarios", "show", "items")
	for _, want := range []string{"items (id ", "Tags:     smoke", "Latest:   v2 by owner@acme.test", ": two users", "Load:     constant-vus"} {
		if !strings.Contains(out, want) {
			t.Errorf("show lacks %q:\n%s", want, out)
		}
	}
	if out := mustCLI(t, "scenarios", "show", "items", "--yaml"); out != v2 {
		t.Errorf("show --yaml: %q", out)
	}
	if out := mustCLI(t, "scenarios", "show", ss0(t), "--version", "1"); !strings.Contains(out, "rate: 5/s") {
		t.Errorf("show --version 1: %q", out)
	}
	var v gen.ScenarioVersion
	cliJSON(t, &v, "scenarios", "show", "items", "--version", "2")
	if v.Version != 2 || v.Yaml != v2 {
		t.Errorf("show --version --json: %+v", v)
	}
	if _, err := runCLI(t, "scenarios", "show", "items", "--version", "9"); err == nil {
		t.Error("a missing version was shown")
	}

	out = mustCLI(t, "scenarios", "versions", "items")
	if !strings.Contains(out, "v2") || !strings.Contains(out, "two users") || !strings.Contains(out, "v1") || strings.Index(out, "v2") > strings.Index(out, "v1 ") {
		t.Errorf("versions:\n%s", out)
	}
	var vs []gen.ScenarioVersion
	cliJSON(t, &vs, "scenarios", "versions", "items")
	if len(vs) != 2 {
		t.Errorf("versions --json: %+v", vs)
	}

	bad := filepath.Join(dir, "bad.yaml")
	if err := os.WriteFile(bad, []byte("metadata: {name: x}\njourneys: []\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := runCLI(t, "scenarios", "validate", file, bad)
	if err == nil || !strings.Contains(err.Error(), "1 of 2 scenarios are invalid") {
		t.Errorf("validate: %v", err)
	}
	if !strings.Contains(out, "✓ "+file+": constant-vus") || !strings.Contains(out, "✗ "+bad) {
		t.Errorf("validate output:\n%s", out)
	}
	var res []struct {
		File  string
		Valid bool
	}
	cliJSON(t, &res, "scenarios", "validate", file)
	if len(res) != 1 || !res[0].Valid {
		t.Errorf("validate --json: %+v", res)
	}

	if out := mustCLI(t, "scenarios", "delete", "items"); out != "Deleted scenario items.\n" {
		t.Errorf("delete: %q", out)
	}
	if _, err := runCLI(t, "scenarios", "show", "items"); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Errorf("show deleted: %v", err)
	}
}

// ss0 returns the first eight characters of the only scenario's id.
func ss0(t *testing.T) string {
	t.Helper()
	var ss []gen.Scenario
	cliJSON(t, &ss, "scenarios", "list")
	return ss[0].Id.String()[:8]
}
