package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Ivan825/Stampede/internal/version"
)

// sdkModule is the module that holds pkg/pluginsdk.
const sdkModule = "github.com/Ivan825/Stampede"

// pluginNameRe is the SDK's rule for plugin names.
var pluginNameRe = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)

type pluginCreateFlags struct {
	dir, module, sdk string
	noTidy           bool
}

func newPluginCreateCmd() *cobra.Command {
	f := &pluginCreateFlags{}
	cmd := &cobra.Command{
		Use:   "create <name>",
		Short: "Scaffold a plugin: a Go module with one step, a README and a conformance test",
		Long: `Creates <dest>/stampede-plugin-<name>, a Go module built on pkg/pluginsdk
with one working step (<name>.send: write a line to a TCP server and time
the reply line), a README, and a test that starts a local TCP server and
runs the SDK's conformance suite against the built plugin. Replace the
step with your protocol and keep the test passing.

The module depends on github.com/Ivan825/Stampede at this binary's
version (the latest one for a development build). With --sdk it builds
against a Stampede checkout instead, through a replace directive, as the
first-party plugins do. go mod tidy runs at the end when Go is installed.`,
		Example: `  stampede plugin create smtp
  cd stampede-plugin-smtp && go test ./... && stampede plugin install .
  stampede plugin create smtp --module github.com/you/stampede-plugin-smtp --sdk ~/src/Stampede`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			root, err := createPlugin(cmd.Context(), cmd.ErrOrStderr(), args[0], f)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "Created %s:\n", root)
			for _, pf := range pluginFiles(args[0]) {
				fmt.Fprintf(out, "  %s\n", pf.path)
			}
			fmt.Fprintf(out, "  go.mod\n\nNext: cd %s && go test ./... && stampede plugin install .\n", root)
			return nil
		},
	}
	fl := cmd.Flags()
	// --dir is taken: on plugin commands it names the plugin directory.
	fl.StringVar(&f.dir, "dest", ".", "folder to create the plugin in")
	fl.StringVar(&f.module, "module", "", "Go module path (default example.com/stampede-plugin-<name>)")
	fl.StringVar(&f.sdk, "sdk", "", "a Stampede source checkout to build against (adds a replace directive)")
	fl.BoolVar(&f.noTidy, "no-tidy", false, "do not run go get and go mod tidy")
	return cmd
}

// createPlugin writes the scaffold and, unless told not to, resolves its
// dependencies with the Go toolchain.
func createPlugin(ctx context.Context, log io.Writer, name string, f *pluginCreateFlags) (string, error) {
	if !pluginNameRe.MatchString(name) {
		return "", fmt.Errorf("plugin name %q: use lowercase letters, digits and hyphens, starting with a letter", name)
	}
	root := filepath.Join(f.dir, "stampede-plugin-"+name)
	if _, err := os.Stat(root); err == nil {
		return "", fmt.Errorf("%s already exists", root)
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	module := f.module
	if module == "" {
		module = "example.com/stampede-plugin-" + name
	}
	gomod := fmt.Sprintf("module %s\n\ngo 1.24\n", module)
	if f.sdk != "" {
		sdk, err := filepath.Abs(f.sdk)
		if err != nil {
			return "", err
		}
		if _, err := os.Stat(filepath.Join(sdk, "pkg", "pluginsdk", "sdk.go")); err != nil {
			return "", fmt.Errorf("--sdk %s is not a Stampede checkout (no pkg/pluginsdk)", f.sdk)
		}
		gomod += fmt.Sprintf("\nrequire %s v0.0.0-00010101000000-000000000000\n\n// Build against the SDK in a local Stampede checkout.\nreplace %s => %s\n", sdkModule, sdkModule, filepath.ToSlash(sdk))
	}
	if err := os.MkdirAll(root, 0o750); err != nil {
		return "", err
	}
	files := append(pluginFiles(name), packFile{"go.mod", gomod})
	for _, pf := range files {
		if err := os.WriteFile(filepath.Join(root, pf.path), []byte(pf.body), 0o600); err != nil {
			return "", err
		}
	}
	if f.noTidy {
		return root, nil
	}
	if _, err := exec.LookPath("go"); err != nil {
		fmt.Fprintf(log, "Go is not installed, so the module's dependencies are not resolved yet: run go mod tidy in %s.\n", root)
		return root, nil
	}
	if f.sdk == "" {
		if err := goCmd(ctx, log, root, "get", sdkModule+"@"+sdkVersion()); err != nil {
			return "", err
		}
	}
	return root, goCmd(ctx, log, root, "mod", "tidy")
}

// sdkVersion is the SDK version a new plugin requires: this binary's
// release, or the latest one for a development build.
func sdkVersion() string {
	v := version.Version
	if regexp.MustCompile(`^v?\d+\.\d+\.\d+$`).MatchString(v) {
		return "v" + strings.TrimPrefix(v, "v")
	}
	return "latest"
}

func goCmd(ctx context.Context, log io.Writer, dir string, args ...string) error {
	fmt.Fprintf(log, "go %s\n", strings.Join(args, " "))
	cmd := exec.CommandContext(ctx, "go", args...) //nolint:gosec // go get and go mod tidy in the new module
	cmd.Dir = dir
	cmd.Stdout, cmd.Stderr = log, log
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("go %s in %s: %w", strings.Join(args, " "), dir, err)
	}
	return nil
}

