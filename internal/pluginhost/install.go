package pluginhost

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/Ivan825/Stampede/pkg/pluginsdk"
)

// Module is Stampede's module path; first-party plugins live under
// plugins/<name> in it, each a module of its own.
const Module = "github.com/Ivan825/Stampede"

// SourceEnv points at a Stampede source checkout for first-party installs.
const SourceEnv = "STAMPEDE_SOURCE"

// RepoEnv overrides the repository cloned when no checkout is found.
const RepoEnv = "STAMPEDE_PLUGIN_REPO"

// DefaultRepo is cloned to build first-party plugins without a checkout.
const DefaultRepo = "https://github.com/Ivan825/Stampede"

// InstallOptions configures Install.
type InstallOptions struct {
	// Dir is where the plugin is installed (Dir() when empty).
	Dir string
	// Source is a Stampede checkout to build first-party plugins from.
	// Empty means $STAMPEDE_SOURCE, then the checkout containing the
	// current directory, then a fresh clone of the repository.
	Source string
	// Ref is the branch or tag cloned when there is no checkout (default:
	// the repository's default branch).
	Ref string
	// Output receives the build tools' output.
	Output io.Writer
	Log    *slog.Logger
}

// Install builds or copies a plugin and installs it into the plugin
// directory as stampede-plugin-<name>, where name is what the plugin
// calls itself. spec is
//
//   - the name of a first-party plugin (mqtt, kafka, redis, sql, udp),
//     built from plugins/<name> of a Stampede checkout;
//   - a directory holding a plugin's main package, built with go build;
//   - an executable plugin file, copied;
//   - a Go package path, optionally with @version, built with go install.
//
// The result is started and must describe itself validly before it is
// installed.
func Install(ctx context.Context, spec string, o InstallOptions) (*Plugin, string, error) {
	if o.Output == nil {
		o.Output = io.Discard
	}
	dir := o.Dir
	if dir == "" {
		d, err := Dir()
		if err != nil {
			return nil, "", err
		}
		dir = d
	}
	tmp, err := os.MkdirTemp("", "stampede-plugin-build-")
	if err != nil {
		return nil, "", err
	}
	defer os.RemoveAll(tmp)
	bin := filepath.Join(tmp, "plugin")
	firstParty := ""

	switch fi, statErr := os.Stat(spec); {
	case statErr == nil && fi.IsDir():
		if err := goBuild(ctx, spec, bin, o.Output); err != nil {
			return nil, "", err
		}
	case statErr == nil:
		if err := copyFile(spec, bin); err != nil {
			return nil, "", err
		}
	case pluginsdk.NameRe.MatchString(spec):
		firstParty = spec
		src, err := firstPartySource(ctx, spec, o, tmp)
		if err != nil {
			return nil, "", err
		}
		if err := goBuild(ctx, src, bin, o.Output); err != nil {
			return nil, "", err
		}
	case strings.Contains(spec, "/"):
		if bin, err = goInstall(ctx, spec, tmp, o.Output); err != nil {
			return nil, "", err
		}
	default:
		return nil, "", fmt.Errorf("%q is not a plugin name, a directory, a file or a Go package path", spec)
	}

	p, err := Start(ctx, bin, o.Log)
	if err != nil {
		return nil, "", err
	}
	p.Kill()
	if firstParty != "" && p.Name != firstParty {
		return nil, "", fmt.Errorf("plugins/%s describes itself as %q", firstParty, p.Name)
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, "", err
	}
	dst := filepath.Join(dir, BinaryName(p.Name))
	if err := copyFile(bin, dst); err != nil {
		return nil, "", err
	}
	p.Path = dst
	return p, dst, nil
}

// Remove deletes an installed plugin from the plugin directory.
func Remove(dir, name string) (string, error) {
	if dir == "" {
		d, err := Dir()
		if err != nil {
			return "", err
		}
		dir = d
	}
	if !pluginsdk.NameRe.MatchString(name) {
		return "", fmt.Errorf("%q is not a valid plugin name", name)
	}
	p := filepath.Join(dir, BinaryName(name))
	if err := os.Remove(p); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			if other, ferr := exec.LookPath(BinaryName(name)); ferr == nil {
				return "", fmt.Errorf("plugin %s is not in %s; the one on PATH (%s) was not installed by stampede, remove it yourself", name, dir, other)
			}
			return "", fmt.Errorf("plugin %s is not installed in %s", name, dir)
		}
		return "", err
	}
	return p, nil
}

