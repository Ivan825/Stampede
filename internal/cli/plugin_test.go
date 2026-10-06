package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Ivan825/Stampede/internal/pluginhost/plugintest"
)

func TestMain(m *testing.M) {
	code := m.Run()
	plugintest.Remove()
	os.Exit(code)
}

// stampede runs the CLI with args and returns stdout, stderr and the error.
func stampede(t *testing.T, args ...string) (string, string, error) {
	t.Helper()
	root := NewRoot()
	var out, errOut bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&errOut)
	root.SetArgs(args)
	err := root.Execute()
	return out.String(), errOut.String(), err
}

func TestPluginCommands(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("STAMPEDE_PLUGIN_DIR", dir)
	echo := filepath.Join(plugintest.EchoDir(t), "stampede-plugin-echo")

	out, _, err := stampede(t, "plugin", "list")
	if err != nil || !strings.Contains(out, "No plugins installed") {
		t.Fatalf("empty list: %q %v", out, err)
	}

	out, _, err = stampede(t, "plugin", "install", echo)
	if err != nil || !strings.Contains(out, "Installed echo 1.0.0 at "+filepath.Join(dir, "stampede-plugin-echo")) {
		t.Fatalf("install: %q %v", out, err)
	}
	out, _, err = stampede(t, "plugin", "list")
	if err != nil || !strings.Contains(out, "echo.say") || !strings.Contains(out, "1.0.0") {
		t.Fatalf("list: %q %v", out, err)
	}
	out, _, err = stampede(t, "plugin", "show", "echo")
	if err != nil || !strings.Contains(out, "target policy applies to: addr") || !strings.Contains(out, `"required": [`) {
		t.Fatalf("show: %q %v", out, err)
	}

	scn := filepath.Join(t.TempDir(), "s.yaml")
	write := func(steps string) {
		t.Helper()
		src := "metadata: {name: t}\njourneys: [{name: j, steps: [" + steps + "]}]\nload: {vus: 1, iterations: 1}\n"
		if err := os.WriteFile(scn, []byte(src), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(`{plugin: echo.say, with: {text: "${vu}"}}`)
	if out, errOut, err := stampede(t, "validate", scn); err != nil || !strings.Contains(out, "✓") {
		t.Fatalf("valid plugin step: %q %q %v", out, errOut, err)
	}
	write(`{plugin: echo.say, with: {txt: hi}}`)
	if _, errOut, err := stampede(t, "validate", scn); err == nil || !strings.Contains(errOut, "missing property 'text'") {
		t.Fatalf("invalid plugin step: %q %v", errOut, err)
	}
	write(`{plugin: mqtt.publish, with: {topic: t}}`)
	if _, errOut, err := stampede(t, "validate", scn); err != nil || !strings.Contains(errOut, "plugin mqtt is not installed here") {
		t.Fatalf("missing plugin: %q %v", errOut, err)
	}

	if out, _, err := stampede(t, "plugin", "remove", "echo"); err != nil || !strings.Contains(out, "Removed") {
		t.Fatalf("remove: %q %v", out, err)
	}
	if _, _, err := stampede(t, "plugin", "remove", "echo"); err == nil {
		t.Fatal("removing a plugin twice should fail")
	}
	if _, _, err := stampede(t, "plugin", "install", "not a spec"); err == nil {
		t.Fatal("a bad spec should fail")
	}
}
