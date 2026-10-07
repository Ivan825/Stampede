package cli

import (
	"context"
	"strings"
	"testing"

	"github.com/Ivan825/Stampede/internal/api/gen"
	"github.com/Ivan825/Stampede/internal/client"
)

func TestProjectsCommands(t *testing.T) {
	c := signedIn(t)
	if out := mustCLI(t, "projects", "list"); !strings.Contains(out, "No projects") {
		t.Errorf("empty list: %s", out)
	}
	if _, err := runCLI(t, "runs"); err == nil || !strings.Contains(err.Error(), "no projects yet") {
		t.Errorf("runs without projects: %v", err)
	}
	if out := mustCLI(t, "projects", "create", "Shop", "--description", "The storefront"); out != "Created project Shop (slug shop).\n" {
		t.Errorf("create: %q", out)
	}
	var blog gen.Project
	cliJSON(t, &blog, "projects", "create", "Blog")
	if blog.Slug != "blog" || blog.Name != "Blog" {
		t.Errorf("create --json: %+v", blog)
	}
	out := mustCLI(t, "projects", "list")
	for _, want := range []string{"NAME", "Shop", "shop", "owner", "The storefront", "Blog"} {
		if !strings.Contains(out, want) {
			t.Errorf("list lacks %q:\n%s", want, out)
		}
	}

	// Two projects: commands need --project, or a default project.
	if _, err := runCLI(t, "runs"); err == nil || !strings.Contains(err.Error(), "several projects exist") {
		t.Errorf("runs with two projects: %v", err)
	}
	if out := mustCLI(t, "projects", "use", "SHOP"); out != "Default project: Shop (slug shop).\n" {
		t.Errorf("use: %q", out)
	}
	if cfg, _ := client.ReadConfigFile(); cfg.Project != "shop" {
		t.Errorf("config: %+v", cfg)
	}
	if out := mustCLI(t, "projects", "list"); !strings.Contains(out, "Shop (default)") {
		t.Errorf("list marks no default:\n%s", out)
	}
	mustCLI(t, "runs")
	if out := mustCLI(t, "whoami"); !strings.Contains(out, "Default project: shop") {
		t.Errorf("whoami: %s", out)
	}
	if err := c.Do(context.Background(), "POST", "/projects/"+blog.Id.String()+"/targets", map[string]string{"name": "local", "baseURL": "http://127.0.0.1:9"}, nil); err != nil {
		t.Fatal(err)
	}
	out = mustCLI(t, "projects", "show", "blog")
	for _, want := range []string{"Blog (slug blog, id " + blog.Id.String() + ")", "Your role:  owner", "Caps:       none", "Dry run:    not required", "Targets:    1  local (http://127.0.0.1:9)", "Scenarios:  0"} {
		if !strings.Contains(out, want) {
			t.Errorf("show lacks %q:\n%s", want, out)
		}
	}
	if out := mustCLI(t, "projects", "show"); !strings.Contains(out, "Shop (slug shop") || !strings.Contains(out, "The storefront") {
		t.Errorf("show of the default project:\n%s", out)
	}

	if out := mustCLI(t, "projects", "update", "blog", "--name", "Journal"); out != "Updated project Journal (slug journal).\n" {
		t.Errorf("update: %q", out)
	}
	var p gen.Project
	cliJSON(t, &p, "projects", "update", "journal", "--description", "Posts")
	if p.Name != "Journal" || *p.Description != "Posts" {
		t.Errorf("update --json: %+v", p)
	}
	if _, err := runCLI(t, "projects", "update", "journal"); err == nil || !strings.Contains(err.Error(), "nothing to change") {
		t.Errorf("update without flags: %v", err)
	}
	// Renaming the default project keeps it the default.
	mustCLI(t, "projects", "update", "shop", "--name", "Store")
	if cfg, _ := client.ReadConfigFile(); cfg.Project != "store" {
		t.Errorf("default after a rename: %+v", cfg)
	}
	mustCLI(t, "projects", "update", "store", "--name", "Shop")

	// Settings: caps and the dry-run gate.
	if out := mustCLI(t, "projects", "settings", "show"); out != "Caps:     none\nDry run:  not required\n" {
		t.Errorf("settings show: %q", out)
	}
	out = mustCLI(t, "projects", "settings", "set", "--max-rate", "200", "--max-duration", "1h", "--require-dry-run")
	if out != "Caps:     rate ≤ 200/s, duration ≤ 1h0m0s\nDry run:  required before every run\n" {
		t.Errorf("settings set: %q", out)
	}
	var s gen.ProjectSettings
	cliJSON(t, &s, "projects", "settings", "set", "--project", "shop", "--max-vus", "50", "--max-rate", "0", "--require-dry-run=false")
	if s.Caps.MaxRate != nil || *s.Caps.MaxVUs != 50 || *s.Caps.MaxDurationSeconds != 3600 || s.RequireDryRun {
		t.Errorf("settings set --json: %+v", s)
	}
	if _, err := runCLI(t, "projects", "settings", "set"); err == nil || !strings.Contains(err.Error(), "nothing to change") {
		t.Errorf("settings set without flags: %v", err)
	}

	// Roles: an editor in the organisation, a viewer in Shop.
	t.Setenv("STAMPEDE_NEW_PASSWORD", "pat's first password")
	mustCLI(t, "users", "create", "pat@acme.test", "--name", "Pat", "--role", "editor")
	if out := mustCLI(t, "projects", "roles", "list"); !strings.Contains(out, "No overrides") {
		t.Errorf("roles list: %s", out)
	}
	if out := mustCLI(t, "projects", "roles", "set", "pat@acme.test", "viewer"); out != "pat@acme.test is viewer in this project (editor in the organisation).\n" {
		t.Errorf("roles set: %q", out)
	}
	out = mustCLI(t, "projects", "roles", "list")
	if !strings.Contains(out, "pat@acme.test") || !strings.Contains(out, "viewer") || !strings.Contains(out, "editor") {
		t.Errorf("roles list:\n%s", out)
	}
	var rs []gen.ProjectRole
	cliJSON(t, &rs, "projects", "roles", "list")
	if len(rs) != 1 || rs[0].Role != gen.Viewer {
		t.Errorf("roles list --json: %+v", rs)
	}
	if _, err := runCLI(t, "projects", "roles", "set", "owner@acme.test", "viewer"); err == nil {
		t.Error("an owner was overridden")
	}
	if out := mustCLI(t, "projects", "roles", "remove", "pat@acme.test"); out != "pat@acme.test has their organisation role (editor) in this project again.\n" {
		t.Errorf("roles remove: %q", out)
	}

	// Deleting needs --yes, and clears the default project.
	if _, err := runCLI(t, "projects", "delete", "shop"); err == nil || !strings.Contains(err.Error(), "add --yes") {
		t.Errorf("delete without --yes: %v", err)
	}
	if out := mustCLI(t, "projects", "delete", "shop", "--yes"); out != "Deleted project Shop.\n" {
		t.Errorf("delete: %q", out)
	}
	if cfg, _ := client.ReadConfigFile(); cfg.Project != "" {
		t.Errorf("default project kept: %+v", cfg)
	}
	if _, err := runCLI(t, "projects", "show", "shop"); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Errorf("show deleted: %v", err)
	}
	if out := mustCLI(t, "projects", "use", "--clear"); out != "No default project.\n" {
		t.Errorf("use --clear: %q", out)
	}
}

