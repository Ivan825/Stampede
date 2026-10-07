package cli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Ivan825/Stampede/internal/api/gen"
)

const itemsSpec = `openapi: 3.0.3
info: { title: Items, version: "1" }
paths:
  /api/items:
    get: { responses: { "200": { description: ok } } }
  /api/orders:
    get: { responses: { "200": { description: ok } } }
`

const repairedItems = `{"metadata":{"name":"items"},"journeys":[{"name":"list","steps":[{"get":"/api/items","check":{"status":200}}]}],"load":{"vus":1,"duration":"1s"}}`

func TestDriftServerCommands(t *testing.T) {
	c, ai := aiServer(t)
	ctx := context.Background()
	tg := target(t)
	var proj gen.Project
	if err := c.Do(ctx, "POST", "/projects", map[string]string{"name": "Shop"}, &proj); err != nil {
		t.Fatal(err)
	}
	pid := proj.Id.String()
	if err := c.Do(ctx, "POST", "/projects/"+pid+"/targets", map[string]string{"name": "local", "baseURL": tg.URL}, nil); err != nil {
		t.Fatal(err)
	}
	yaml := "metadata: {name: items}\njourneys:\n  - name: list\n    steps:\n      - get: /api/items\n        check: {status: 200}\n  - name: gone\n    steps:\n      - get: /api/gone\n        check: {status: 200}\nload: {vus: 1, duration: 1s}\n"
	if err := c.Do(ctx, "POST", "/projects/"+pid+"/scenarios", map[string]string{"yaml": yaml}, nil); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	spec := filepath.Join(dir, "openapi.yaml")
	file := filepath.Join(dir, "items.yaml")
	for path, data := range map[string]string{spec: itemsSpec, file: "target: {baseURL: \"http://localhost:1\"}\n" + yaml} {
		if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	if out := mustCLI(t, "drift", "results"); !strings.Contains(out, "No drift checks yet") {
		t.Errorf("empty results: %s", out)
	}
	mustCLI(t, "schedules", "create", "api-drift", "--kind", "drift", "--scenario", "items", "--cron", "0 6 * * *")
	if _, err := runCLI(t, "schedules", "run", "api-drift"); ExitCode(err) != ExitDrift {
		t.Fatalf("schedules run: %v", err)
	}
	out := mustCLI(t, "drift", "results", "--schedule", "api-drift")
	for _, want := range []string{"CHECK", "drifted", "api-drift", "items v1", "gone"} {
		if !strings.Contains(out, want) {
			t.Errorf("results lack %q:\n%s", want, out)
		}
	}
	var ds []gen.DriftResult
	cliJSON(t, &ds, "drift", "results", "--project", "shop")
	if len(ds) != 1 || ds[0].Status != gen.DriftDrifted {
		t.Fatalf("results --json: %+v", ds)
	}
	check := ds[0].Id.String()[:8]
	out = mustCLI(t, "drift", "show", check)
	for _, want := range []string{"Drift check " + ds[0].Id.String() + " of items v1 against " + tg.URL + ": drifted", "schedule api-drift", "✓ list passes its dry run", "✗ gone fails its dry run", "stampede drift repair " + check} {
		if !strings.Contains(out, want) {
			t.Errorf("show lacks %q:\n%s", want, out)
		}
	}
	var d gen.DriftResult
	cliJSON(t, &d, "drift", "show", check)
	if d.Journeys == nil || len(*d.Journeys) != 2 || (*d.Journeys)[1].Traces == nil {
		t.Errorf("show --json has no traces: %+v", d)
	}

	// repair starts an AI job and prints its id.
	if _, err := runCLI(t, "drift", "repair", check); err == nil || !strings.Contains(err.Error(), "AI provider") {
		t.Errorf("repair without a provider: %v", err)
	}
	mustCLI(t, "ai", "providers", "set", "default", "--kind", "ollama", "--model", "llama3")
	ai.set(repairedItems)
	out = mustCLI(t, "drift", "repair", check)
	job, _, _ := strings.Cut(out, "\n")
	if len(job) != 36 {
		t.Fatalf("repair: %q", out)
	}
	var j gen.AIJob
	cliJSON(t, &j, "ai", "jobs", "show", job, "--wait")
	if j.Status != gen.AIJobSucceeded || j.Diff == nil || !strings.Contains(*j.Diff, "-  - name: gone") {
		t.Errorf("repair job: %+v", j)
	}
	if out := mustCLI(t, "drift", "show", check); !strings.Contains(out, "Repair job: "+job) {
		t.Errorf("show after repair:\n%s", out)
	}

	// The server checks a saved scenario against a spec: coverage and drift.
	out = mustCLI(t, "coverage", "--scenario", "items", "--from-openapi", spec)
	for _, want := range []string{"items v1: 1 of 2 endpoints covered (50%)", "✓ GET", "/api/items", "list", "✗ GET", "/api/orders", "requests that match no endpoint", "/api/gone"} {
		if !strings.Contains(out, want) {
			t.Errorf("coverage lacks %q:\n%s", want, out)
		}
	}
	var cov gen.ScenarioCoverage
	cliJSON(t, &cov, "coverage", "--scenario", "items", "--project", "shop", "--from-openapi", spec)
	if cov.Covered != 1 || cov.Total != 2 || len(cov.Unmatched) != 1 {
		t.Errorf("coverage --json: %+v", cov)
	}
	// --spec-url has the server fetch the document from the target.
	if _, err := runCLI(t, "coverage", "--scenario", "items", "--spec-url", tg.URL+"/openapi.json"); err == nil || !strings.Contains(err.Error(), "404") {
		t.Errorf("coverage --spec-url: %v", err)
	}
	out, err := runCLI(t, "drift", "--scenario", "items", "--from-openapi", spec, "--target", "local")
	if ExitCode(err) != ExitDrift {
		t.Fatalf("drift --scenario: %v\n%s", err, out)
	}
	for _, want := range []string{"items v1", "✗ gone: GET /api/gone is not an endpoint of the current API", "✓ list passes its dry run", "✗ gone fails its dry run (step GET /api/gone"} {
		if !strings.Contains(out, want) {
			t.Errorf("drift --scenario lacks %q:\n%s", want, out)
		}
	}
	prev := filepath.Join(dir, "v0.yaml")
	if err := os.WriteFile(prev, []byte(strings.Replace(itemsSpec, "/api/orders", "/api/gone", 1)), 0o600); err != nil {
		t.Fatal(err)
	}
	var sd gen.ScenarioDrift
	root := NewRoot()
	var stdout, stderr strings.Builder
	root.SetOut(&stdout)
	root.SetErr(&stderr)
	root.SetArgs([]string{"drift", "--scenario", "items", "--from-openapi", spec, "--previous-openapi", prev, "--json"})
	if err := root.Execute(); ExitCode(err) != ExitDrift {
		t.Fatalf("drift --previous-openapi --json: %v %s", err, stderr.String())
	}
	if err := json.Unmarshal([]byte(stdout.String()), &sd); err != nil || !sd.Drifted || sd.Removed == nil || len(*sd.Removed) != 1 || sd.Broken == nil || (*sd.Broken)[0].Journey != "gone" {
		t.Errorf("drift --json: %v %+v", err, sd)
	}

	// Local files keep working, and the modes do not mix.
	if _, err := runCLI(t, "drift", file, "--from-openapi", spec); ExitCode(err) != ExitDrift {
		t.Errorf("local drift: %v", err)
	}
	if out, err := runCLI(t, "coverage", file, "--from-openapi", spec); err != nil || !strings.Contains(out, "1 of 2 endpoints covered") {
		t.Errorf("local coverage: %v\n%s", err, out)
	}
	for args, msg := range map[string]string{
		"drift --scenario items --from-har x.har": "--from-har applies only to scenario files",
		"drift":                             "give a scenario file, or a scenario saved on the server with --scenario",
		"coverage --project shop " + file:   "--project applies only with --scenario",
		"coverage --scenario items " + file: "not both",
		"coverage --scenario items":         "give the API with --from-openapi",
	} {
		_, err := runCLI(t, strings.Fields(args)...)
		if err == nil || !strings.Contains(err.Error(), msg) {
			t.Errorf("%s: %v", args, err)
		}
	}
}