// pluginFiles is the scaffold of a plugin called name, without go.mod.
func pluginFiles(name string) []packFile {
	r := strings.NewReplacer("{{name}}", name)
	return []packFile{
		{"main.go", r.Replace(pluginMain)},
		{"main_test.go", r.Replace(pluginTest)},
		{"README.md", r.Replace(pluginREADME)},
	}
}

const pluginMain = `// Command stampede-plugin-{{name}} is a Stampede protocol plugin. Its one
// step, {{name}}.send, writes a line to a TCP server and times the reply
// line. Replace it with your protocol.
//
// pluginsdk.Serve implements the plugin contract from this description:
//
//   - Describe: Name, Version, Description and each step's Schema
//   - Open:     NewSession, once for each virtual user
//   - Execute:  a step's Run, for every step a user runs
//   - Close:    the session's Close method, when the user retires
package main

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net"
	"strings"
	"time"

	"github.com/Ivan825/Stampede/pkg/pluginsdk"
)

// version is the plugin's own version.
const version = "0.1.0"

// sendSchema describes the settings of {{name}}.send, the with: block of
// a scenario step. "x-stampede-target" marks the address, so Stampede
// applies its target policy to it.
const sendSchema = ` + "`" + `{
  "type": "object",
  "additionalProperties": false,
  "required": ["addr", "line"],
  "properties": {
    "addr": {"type": "string", "x-stampede-target": true, "description": "host:port of the server."},
    "line": {"type": "string", "description": "The line to send; a newline is added."}
  }
}` + "`" + `

func main() { pluginsdk.Serve(newPlugin()) }

func newPlugin() *pluginsdk.Plugin {
	return &pluginsdk.Plugin{
		Name:        "{{name}}",
		Version:     version,
		Description: "Sends a line to a TCP server and times the reply.",
		NewSession: func(context.Context, pluginsdk.SessionInfo) (any, error) {
			return &session{}, nil
		},
		Steps: []pluginsdk.Step{{
			Name:        "send",
			Description: "Write a line and wait for the reply line.",
			Schema:      sendSchema,
			Run:         send,
		}},
	}
}

// session is one virtual user's connection, opened by its first step and
// kept across iterations like a real client's.
type session struct {
	addr string
	conn net.Conn
	r    *bufio.Reader
}

// Close ends the session; the SDK calls it when the user retires.
func (s *session) Close() error {
	if s.conn == nil {
		return nil
	}
	err := s.conn.Close()
	s.conn, s.r = nil, nil
	return err
}

type sendConfig struct {
	Addr string ` + "`json:\"addr\"`" + `
	Line string ` + "`json:\"line\"`" + `
}

func send(ctx context.Context, c *pluginsdk.Call) (*pluginsdk.Result, error) {
	var cfg sendConfig
	if err := c.Decode(&cfg); err != nil {
		return nil, err
	}
	s := c.Session.(*session)
	start := time.Now()
	var connect time.Duration
	if s.conn == nil || s.addr != cfg.Addr {
		_ = s.Close()
		var d net.Dialer
		conn, err := d.DialContext(ctx, "tcp", cfg.Addr)
		if err != nil {
			return nil, classify(err)
		}
		s.addr, s.conn, s.r = cfg.Addr, conn, bufio.NewReader(conn)
		connect = time.Since(start)
	}
	if dl, ok := ctx.Deadline(); ok {
		_ = s.conn.SetDeadline(dl)
	}
	sent := time.Now()
	n, err := io.WriteString(s.conn, cfg.Line+"\n")
	if err != nil {
		_ = s.Close()
		return nil, classify(err)
	}
	reply, err := s.r.ReadString('\n')
	if err != nil {
		// The reply may still arrive; a new connection keeps it from
		// being read as the answer to the next line.
		_ = s.Close()
		return nil, classify(err)
	}
	return &pluginsdk.Result{
		Latency:  time.Since(start),
		Phases:   pluginsdk.Phases{Connect: connect, Wait: time.Since(sent)},
		BytesOut: int64(n),
		BytesIn:  int64(len(reply)),
		// Checks and extractors read these: extract: { reply: "$.reply" }.
		Values: map[string]any{"reply": strings.TrimRight(reply, "\r\n")},
	}, nil
}

// classify gives a failure a short, bounded error class for reports.
func classify(err error) error {
	var ne net.Error
	switch {
	case errors.As(err, &ne) && ne.Timeout():
		return pluginsdk.Fail("{{name}} timeout", err)
	case errors.Is(err, io.EOF):
		return pluginsdk.Fail("{{name}} closed", err)
	}
	return pluginsdk.Fail("{{name}} error", err)
}
`

