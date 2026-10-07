package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Ivan825/Stampede/internal/api/gen"
	"github.com/Ivan825/Stampede/internal/client"
	"github.com/Ivan825/Stampede/internal/keyring"
	"github.com/Ivan825/Stampede/internal/server"
	"github.com/Ivan825/Stampede/internal/store/storetest"
)

// testServer starts a DB-backed server with secrets enabled and no
// accounts, and points the CLI's config at an empty directory.
func testServer(t *testing.T, cfg server.Config) string {
	t.Helper()
	key, err := keyring.New(bytes.Repeat([]byte{7}, 32))
	if err != nil {
		t.Fatal(err)
	}
	cfg.Store, cfg.Keyring, cfg.Logger = storetest.Open(t), key, slog.New(slog.NewTextHandler(io.Discard, nil))
	srv, err := server.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	hs := httptest.NewServer(srv.Handler())
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		srv.Shutdown(ctx)
		hs.Close()
	})
	t.Setenv("HOME", t.TempDir()) // ignore any real CLI config
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	for _, k := range []string{"STAMPEDE_SERVER", "STAMPEDE_TOKEN", "STAMPEDE_PROJECT", "STAMPEDE_PASSWORD", "STAMPEDE_NEW_PASSWORD"} {
		t.Setenv(k, "")
	}
	return hs.URL
}

const ownerPassword = "correct horse battery"

// signedIn starts a DB-backed server with an owner account and points
// the CLI at it with an API token.
func signedIn(t *testing.T) *client.Client {
	t.Helper()
	return signedInWith(t, server.Config{})
}

func signedInWith(t *testing.T, cfg server.Config) *client.Client {
	t.Helper()
	url := testServer(t, cfg)
	b, _ := json.Marshal(map[string]string{"organisation": "Acme", "name": "Owner", "email": "owner@acme.test", "password": ownerPassword})
	req, _ := http.NewRequest("POST", url+"/api/v1/setup", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Stampede-CSRF", "1")
	resp, err := http.DefaultClient.Do(req)
	if err != nil || resp.StatusCode != 201 {
		t.Fatalf("setup: %v %v", resp, err)
	}
	resp.Body.Close()
	tok, _, err := client.Login(context.Background(), url, "owner@acme.test", ownerPassword, "test")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("STAMPEDE_SERVER", url)
	t.Setenv("STAMPEDE_TOKEN", tok)
	return &client.Client{Server: url, Token: tok, HTTP: &http.Client{Timeout: 30 * time.Second}}
}

func runCLI(t *testing.T, args ...string) (string, error) {
	t.Helper()
	return runCLIIn(t, "", args...)
}

// runCLIIn runs the CLI with stdin read from a string.
func runCLIIn(t *testing.T, stdin string, args ...string) (string, error) {
	t.Helper()
	root := NewRoot()
	var out bytes.Buffer
	root.SetIn(strings.NewReader(stdin))
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs(args)
	err := root.Execute()
	return out.String(), err
}

// mustCLI runs the CLI and fails the test on an error.
func mustCLI(t *testing.T, args ...string) string {
	t.Helper()
	out, err := runCLI(t, args...)
	if err != nil {
		t.Fatalf("stampede %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return out
}

// cliJSON runs the CLI with --json and decodes its output into v.
func cliJSON(t *testing.T, v any, args ...string) {
	t.Helper()
	out := mustCLI(t, append(args, "--json")...)
	if err := json.Unmarshal([]byte(out), v); err != nil {
		t.Fatalf("stampede %s --json: %v\n%s", strings.Join(args, " "), err, out)
	}
}

// shop creates a project "Shop" with a target "local" for tg and the
// "items" scenario, and returns the project's id.
func shop(t *testing.T, c *client.Client, targetURL string) string {
	t.Helper()
	ctx := context.Background()
	var proj gen.Project
	if err := c.Do(ctx, "POST", "/projects", map[string]string{"name": "Shop"}, &proj); err != nil {
		t.Fatal(err)
	}
	pid := proj.Id.String()
	if err := c.Do(ctx, "POST", "/projects/"+pid+"/targets", map[string]string{"name": "local", "baseURL": targetURL}, nil); err != nil {
		t.Fatal(err)
	}
	yaml := "metadata: {name: items}\njourneys:\n  - name: list\n    steps:\n      - get: /api/items\n        check: {status: 200}\nload: {mode: rate, rate: 5/s, duration: 1s}\n"
	if err := c.Do(ctx, "POST", "/projects/"+pid+"/scenarios", map[string]string{"yaml": yaml}, nil); err != nil {
		t.Fatal(err)
	}
	return pid
}