// firstPartySource finds plugins/<name> in a Stampede checkout, cloning
// the repository when there is none.
func firstPartySource(ctx context.Context, name string, o InstallOptions, tmp string) (string, error) {
	roots := []string{o.Source, os.Getenv(SourceEnv)}
	if wd, err := os.Getwd(); err == nil {
		roots = append(roots, checkoutAbove(wd))
	}
	for _, root := range roots {
		if root == "" {
			continue
		}
		if !isCheckout(root) {
			if root == o.Source {
				return "", fmt.Errorf("%s is not a Stampede source checkout", root)
			}
			continue
		}
		dir := filepath.Join(root, "plugins", name)
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err != nil { //nolint:gosec // the checkout the user pointed at
			return "", fmt.Errorf("there is no first-party plugin %q (no plugins/%s in %s)", name, name, root)
		}
		return dir, nil
	}

	repo := os.Getenv(RepoEnv)
	if repo == "" {
		repo = DefaultRepo
	}
	clone := filepath.Join(tmp, "src")
	args := []string{"clone", "--quiet", "--depth", "1"}
	if o.Ref != "" {
		args = append(args, "--branch", o.Ref)
	}
	args = append(args, repo, clone)
	cmd := exec.CommandContext(ctx, "git", args...) //nolint:gosec // git with the repository the user configured
	cmd.Stdout, cmd.Stderr = o.Output, o.Output
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("no Stampede checkout found and cloning %s failed: %w (set %s to a checkout)", repo, err, SourceEnv)
	}
	dir := filepath.Join(clone, "plugins", name)
	if _, err := os.Stat(filepath.Join(dir, "go.mod")); err != nil {
		return "", fmt.Errorf("there is no first-party plugin %q in %s", name, repo)
	}
	return dir, nil
}

// checkoutAbove returns the Stampede checkout containing dir, or "".
func checkoutAbove(dir string) string {
	for d := dir; ; d = filepath.Dir(d) {
		if isCheckout(d) {
			return d
		}
		if filepath.Dir(d) == d {
			return ""
		}
	}
}

// isCheckout reports whether root is the root of Stampede's module.
func isCheckout(root string) bool {
	f, err := os.Open(filepath.Join(root, "go.mod")) //nolint:gosec // the checkout the user pointed at
	if err != nil {
		return false
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if line := strings.TrimSpace(sc.Text()); strings.HasPrefix(line, "module ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "module ")) == Module
		}
	}
	return false
}

func goBuild(ctx context.Context, dir, out string, w io.Writer) error {
	cmd := exec.CommandContext(ctx, "go", "build", "-trimpath", "-o", out, ".") //nolint:gosec // builds the plugin source the user chose
	cmd.Dir = dir
	cmd.Stdout, cmd.Stderr = w, w
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("go build in %s: %w", dir, err)
	}
	return nil
}

// goInstall builds a Go package path into a temporary GOBIN and returns
// the executable it produced.
func goInstall(ctx context.Context, pkg, tmp string, w io.Writer) (string, error) {
	if !strings.Contains(pkg, "@") {
		pkg += "@latest"
	}
	gobin := filepath.Join(tmp, "bin")
	cmd := exec.CommandContext(ctx, "go", "install", "-trimpath", pkg) //nolint:gosec // installs the Go package the user named
	cmd.Env = append(os.Environ(), "GOBIN="+gobin)
	cmd.Stdout, cmd.Stderr = w, w
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("go install %s: %w", pkg, err)
	}
	entries, err := os.ReadDir(gobin)
	if err != nil || len(entries) != 1 {
		return "", fmt.Errorf("go install %s did not produce one executable", pkg)
	}
	return filepath.Join(gobin, entries[0].Name()), nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp := dst + ".tmp"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755) //nolint:gosec // plugins are executables
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := out.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, dst)
}
