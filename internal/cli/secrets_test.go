package cli

import (
	"strings"
	"testing"

	"github.com/Ivan825/Stampede/internal/api/gen"
)

func TestSecretsCommands(t *testing.T) {
	signedIn(t)
	mustCLI(t, "projects", "create", "Shop")
	if out := mustCLI(t, "secrets", "list"); !strings.Contains(out, "No secrets") {
		t.Errorf("empty list: %s", out)
	}
	const fromStdin, fromEnv = "s3cret-from-stdin", "s3cret-from-env"
	out, err := runCLIIn(t, fromStdin+"\n", "secrets", "set", "SHOP_PASSWORD")
	if err != nil || out != "Saved secret SHOP_PASSWORD (encrypted; use it as ${secret.SHOP_PASSWORD}).\n" {
		t.Errorf("set from stdin: %v %q", err, out)
	}
	t.Setenv("STAGING_KEY", fromEnv)
	if out := mustCLI(t, "secrets", "set", "API_KEY", "--from-env", "STAGING_KEY", "--project", "shop"); strings.Contains(out, fromEnv) {
		t.Errorf("set printed the value: %q", out)
	}
	if _, err := runCLI(t, "secrets", "set", "X", "--from-env", "NO_SUCH_VARIABLE_HERE"); err == nil || !strings.Contains(err.Error(), "NO_SUCH_VARIABLE_HERE is not set") {
		t.Errorf("missing variable: %v", err)
	}
	if _, err := runCLIIn(t, "", "secrets", "set", "EMPTY"); err == nil || !strings.Contains(err.Error(), "empty") {
		t.Errorf("empty value: %v", err)
	}
	if _, err := runCLIIn(t, "x", "secrets", "set", "not-an-identifier"); err == nil || !strings.Contains(err.Error(), "identifiers") {
		t.Errorf("bad name: %v", err)
	}

	// Values are never printed, in tables or JSON.
	out = mustCLI(t, "secrets", "list")
	jsonOut := mustCLI(t, "secrets", "list", "--json")
	for _, o := range []string{out, jsonOut} {
		if strings.Contains(o, fromStdin) || strings.Contains(o, fromEnv) {
			t.Errorf("a secret value was printed:\n%s", o)
		}
	}
	if !strings.Contains(out, "API_KEY") || !strings.Contains(out, "SHOP_PASSWORD") {
		t.Errorf("list:\n%s", out)
	}
	var ss []gen.Secret
	cliJSON(t, &ss, "secrets", "list")
	if len(ss) != 2 {
		t.Errorf("list --json: %+v", ss)
	}

	if out := mustCLI(t, "secrets", "delete", "API_KEY"); out != "Deleted secret API_KEY.\n" {
		t.Errorf("delete: %q", out)
	}
	if _, err := runCLI(t, "secrets", "delete", "API_KEY"); err == nil {
		t.Error("deleting a missing secret succeeded")
	}
	cliJSON(t, &ss, "secrets", "list")
	if len(ss) != 1 || ss[0].Name != "SHOP_PASSWORD" {
		t.Errorf("after delete: %+v", ss)
	}
}
