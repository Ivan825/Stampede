package cli

import (
	"context"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/Ivan825/Stampede/internal/api/gen"
	"github.com/Ivan825/Stampede/internal/client"
)

func TestUsersCommands(t *testing.T) {
	signedIn(t)
	const pw = "pat's first password"
	t.Setenv("STAMPEDE_NEW_PASSWORD", pw)
	out := mustCLI(t, "users", "create", "pat@acme.test", "--name", "Pat Lee", "--role", "editor")
	if out != "Added pat@acme.test (Pat Lee) as editor.\n" {
		t.Errorf("create: %q", out)
	}
	if _, err := runCLI(t, "users", "create", "pat@acme.test", "--name", "Pat"); err == nil {
		t.Error("a second account with the same email was created")
	}
	if _, err := runCLI(t, "users", "create", "x@acme.test"); err == nil || !strings.Contains(err.Error(), "--name is required") {
		t.Errorf("no name: %v", err)
	}

	out = mustCLI(t, "users", "list")
	for _, want := range []string{"EMAIL", "owner@acme.test", "owner", "pat@acme.test", "Pat Lee", "editor"} {
		if !strings.Contains(out, want) {
			t.Errorf("list lacks %q:\n%s", want, out)
		}
	}
	var us []gen.User
	cliJSON(t, &us, "users", "list")
	if len(us) != 2 {
		t.Fatalf("users: %+v", us)
	}

	if out := mustCLI(t, "users", "update", "PAT@acme.test", "--role", "admin", "--name", "Pat L."); out != "Updated pat@acme.test: Pat L., admin.\n" {
		t.Errorf("update: %q", out)
	}
	if _, err := runCLI(t, "users", "update", "pat@acme.test"); err == nil || !strings.Contains(err.Error(), "nothing to change") {
		t.Errorf("update without flags: %v", err)
	}
	if _, err := runCLI(t, "users", "update", "pat@acme.test", "--role", "boss"); err == nil {
		t.Error("an unknown role was accepted")
	}
	// The new member can sign in with the password they were given.
	if _, _, err := client.Login(context.Background(), os.Getenv("STAMPEDE_SERVER"), "pat@acme.test", pw, "t"); err != nil {
		t.Errorf("pat signs in: %v", err)
	}
	if _, err := runCLI(t, "users", "delete", "owner@acme.test"); err == nil || !strings.Contains(err.Error(), "owner") {
		t.Errorf("deleting the last owner: %v", err)
	}
	pat := us[0]
	if pat.Email != "pat@acme.test" {
		pat = us[1]
	}
	if out := mustCLI(t, "users", "delete", pat.Id.String()[:8]); out != "Removed pat@acme.test.\n" {
		t.Errorf("delete by id prefix: %q", out)
	}
	if _, err := runCLI(t, "users", "delete", "pat@acme.test"); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Errorf("delete again: %v", err)
	}
}

func TestTokensCommands(t *testing.T) {
	c := signedIn(t)
	t.Setenv("STAMPEDE_PASSWORD", ownerPassword)
	out, err := runCLI(t, "tokens", "create", "ci", "--role", "runner", "--expires-in-days", "30")
	if err != nil {
		t.Fatalf("create: %v\n%s", err, out)
	}
	if !strings.Contains(out, "Created token ci (runner, expires") || strings.Contains(out, ownerPassword) {
		t.Errorf("create: %s", out)
	}
	secret := strings.TrimSpace(out[strings.LastIndex(strings.TrimSpace(out), "\n")+1:])
	if !strings.HasPrefix(secret, "stp_") {
		t.Fatalf("no secret printed: %q", out)
	}
	ci := &client.Client{Server: c.Server, Token: secret, HTTP: &http.Client{}}
	var me gen.Me
	if err := ci.Do(context.Background(), "GET", "/me", nil, &me); err != nil || me.Role != gen.Runner {
		t.Errorf("the new token: %+v %v", me, err)
	}

	// The secret is printed once: listing shows only its prefix.
	out = mustCLI(t, "tokens", "list")
	if strings.Contains(out, secret) || !strings.Contains(out, secret[:12]+"…") || !strings.Contains(out, "test (this CLI)") || !strings.Contains(out, "runner") {
		t.Errorf("list:\n%s", out)
	}
	var ts []gen.Token
	cliJSON(t, &ts, "tokens", "list")
	if len(ts) != 2 || strings.Contains(mustCLI(t, "tokens", "list", "--json"), secret) {
		t.Errorf("list --json: %+v", ts)
	}
	var created gen.TokenCreated
	cliJSON(t, &created, "tokens", "create", "script")
	if !strings.HasPrefix(created.Secret, "stp_") || created.Role != gen.Owner {
		t.Errorf("create --json: %+v", created)
	}

	t.Setenv("STAMPEDE_PASSWORD", "not my password")
	if _, err := runCLI(t, "tokens", "create", "x"); err == nil {
		t.Error("a token was created with a wrong password")
	}
	if out := mustCLI(t, "tokens", "delete", "ci"); out != "Revoked ci ("+secret[:12]+"…).\n" {
		t.Errorf("delete: %q", out)
	}
	if err := ci.Do(context.Background(), "GET", "/me", nil, nil); err == nil {
		t.Error("a revoked token still works")
	}
	if _, err := runCLI(t, "tokens", "delete", "ci"); err == nil || !strings.Contains(err.Error(), `token "ci" not found`) {
		t.Errorf("delete again: %v", err)
	}
}
