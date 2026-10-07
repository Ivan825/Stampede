package cli

import (
	"context"
	"strings"
	"testing"

	"github.com/Ivan825/Stampede/internal/api/gen"
)

func TestSchedulesUpdatePreviewShow(t *testing.T) {
	c := signedIn(t)
	pid := shop(t, c, target(t).URL)
	ctx := context.Background()
	if err := c.Do(ctx, "POST", "/projects/"+pid+"/targets", map[string]string{"name": "other", "baseURL": "http://127.0.0.1:9"}, nil); err != nil {
		t.Fatal(err)
	}
	mustCLI(t, "schedules", "create", "nightly", "--scenario", "items", "--target", "local", "--cron", "0 2 * * *", "--duration", "2s", "-e", "A=1", "-e", "B=2")

	out := mustCLI(t, "schedules", "update", "nightly", "--cron", "30 3 * * *", "--timezone", "Asia/Kolkata", "--target", "other",
		"--shape", "spike", "-e", "B=3", "-e", "C=4", "--unset-env", "A", "--note", "moved", "--workers", "2")
	for _, want := range []string{"Updated schedule nightly.", "Cron:       30 3 * * * (Asia/Kolkata)", "Target:     other", "Overrides:  duration=2s shape=spike", "Env:        B=3 C=4", "Workers:    2", "Note:       moved", "Owner:      owner@acme.test"} {
		if !strings.Contains(out, want) {
			t.Errorf("update lacks %q:\n%s", want, out)
		}
	}
	var s gen.Schedule
	cliJSON(t, &s, "schedules", "update", "nightly", "--clear-overrides", "--vus", "3", "--clear-env", "--enabled=false", "--name", "late")
	if s.Name != "late" || s.Enabled || s.NextRunAt != nil || (s.Env != nil && len(*s.Env) != 0) || s.Overrides == nil || *s.Overrides.Vus != 3 || s.Overrides.Shape != nil {
		t.Errorf("update --json: %+v", s)
	}
	if _, err := runCLI(t, "schedules", "update", "late"); err == nil || !strings.Contains(err.Error(), "nothing to change") {
		t.Errorf("update without flags: %v", err)
	}
	if _, err := runCLI(t, "schedules", "update", "late", "--cron", "99 * * * *"); err == nil || !strings.Contains(err.Error(), "cron") {
		t.Errorf("bad cron: %v", err)
	}
	if _, err := runCLI(t, "schedules", "update", "late", "--spec-url", "http://127.0.0.1:9/openapi.json"); err == nil || !strings.Contains(err.Error(), "specURL applies only to drift schedules") {
		t.Errorf("--spec-url on a run schedule: %v", err)
	}

	out = mustCLI(t, "schedules", "show", "late")
	for _, want := range []string{"late (run schedule, id ", "Scenario:   items", "Overrides:  vus=3", "Next run:   disabled"} {
		if !strings.Contains(out, want) {
			t.Errorf("show lacks %q:\n%s", want, out)
		}
	}
	var ss []gen.Schedule
	cliJSON(t, &ss, "schedules", "list")
	if len(ss) != 1 || ss[0].Name != "late" {
		t.Errorf("list --json: %+v", ss)
	}

	// A drift schedule's spec URL can be changed.
	mustCLI(t, "schedules", "create", "drift", "--kind", "drift", "--scenario", "items", "--cron", "@daily", "--target", "local")
	cliJSON(t, &s, "schedules", "update", "drift", "--spec-url", "http://127.0.0.1:9/openapi.json")
	if s.SpecURL == nil || *s.SpecURL != "http://127.0.0.1:9/openapi.json" {
		t.Errorf("drift spec URL: %+v", s)
	}
	if out := mustCLI(t, "schedules", "show", "drift"); !strings.Contains(out, "Spec URL:   http://127.0.0.1:9/openapi.json") {
		t.Errorf("show drift:\n%s", out)
	}

	out = mustCLI(t, "schedules", "preview", "--cron", "0 6 * * MON", "--timezone", "Europe/London", "--count", "2")
	if !strings.HasPrefix(out, `"0 6 * * MON" in Europe/London fires next at:`) || strings.Count(out, "Mon ") != 2 || !strings.Contains(out, "06:00") {
		t.Errorf("preview:\n%s", out)
	}
	var p gen.SchedulePreview
	cliJSON(t, &p, "schedules", "preview", "--cron", "@hourly")
	if p.Timezone != "UTC" || len(p.Next) != 3 {
		t.Errorf("preview --json: %+v", p)
	}
	if _, err := runCLI(t, "schedules", "preview", "--cron", "bad"); err == nil {
		t.Error("preview of a bad cron worked")
	}
}
