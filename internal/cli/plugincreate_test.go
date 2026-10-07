package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestPluginCreate generates a plugin against this checkout and runs the
// generated module's own tests: it must build, and its conformance test
// must pass.
func TestPluginCreate(t *testing.T) {
	if testing.Short() {
		t.Skip("builds a plugin module")
	}
	repo, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	dest := t.TempDir()
	out, err := runCLI(t, "plugin", "create", "lineproto", "--dest", dest, "--sdk", repo)
	if err != nil {
		t.Fatalf("create: %v\n%s", err, out)
	}
	root := filepath.Join(dest, "stampede-plugin-lineproto")
	gomod, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil || !strings.Contains(string(gomod), "module example.com/stampede-plugin-lineproto") ||
		!strings.Contains(string(gomod), "replace github.com/Ivan825/Stampede => "+filepath.ToSlash(repo)) {
		t.Fatalf("go.mod: %v\n%s", err, gomod)
	}
	for _, args := range [][]string{{"vet", "./..."}, {"test", "-count=1", "./..."}} {
		cmd := exec.Command("go", args...)
		cmd.Dir = root
		if b, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("go %s in the generated plugin: %v\n%s", strings.Join(args, " "), err, b)
		}
	}
}

func TestPluginCreateChecks(t *testing.T) {
	dest := t.TempDir()
	if _, err := runCLI(t, "plugin", "create", "Bad_Name", "--dest", dest, "--no-tidy"); err == nil || !strings.Contains(err.Error(), "lowercase") {
		t.Errorf("bad name: %v", err)
	}
	if _, err := runCLI(t, "plugin", "create", "x", "--dest", dest, "--sdk", dest); err == nil || !strings.Contains(err.Error(), "not a Stampede checkout") {
		t.Errorf("bad --sdk: %v", err)
	}
	out, err := runCLI(t, "plugin", "create", "smtp", "--dest", dest, "--module", "github.com/you/smtp", "--no-tidy")
	if err != nil || !strings.Contains(out, "main_test.go") {
		t.Fatalf("create: %v %s", err, out)
	}
	main, _ := os.ReadFile(filepath.Join(dest, "stampede-plugin-smtp", "main.go"))
	if !strings.Contains(string(main), `Name:        "smtp",`) || !strings.Contains(string(main), `"smtp timeout"`) {
		t.Errorf("main.go is not named for the plugin:\n%s", main)
	}
	if _, err := runCLI(t, "plugin", "create", "smtp", "--dest", dest, "--no-tidy"); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Errorf("second create: %v", err)
	}
}
