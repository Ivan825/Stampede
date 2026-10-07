package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/Ivan825/Stampede/internal/pluginhost"
	"github.com/Ivan825/Stampede/internal/scenario"
)

func newPluginCmd() *cobra.Command {
	var dir string
	cmd := &cobra.Command{
		Use:   "plugin",
		Short: "List, install, remove and create protocol plugins",
		Long: `Plugins add step types such as mqtt.publish or kafka.produce. Each is an
executable named stampede-plugin-<name>, looked for in the plugin directory
($STAMPEDE_PLUGIN_DIR, or plugins/ under your user config directory) and
then on PATH. Workers find plugins the same way: install a plugin on every
worker that runs scenarios using it.`,
	}
	cmd.PersistentFlags().StringVar(&dir, "dir", "", "plugin directory (default $STAMPEDE_PLUGIN_DIR or the user config directory)")

	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	cmd.AddCommand(newPluginCreateCmd())
	cmd.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "List installed plugins and the steps they offer",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			list, err := pluginhost.List(dir)
			if err != nil {
				return err
			}
			if len(list) == 0 {
				d, _ := pluginhost.Dir()
				if dir != "" {
					d = dir
				}
				fmt.Fprintf(cmd.OutOrStdout(), "No plugins installed in %s or on PATH.\nInstall one with `stampede plugin install mqtt` (or kafka, redis, sql, udp).\n", d)
				return nil
			}
			tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
			fmt.Fprintln(tw, "PLUGIN\tVERSION\tSTEPS\tPATH")
			for _, in := range list {
				ctx, cancel := context.WithTimeout(cmd.Context(), 20*time.Second)
				p, err := pluginhost.Start(ctx, in.Path, quiet)
				cancel()
				if err != nil {
					fmt.Fprintf(tw, "%s\t-\terror: %s\t%s\n", in.Name, oneLine(err.Error()), in.Path)
					continue
				}
				p.Kill()
				steps := make([]string, 0, len(p.Steps))
				for _, s := range p.StepNames() {
					steps = append(steps, p.Name+"."+s)
				}
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", in.Name, p.Version, strings.Join(steps, ", "), in.Path)
			}
			return tw.Flush()
		},
	})

	cmd.AddCommand(&cobra.Command{
		Use:   "show <name>",
		Short: "Show a plugin's steps and the settings each takes",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			path, err := pluginhost.Find(dir, args[0])
			if err != nil {
				return err
			}
			p, err := pluginhost.Start(cmd.Context(), path, quiet)
			if err != nil {
				return err
			}
			defer p.Kill()
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "%s %s (%s)\n", p.Name, p.Version, path)
			if p.Description != "" {
				fmt.Fprintf(out, "%s\n", p.Description)
			}
			for _, name := range p.StepNames() {
				st := p.Steps[name]
				fmt.Fprintf(out, "\n%s.%s", p.Name, name)
				if st.Description != "" {
					fmt.Fprintf(out, ": %s", st.Description)
				}
				fmt.Fprintln(out)
				if len(st.Targets) > 0 {
					fmt.Fprintf(out, "  target policy applies to: %s\n", strings.Join(st.Targets, ", "))
				}
				var b bytes.Buffer
				if json.Indent(&b, st.Schema, "  ", "  ") == nil {
					fmt.Fprintf(out, "  with (JSON Schema):\n  %s\n", b.String())
				}
			}
			return nil
		},
	})

	var source, ref string
	install := &cobra.Command{
		Use:   "install <name | directory | file | go-package[@version]>",
		Short: "Build and install a plugin",
		Long: `Installs a plugin into the plugin directory as stampede-plugin-<name>:

  stampede plugin install mqtt                 a first-party plugin (mqtt, kafka, redis, sql, udp),
                                               built from plugins/<name> of a Stampede checkout
  stampede plugin install ./my-plugin          a directory with a plugin's main package (go build)
  stampede plugin install ./stampede-plugin-x  a built plugin executable (copied)
  stampede plugin install example.com/x@v1.2.0 a Go package (go install)

Building needs a Go toolchain. A first-party plugin is built from the
checkout given by --source or $STAMPEDE_SOURCE, or the one containing the
current directory; with none, the repository is cloned (git is needed).
The plugin is started and must describe itself correctly before it is
installed.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Fprintf(cmd.ErrOrStderr(), "Building %s...\n", args[0])
			p, path, err := pluginhost.Install(cmd.Context(), args[0], pluginhost.InstallOptions{
				Dir: dir, Source: source, Ref: ref, Output: cmd.ErrOrStderr(), Log: quiet,
			})
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Installed %s %s at %s\nSteps: %s\n", p.Name, p.Version, path, strings.Join(prefixed(p.Name, p.StepNames()), ", "))
			return nil
		},
	}
	install.Flags().StringVar(&source, "source", "", "Stampede source checkout to build first-party plugins from")
	install.Flags().StringVar(&ref, "ref", "", "branch or tag to clone when there is no checkout")
	cmd.AddCommand(install)

	cmd.AddCommand(&cobra.Command{
		Use:   "remove <name>",
		Short: "Remove an installed plugin",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			path, err := pluginhost.Remove(dir, args[0])
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Removed %s\n", path)
			return nil
		},
	})
	return cmd
}

func prefixed(p string, names []string) []string {
	out := make([]string, len(names))
	for i, n := range names {
		out[i] = p + "." + n
	}
	return out
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

// checkPluginSteps checks a scenario's plugin steps against the installed
// plugins. Plugins that are not installed here are reported in skipped:
// their steps' settings could not be checked.
func checkPluginSteps(ctx context.Context, s *scenario.Scenario) (skipped []string, err error) {
	prog, err := scenario.Compile(s)
	if err != nil {
		return nil, err
	}
	var have []string
	for _, name := range prog.PluginNames() {
		if _, err := pluginhost.Find("", name); err != nil {
			skipped = append(skipped, name)
			continue
		}
		have = append(have, name)
	}
	if len(have) == 0 {
		return skipped, nil
	}
	set, err := pluginhost.Load(ctx, "", have, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		return skipped, err
	}
	defer set.Kill()
	// Steps of plugins that are not installed are left out of the check.
	sub := *prog
	sub.Steps = nil
	for _, st := range prog.Steps {
		if st.Plugin == nil || set[st.Plugin.Plugin] != nil {
			sub.Steps = append(sub.Steps, st)
		}
	}
	return skipped, pluginhost.CheckProgram(&sub, set)
}