const pluginTest = `package main

import (
	"bufio"
	"io"
	"net"
	"testing"

	"github.com/Ivan825/Stampede/pkg/pluginsdk/conformance"
)

// echoServer answers every line with "echo: " and the line.
func echoServer(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				r := bufio.NewReader(conn)
				for {
					line, err := r.ReadString('\n')
					if err != nil {
						return
					}
					if _, err := io.WriteString(conn, "echo: "+line); err != nil {
						return
					}
				}
			}()
		}
	}()
	return ln.Addr().String()
}

// TestConformance builds the plugin and checks it against Stampede's
// plugin contract: its description, the session lifecycle, clean errors
// for bad input, timeouts, and 50 users at once.
func TestConformance(t *testing.T) {
	addr := echoServer(t)
	conformance.Run(t, conformance.Build(t, ".", "{{name}}"), conformance.Options{
		Cases: []conformance.Case{
			{Step: "send", Config: map[string]any{"addr": addr, "line": "ping"}},
		},
	})
}
`

const pluginREADME = "# {{name}} plugin\n\n" +
	"A Stampede protocol plugin. Its step `{{name}}.send` writes a line to a\n" +
	"TCP server and times the reply line. Replace it with your protocol; see\n" +
	"[Writing a plugin](https://github.com/Ivan825/Stampede/blob/main/docs/plugins.md#writing-a-plugin).\n\n" +
	"## Build and install\n\n" +
	"```sh\n" +
	"go test ./...                  # builds the plugin and runs the conformance suite\n" +
	"stampede plugin install .      # builds it as stampede-plugin-{{name}} into the plugin directory\n" +
	"stampede plugin show {{name}}\n" +
	"```\n\n" +
	"Install it on every machine that runs scenarios using it.\n\n" +
	"## `{{name}}.send`\n\n" +
	"```yaml\n" +
	"- name: ping\n" +
	"  plugin: {{name}}.send\n" +
	"  with:\n" +
	"    addr: ${env.SERVER_ADDR}       # host:port; the target policy applies to it\n" +
	"    line: \"ping ${vu} ${iter}\"\n" +
	"  check: { maxLatency: 50ms, json: { \"$.reply\": exists } }\n" +
	"  extract: { reply: \"$.reply\" }\n" +
	"  timeout: 2s\n" +
	"```\n\n" +
	"Each virtual user keeps one connection. **Latency** is from the start of\n" +
	"the step (including connecting, the first time) to the reply line, with\n" +
	"`connect` and `wait` phases. **Returned values**: `reply`. **Errors**:\n" +
	"`{{name}} timeout`, `{{name}} closed`, `{{name}} error`, `invalid config`.\n"
