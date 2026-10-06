// Package pluginhost finds, starts and talks to protocol plugins: the
// executables that implement steps such as mqtt.publish. Each plugin runs
// as a child process speaking the gRPC contract in
// proto/stampede/plugin/v1 through hashicorp/go-plugin, so a plugin that
// crashes fails its own steps and nothing else.
package pluginhost

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/Ivan825/Stampede/pkg/pluginsdk"
)

// BinaryPrefix starts every plugin executable's name.
const BinaryPrefix = "stampede-plugin-"

// DirEnv overrides the plugin directory.
const DirEnv = "STAMPEDE_PLUGIN_DIR"

// Dir is where `stampede plugin install` puts plugins: $STAMPEDE_PLUGIN_DIR,
// or plugins/ under the user config directory (~/.config/stampede/plugins
// on Linux, ~/Library/Application Support/stampede/plugins on macOS).
func Dir() (string, error) {
	if d := os.Getenv(DirEnv); d != "" {
		return d, nil
	}
	cfg, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("no plugin directory: set %s (%w)", DirEnv, err)
	}
	return filepath.Join(cfg, "stampede", "plugins"), nil
}

// BinaryName is the executable name for a plugin.
func BinaryName(name string) string {
	if runtime.GOOS == "windows" {
		return BinaryPrefix + name + ".exe"
	}
	return BinaryPrefix + name
}

// Installed is a plugin executable found on this machine.
type Installed struct {
	Name string
	Path string
	// InDir is true for plugins in the plugin directory, false for ones
	// found on PATH.
	InDir bool
}

// ErrNotInstalled is returned by Find for a plugin that is nowhere to be
// found.
var ErrNotInstalled = errors.New("plugin is not installed")

// Find locates a plugin's executable: first in dir (Dir() when empty),
// then on PATH.
func Find(dir, name string) (string, error) {
	if !pluginsdk.NameRe.MatchString(name) {
		return "", fmt.Errorf("%q is not a valid plugin name", name)
	}
	if dir == "" {
		d, err := Dir()
		if err == nil {
			dir = d
		}
	}
	if dir != "" {
		p := filepath.Join(dir, BinaryName(name))
		if isExecutable(p) {
			return p, nil
		}
	}
	if p, err := exec.LookPath(BinaryName(name)); err == nil {
		return p, nil
	}
	return "", fmt.Errorf("%w: %s (looked in %s and on PATH; install it with `stampede plugin install %s`)", ErrNotInstalled, name, dir, name)
}

// List returns the installed plugins, those in dir (Dir() when empty)
// first; a plugin in dir hides one of the same name on PATH.
func List(dir string) ([]Installed, error) {
	if dir == "" {
		d, err := Dir()
		if err != nil {
			return nil, err
		}
		dir = d
	}
	seen := map[string]bool{}
	var out []Installed
	scan := func(d string, inDir bool) {
		entries, err := os.ReadDir(d)
		if err != nil {
			return
		}
		for _, e := range entries {
			name, ok := pluginName(e)
			if !ok || seen[name] || !isExecutable(filepath.Join(d, e.Name())) {
				continue
			}
			seen[name] = true
			out = append(out, Installed{Name: name, Path: filepath.Join(d, e.Name()), InDir: inDir})
		}
	}
	scan(dir, true)
	for _, d := range filepath.SplitList(os.Getenv("PATH")) {
		if d != "" && d != dir {
			scan(d, false)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func pluginName(e fs.DirEntry) (string, bool) {
	n := e.Name()
	if e.IsDir() || !strings.HasPrefix(n, BinaryPrefix) {
		return "", false
	}
	n = strings.TrimPrefix(n, BinaryPrefix)
	if runtime.GOOS == "windows" {
		n = strings.TrimSuffix(n, ".exe")
	}
	return n, pluginsdk.NameRe.MatchString(n)
}

func isExecutable(p string) bool {
	fi, err := os.Stat(p) //nolint:gosec // plugin paths are where the user put plugins
	if err != nil || fi.IsDir() {
		return false
	}
	return runtime.GOOS == "windows" || fi.Mode()&0o111 != 0
}