func TestCapsCommands(t *testing.T) {
	signedIn(t)
	if out := mustCLI(t, "caps", "show"); out != "Organisation caps: none\n" {
		t.Errorf("show: %q", out)
	}
	if out := mustCLI(t, "caps", "set", "--max-rate", "1000", "--max-vus", "5000", "--max-duration", "2h"); out != "Organisation caps: rate ≤ 1000/s, VUs ≤ 5000, duration ≤ 2h0m0s\n" {
		t.Errorf("set: %q", out)
	}
	var caps gen.Caps
	cliJSON(t, &caps, "caps", "set", "--max-vus", "0")
	if caps.MaxVUs != nil || *caps.MaxRate != 1000 || *caps.MaxDurationSeconds != 7200 {
		t.Errorf("set --json: %+v", caps)
	}
	cliJSON(t, &caps, "caps", "show")
	if caps.MaxVUs != nil || *caps.MaxRate != 1000 {
		t.Errorf("show --json: %+v", caps)
	}
	if _, err := runCLI(t, "caps", "set", "--max-rate", "-1"); err == nil {
		t.Error("a negative cap was accepted")
	}
	if _, err := runCLI(t, "caps", "set", "--clear", "--max-vus", "3"); err == nil || !strings.Contains(err.Error(), "leave out the other flags") {
		t.Errorf("--clear with a cap: %v", err)
	}
	if out := mustCLI(t, "caps", "set", "--clear"); out != "Organisation caps: none\n" {
		t.Errorf("clear: %q", out)
	}
}
