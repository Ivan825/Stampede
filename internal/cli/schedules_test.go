package cli

import (
	"context"
	"strings"
	"testing"

	"github.com/Ivan825/Stampede/internal/api/gen"
)

func TestSchedulesCommands(t *testing.T) {
	c := signedIn(t)
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
	yaml := "metadata: {name: items}\njourneys:\n  - name: list\n    steps:\n      - get: /api/items\nload: {mode: rate, rate: 5/s, duration: 1s}\n"
	if err := c.Do(ctx, "POST", "/projects/"+pid+"/scenarios", map[string]string{"yaml": yaml}, nil); err != nil {
		t.Fatal(err)
	}

	out, err := runCLI(t, "schedules", "list", "--project", "shop")
	if err != nil || !strings.Contains(out, "No schedules") {
		t.Fatalf("empty list: %v %s", err, out)
	}
	out, err = runCLI(t, "schedules", "create", "nightly", "--project", "shop", "--scenario", "items",
		"--cron", "0 2 * * *", "--timezone", "Europe/London", "--duration", "2s", "-e", "STAGE=ci", "--note", "regression")
	if err != nil || !strings.Contains(out, "Created schedule nightly (0 2 * * *, Europe/London)") || !strings.Contains(out, "Next run:") {
		t.Fatalf("create: %v %s", err, out)
	}
	var ss []gen.Schedule
	if err := c.Do(ctx, "GET", "/projects/"+pid+"/schedules", nil, &ss); err != nil || len(ss) != 1 {
		t.Fatalf("schedules: %v %v", ss, err)
	}
	if s := ss[0]; *s.Overrides.Duration != "2s" || (*s.Env)["STAGE"] != "ci" || *s.Note != "regression" {
		t.Errorf("saved: %+v", s)
	}
	if _, err := runCLI(t, "schedules", "create", "bad", "--project", "shop", "--scenario", "items", "--cron", "61 * * * *"); err == nil || !strings.Contains(err.Error(), "cron") {
		t.Errorf("bad cron: %v", err)
	}
	if _, err := runCLI(t, "schedules", "create", "nocron", "--project", "shop", "--scenario", "items"); err == nil || !strings.Contains(err.Error(), "--cron") {
		t.Errorf("missing cron: %v", err)
	}

	out, err = runCLI(t, "schedules", "list", "--project", "shop")
	if err != nil || !strings.Contains(out, "nightly") || !strings.Contains(out, "Europe/London") || !strings.Contains(out, "items") {
		t.Errorf("list: %v %s", err, out)
	}
	if out, err = runCLI(t, "schedules", "disable", "nightly", "--project", "shop"); err != nil || out != "Disabled nightly.\n" {
		t.Errorf("disable: %v %q", err, out)
	}
	if out, _ = runCLI(t, "schedules", "list", "--project", "shop"); !strings.Contains(out, "disabled") {
		t.Errorf("list after disable: %s", out)
	}
	if out, err = runCLI(t, "schedules", "enable", "NIGHTLY", "--project", "shop"); err != nil || !strings.HasPrefix(out, "Enabled nightly. Next run:") {
		t.Errorf("enable: %v %q", err, out)
	}

	out, err = runCLI(t, "schedules", "run", "nightly", "--project", "shop")
	if err != nil || len(strings.TrimSpace(out)) != 36 {
		t.Fatalf("run: %v %q", err, out)
	}
	var run gen.Run
	if err := c.Do(ctx, "GET", "/runs/"+strings.TrimSpace(out), nil, &run); err != nil || *run.Note != "scheduled: nightly" {
		t.Errorf("run: %+v %v", run, err)
	}
	if _, err := runCLI(t, "schedules", "run", "nightly", "--project", "shop"); err == nil || !strings.Contains(err.Error(), "still active") {
		t.Errorf("second run while active: %v", err)
	}
	out, _ = runCLI(t, "schedules", "list", "--project", "shop")
	shown := false
	for _, st := range []string{"scheduling", "starting", "running", "stopping", "analyzing", "completed"} {
		shown = shown || strings.Contains(out, st)
	}
	if !shown {
		t.Errorf("list shows no last run: %s", out)
	}

	if _, err := runCLI(t, "schedules", "delete", "nope", "--project", "shop"); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Errorf("unknown: %v", err)
	}
	if out, err = runCLI(t, "schedules", "delete", "nightly", "--project", "shop"); err != nil || out != "Deleted nightly.\n" {
		t.Errorf("delete: %v %q", err, out)
	}
}
