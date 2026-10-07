package cli

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/Ivan825/Stampede/internal/api/gen"
	"github.com/Ivan825/Stampede/internal/report"
)

// shopProject creates a project "shop" with one target and returns a
// scenario file for it.
func shopProject(t *testing.T) (scenarioFile string, targetURL string) {
	t.Helper()
	c := signedIn(t)
	ctx := context.Background()
	tg := target(t)
	var proj gen.Project
	if err := c.Do(ctx, "POST", "/projects", map[string]string{"name": "Shop"}, &proj); err != nil {
		t.Fatal(err)
	}
	if err := c.Do(ctx, "POST", "/projects/"+proj.Id.String()+"/targets", map[string]string{"name": "local", "baseURL": tg.URL}, nil); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "items.yaml")
	yaml := "metadata: {name: items}\njourneys:\n  - name: list\n    steps:\n      - get: /api/items\n        check: { status: 200 }\nload: {mode: rate, rate: 5/s, duration: 1s}\ntargets: [\"errors < 1%\"]\n"
	if err := os.WriteFile(path, []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	return path, tg.URL
}

func TestRunCluster(t *testing.T) {
	path, base := shopProject(t)
	jsonOut := filepath.Join(t.TempDir(), "report.json")
	out, err := runCLI(t, "run", path, "--cluster", "--project", "shop", "--base-url", base, "--duration", "2s", "--json", jsonOut, "-q")
	if err != nil {
		t.Fatalf("run --cluster: %v\n%s", err, out)
	}
	if !strings.Contains(out, "started; Ctrl-C stops following") || !strings.Contains(out, "list") {
		t.Errorf("output: %s", out)
	}
	rs, err := readReports(context.Background(), []string{jsonOut})
	if err != nil {
		t.Fatal(err)
	}
	if r := rs[0]; r.Verdict != report.VerdictPass || r.Overall.Requests < 5 {
		t.Errorf("report: verdict %s, %d requests", r.Verdict, r.Overall.Requests)
	}

	// report and compare take the run's id, or a prefix of it, as well
	// as files.
	m := regexp.MustCompile(`run ([0-9a-f-]{36}) of items`).FindStringSubmatch(out)
	if m == nil {
		t.Fatalf("no run id in %s", out)
	}
	id := m[1]
	if out, err := runCLI(t, "report", id[:8]); err != nil || !strings.Contains(out, "list") {
		t.Errorf("report <run-id prefix>: %v %s", err, out)
	}
	if out, err := runCLI(t, "compare", id, jsonOut); err != nil || !strings.Contains(out, "A (1 runs) → B (1 runs)") {
		t.Errorf("compare <run-id> <file>: %v %s", err, out)
	}
	if _, err := runCLI(t, "report", "ffffffff"); err == nil || !strings.Contains(err.Error(), "no run starts with") {
		t.Errorf("unknown run: %v", err)
	}

	// start keeps working on the same code path.
	out, err = runCLI(t, "start", "--project", "shop", "--file", path, "--detach")
	if err != nil || len(strings.TrimSpace(out)) != 36 {
		t.Errorf("start --detach: %v %q", err, out)
	}
}

func TestRunClusterFlags(t *testing.T) {
	if _, err := runCLI(t, "run", "x.yaml", "--project", "shop"); err == nil || !strings.Contains(err.Error(), "--project applies only with --cluster") {
		t.Errorf("cluster flag without --cluster: %v", err)
	}
	if _, err := runCLI(t, "run", "x.yaml", "--cluster", "--iterations", "3"); err == nil || !strings.Contains(err.Error(), "--iterations applies only to in-process runs") {
		t.Errorf("local flag with --cluster: %v", err)
	}
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("STAMPEDE_SERVER", "")
	t.Setenv("STAMPEDE_TOKEN", "")
	if _, err := runCLI(t, "run", "x.yaml", "--cluster"); err == nil || !strings.Contains(err.Error(), "not signed in") {
		t.Errorf("not signed in: %v", err)
	}
	if _, err := runCLI(t, "report", "missing.json"); err == nil || !strings.Contains(err.Error(), "missing.json is not a file, and it cannot be looked up as a server run: not signed in") {
		t.Errorf("report of a missing file: %v", err)
	}
}
