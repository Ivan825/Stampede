package cli

import (
	"strings"
	"testing"
	"time"

	"github.com/Ivan825/Stampede/internal/api/gen"
)

func TestParseWhen(t *testing.T) {
	now := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	for in, want := range map[string]time.Time{
		"24h":                  now.Add(-24 * time.Hour),
		"7d":                   now.Add(-7 * 24 * time.Hour),
		"90m":                  now.Add(-90 * time.Minute),
		"2026-10-01T08:00:00Z": time.Date(2026, 10, 1, 8, 0, 0, 0, time.UTC),
	} {
		if got, err := parseWhen(in, now); err != nil || !got.Equal(want) {
			t.Errorf("%s: %v %v", in, got, err)
		}
	}
	if got, err := parseWhen("2026-10-01", now); err != nil || got.Day() != 1 {
		t.Errorf("date: %v %v", got, err)
	}
	for _, bad := range []string{"yesterday", "-1h", "xd"} {
		if _, err := parseWhen(bad, now); err == nil {
			t.Errorf("%s was accepted", bad)
		}
	}
}

func TestAuditAndSettingsCommands(t *testing.T) {
	signedIn(t)
	mustCLI(t, "projects", "create", "Shop")
	mustCLI(t, "targets", "create", "local", "--base-url", "http://127.0.0.1:9", "--max-vus", "20")
	mustCLI(t, "caps", "set", "--max-rate", "500")

	out := mustCLI(t, "audit")
	for _, want := range []string{"WHEN", "ACTOR", "owner@acme.test", "project.create", "target.create", "setup", "org.caps"} {
		if !strings.Contains(out, want) {
			t.Errorf("audit lacks %q:\n%s", want, out)
		}
	}
	var es []gen.AuditEntry
	cliJSON(t, &es, "audit", "--limit", "2")
	if len(es) != 2 || es[0].At.Before(es[1].At) {
		t.Errorf("audit --limit 2: %+v", es)
	}
	cliJSON(t, &es, "audit", "--action", "target.")
	if len(es) != 1 || es[0].Action != "target.create" {
		t.Errorf("audit --action: %+v", es)
	}
	cliJSON(t, &es, "audit", "--since", "1h")
	if len(es) < 4 {
		t.Errorf("audit --since 1h: %+v", es)
	}
	cliJSON(t, &es, "audit", "--before", "1h")
	if len(es) != 0 {
		t.Errorf("audit --before 1h: %+v", es)
	}
	if out := mustCLI(t, "audit", "--since", "2000-01-01", "--before", "2000-01-02"); out != "No audit entries.\n" {
		t.Errorf("an empty window: %q", out)
	}
	if _, err := runCLI(t, "audit", "--since", "yesterday"); err == nil || !strings.Contains(err.Error(), "--since") {
		t.Errorf("bad --since: %v", err)
	}

	if out := mustCLI(t, "settings", "sso"); !strings.HasPrefix(out, "Single sign-on is off") {
		t.Errorf("settings sso: %q", out)
	}
	var sso gen.SSOSettings
	cliJSON(t, &sso, "settings", "sso")
	if sso.Enabled || !sso.PasswordLogin {
		t.Errorf("settings sso --json: %+v", sso)
	}
	out = mustCLI(t, "settings", "limits")
	for _, want := range []string{"Organisation:        rate ≤ 500/s", "Unverified public:", "PROJECT", "Shop", "Shop/local", "private", "VUs ≤ 20"} {
		if !strings.Contains(out, want) {
			t.Errorf("settings limits lacks %q:\n%s", want, out)
		}
	}
	var l gen.LimitSettings
	cliJSON(t, &l, "settings", "limits")
	if len(l.Targets) != 1 || *l.Targets[0].Effective.MaxRate != 500 || *l.Targets[0].Effective.MaxVUs != 20 {
		t.Errorf("settings limits --json: %+v", l)
	}
}
