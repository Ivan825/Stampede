package cli

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/Ivan825/Stampede/internal/client"
	"github.com/Ivan825/Stampede/internal/server"
)

func TestSetupWhoamiPasswordLogout(t *testing.T) {
	url := testServer(t, server.Config{})
	const pw = "a long enough password"

	// The password comes from the prompt (stdin here) and is never echoed.
	out, err := runCLIIn(t, pw+"\n", "setup", "--server", url, "--organisation", "Acme", "--name", "Ada", "--email", "ada@acme.test")
	if err != nil || !strings.Contains(out, "Created Acme with owner ada@acme.test on "+url) || strings.Contains(out, pw) {
		t.Fatalf("setup: %v\n%s", err, out)
	}
	cfg, err := client.ReadConfigFile()
	if err != nil || cfg.Server != url || !strings.HasPrefix(cfg.Token, "stp_") {
		t.Fatalf("config after setup: %+v %v", cfg, err)
	}
	if _, err := runCLIIn(t, pw+"\n", "setup", "--server", url, "--organisation", "B", "--name", "B", "--email", "b@b.test"); err == nil || !strings.Contains(err.Error(), "already been completed") {
		t.Errorf("second setup: %v", err)
	}

	out = mustCLI(t, "whoami")
	if out != "ada@acme.test (Ada), owner of Acme on "+url+"\n" {
		t.Errorf("whoami: %q", out)
	}
	var me struct{ Server, Email, Role, OrgName string }
	cliJSON(t, &me, "whoami")
	if me.Server != url || me.Email != "ada@acme.test" || me.Role != "owner" || me.OrgName != "Acme" {
		t.Errorf("whoami --json: %+v", me)
	}

	// Change the password with the environment variables, then sign in
	// with the new one.
	t.Setenv("STAMPEDE_PASSWORD", "wrong password!")
	t.Setenv("STAMPEDE_NEW_PASSWORD", "the newest password")
	if _, err := runCLI(t, "password"); err == nil || !strings.Contains(err.Error(), "current password is incorrect") {
		t.Errorf("wrong current password: %v", err)
	}
	t.Setenv("STAMPEDE_PASSWORD", pw)
	if out := mustCLI(t, "password"); !strings.Contains(out, "Password changed") {
		t.Errorf("password: %s", out)
	}
	t.Setenv("STAMPEDE_NEW_PASSWORD", "")
	if _, err := runCLI(t, "login", "--server", url, "--email", "ada@acme.test"); err == nil {
		t.Error("login with the old password worked")
	}
	t.Setenv("STAMPEDE_PASSWORD", "the newest password")
	if out := mustCLI(t, "login", "--server", url, "--email", "ada@acme.test"); !strings.Contains(out, "Signed in to "+url+" as ada@acme.test (owner, Acme)") {
		t.Errorf("login: %s", out)
	}

	// logout revokes the token on the server and removes it here.
	cfg, _ = client.ReadConfigFile()
	old := cfg.Token
	out = mustCLI(t, "logout")
	if !strings.Contains(out, "Revoked the token on "+url) || !strings.Contains(out, "Removed the token from") {
		t.Errorf("logout: %s", out)
	}
	if cfg, _ = client.ReadConfigFile(); cfg.Token != "" || cfg.Server != url {
		t.Errorf("config after logout: %+v", cfg)
	}
	c := &client.Client{Server: url, Token: old, HTTP: &http.Client{}}
	if err := c.Do(context.Background(), "GET", "/me", nil, nil); err == nil {
		t.Error("the revoked token still works")
	}
	if _, err := runCLI(t, "whoami"); err == nil || !strings.Contains(err.Error(), "not signed in") {
		t.Errorf("whoami after logout: %v", err)
	}
}
