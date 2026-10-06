// Package plugintest builds Stampede's test plugin for other packages'
// tests.
package plugintest

import (
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"

	"github.com/Ivan825/Stampede/internal/pluginhost"
)

var (
	once  sync.Once
	built string
	err   error
)

// EchoDir builds the echo plugin (once per test binary) and returns a
// plugin directory holding it as stampede-plugin-echo.
func EchoDir(t testing.TB) string {
	t.Helper()
	once.Do(func() {
		dir, e := os.MkdirTemp("", "stampede-plugins-")
		if e != nil {
			err = e
			return
		}
		out := filepath.Join(dir, pluginhost.BinaryName("echo"))
		cmd := exec.Command("go", "build", "-o", out, "github.com/Ivan825/Stampede/internal/pluginhost/plugintest/echo") //nolint:gosec // builds the test plugin
		cmd.Stderr = os.Stderr
		err = cmd.Run()
		built = dir
	})
	if err != nil {
		t.Fatalf("building the echo plugin: %v", err)
	}
	return built
}

// Remove deletes the built plugin; call it from TestMain after m.Run.
func Remove() {
	if built != "" {
		_ = os.RemoveAll(built)
	}
}
