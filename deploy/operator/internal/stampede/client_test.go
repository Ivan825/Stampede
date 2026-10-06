package stampede_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/Ivan825/Stampede/deploy/operator/internal/stampede"
	"github.com/Ivan825/Stampede/deploy/operator/internal/stampede/stampedetest"
)

const scenarioV1 = `apiVersion: stampede.dev/v1
kind: Scenario
metadata:
  name: smoke
journeys:
  - name: home
    steps:
      - get: /
`

func TestResolveCreatesThenReuses(t *testing.T) {
	ctx := context.Background()
	f := stampedetest.New("stp_test")
	defer f.Close()
	c := stampede.New(f.URL, "stp_test")

	p, err := c.ResolveProject(ctx, "Checkout")
	if err != nil {
		t.Fatal(err)
	}
	for _, ref := range []string{p.ID, "checkout", "CHECKOUT"} {
		again, err := c.ResolveProject(ctx, ref)
		if err != nil || again.ID != p.ID {
			t.Fatalf("ResolveProject(%q) = %v, %v; want %s", ref, again, err, p.ID)
		}
	}

	tg, err := c.ResolveTarget(ctx, p.ID, "http://shop.internal:8090/")
	if err != nil {
		t.Fatal(err)
	}
	if tg.Name != "shop.internal:8090" || tg.BaseURL != "http://shop.internal:8090" {
		t.Fatalf("target = %+v", tg)
	}
	tg2, err := c.ResolveTarget(ctx, p.ID, "http://shop.internal:8090")
	if err != nil || tg2.ID != tg.ID {
		t.Fatalf("target not reused: %v %v", tg2, err)
	}
	if _, err := c.ResolveTarget(ctx, p.ID, "not a url"); err == nil {
		t.Fatal("want an error for a relative target URL")
	}
}

func TestResolveScenarioInlineVersions(t *testing.T) {
	ctx := context.Background()
	f := stampedetest.New("stp_test")
	defer f.Close()
	c := stampede.New(f.URL, "stp_test")
	p, _ := c.ResolveProject(ctx, "p")

	id, v, err := c.ResolveScenario(ctx, p.ID, "", 0, scenarioV1, "m")
	if err != nil || v != 1 {
		t.Fatalf("create: %s v%d %v", id, v, err)
	}
	id2, v2, err := c.ResolveScenario(ctx, p.ID, "", 0, scenarioV1, "m")
	if err != nil || id2 != id || v2 != 1 {
		t.Fatalf("unchanged inline must reuse v1: %s v%d %v", id2, v2, err)
	}
	changed := scenarioV1 + "      - get: /about\n"
	id3, v3, err := c.ResolveScenario(ctx, p.ID, "", 0, changed, "m")
	if err != nil || id3 != id || v3 != 2 {
		t.Fatalf("changed inline must add v2: %s v%d %v", id3, v3, err)
	}

	byName, ver, err := c.ResolveScenario(ctx, p.ID, "smoke", 0, "", "")
	if err != nil || byName != id || ver != 2 {
		t.Fatalf("by name: %s v%d %v", byName, ver, err)
	}
	_, ver, _ = c.ResolveScenario(ctx, p.ID, id, 1, "", "")
	if ver != 1 {
		t.Fatalf("pinned version = %d, want 1", ver)
	}
	_, _, err = c.ResolveScenario(ctx, p.ID, "missing", 0, "", "")
	if !stampede.IsStatus(err, http.StatusNotFound) || !stampede.Permanent(err) {
		t.Fatalf("missing scenario: %v", err)
	}
	if _, _, err := c.ResolveScenario(ctx, p.ID, "", 0, "journeys: []", ""); err == nil {
		t.Fatal("want an error for inline YAML without metadata.name")
	}
}

func TestRunLifecycleAndErrors(t *testing.T) {
	ctx := context.Background()
	f := stampedetest.New("stp_test")
	defer f.Close()
	c := stampede.New(f.URL, "stp_test")
	p, _ := c.ResolveProject(ctx, "p")

	run, err := c.CreateRun(ctx, p.ID, stampede.RunCreate{ScenarioID: "s", TargetID: "t", Workers: 2, Note: "x [k8s:abc]"})
	if err != nil {
		t.Fatal(err)
	}
	found, err := c.FindRunByNote(ctx, p.ID, "[k8s:abc]")
	if err != nil || found == nil || found.ID != run.ID {
		t.Fatalf("FindRunByNote = %v, %v", found, err)
	}
	if none, _ := c.FindRunByNote(ctx, p.ID, "[k8s:zzz]"); none != nil {
		t.Fatalf("unexpected match %v", none)
	}
	f.FinishRun(run.ID, "completed", "pass")
	got, err := c.GetRun(ctx, run.ID)
	if err != nil || !got.Terminal() || *got.Verdict != "pass" {
		t.Fatalf("GetRun = %+v, %v", got, err)
	}

	f.SetWorkers(3)
	if n, err := c.ConnectedWorkers(ctx); err != nil || n != 3 {
		t.Fatalf("ConnectedWorkers = %d, %v", n, err)
	}

	bad := stampede.New(f.URL, "wrong")
	_, err = bad.ListProjects(ctx)
	if !stampede.IsStatus(err, http.StatusUnauthorized) || !stampede.Permanent(err) {
		t.Fatalf("bad token: %v", err)
	}
	if err.Error() != "stampede API 401 unauthorized: bad token" {
		t.Fatalf("error text: %q", err.Error())
	}
}
