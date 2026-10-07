package cli

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Ivan825/Stampede/internal/api/gen"
)

func TestDriftScheduleCommands(t *testing.T) {
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
	yaml := "metadata: {name: items}\njourneys:\n  - name: list\n    steps:\n      - get: /api/items\n        check: {status: 200}\n  - name: gone\n    steps:\n      - get: /api/gone\n        check: {status: 200}\nload: {vus: 1, duration: 1s}\n"
	if err := c.Do(ctx, "POST", "/projects/"+pid+"/scenarios", map[string]string{"yaml": yaml}, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := runCLI(t, "schedules", "create", "api-drift", "--project", "shop", "--scenario", "items", "--kind", "drift", "--cron", "0 6 * * *"); err != nil {
		t.Fatalf("create: %v", err)
	}
	out, err := runCLI(t, "schedules", "list", "--project", "shop")
	if err != nil || !strings.Contains(out, "api-drift  drift") {
		t.Errorf("list: %v\n%s", err, out)
	}
	// Running it now checks for drift: /api/gone is a 404.
	out, err = runCLI(t, "schedules", "run", "api-drift", "--project", "shop")
	var ee *exitError
	if !errors.As(err, &ee) || ee.code != ExitDrift {
		t.Fatalf("run: %v\n%s", err, out)
	}
	if !strings.Contains(out, "  ✓ list passes its dry run") || !strings.Contains(out, "  ✗ gone fails its dry run: step GET /api/gone: check status failed: got 404") {
		t.Errorf("run output:\n%s", out)
	}
	out, _ = runCLI(t, "schedules", "list", "--project", "shop")
	if !strings.Contains(out, "drifted (gone)") {
		t.Errorf("list after the check:\n%s", out)
	}
	if _, err := runCLI(t, "schedules", "create", "x", "--project", "shop", "--scenario", "items", "--cron", "0 6 * * *", "--spec-url", tg.URL+"/openapi.json"); err == nil || !strings.Contains(err.Error(), "specURL applies only to drift schedules") {
		t.Errorf("--spec-url on a run schedule: %v", err)
	}
}
