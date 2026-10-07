// Package cli wires the stampede command tree.
package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/Ivan825/Stampede/internal/api/gen"
	"github.com/Ivan825/Stampede/internal/client"
	"github.com/Ivan825/Stampede/internal/safety"
	"github.com/Ivan825/Stampede/internal/scenario"
	"github.com/Ivan825/Stampede/internal/tui"

	"github.com/Ivan825/Stampede/internal/version"
)

// NewRoot builds the root command with every subcommand attached.
func NewRoot() *cobra.Command {
	var resumeLast bool
	var resumeID string
	root := &cobra.Command{
		Use:   "stampede",
		Short: "Self-hosted, distributed load testing",
		Long: `Stampede describes how your users behave, validates each journey with a
single user, then runs thousands of them across distributed workers and
reports where your product breaks.`,
		SilenceUsage:  true,
		SilenceErrors: true,
		// With no subcommand in a terminal, open the interactive console.
		RunE: func(cmd *cobra.Command, _ []string) error {
			if !term.IsTerminal(int(os.Stdin.Fd())) || !term.IsTerminal(int(os.Stdout.Fd())) {
				return cmd.Help()
			}
			c, err := client.New()
			if err != nil {
				c = nil // local mode: /run works on files
			}
			opts := consoleOptions()
			opts.Commands = consoleCommands(cmd.Root())
			opts.Self, _ = os.Executable()
			if opts.Store, err = tui.OpenStore(); err != nil {
				fmt.Fprintln(cmd.ErrOrStderr(), "stampede: sessions will not be saved:", err)
				opts.Store = nil
			}
			if resumeLast || resumeID != "" {
				if opts.Store == nil {
					return errors.New("no saved sessions to resume")
				}
				if opts.Resume, err = opts.Store.Load(resumeID); err != nil {
					return err
				}
			}
			return tui.Run(c, opts)
		},
	}
	root.Flags().BoolVarP(&resumeLast, "continue", "c", false, "open the console in your last session")
	root.Flags().StringVar(&resumeID, "resume", "", "open the console in a saved session (its id, or the start of it)")
	root.AddCommand(newVersionCmd(), newRunCmd(), newValidateCmd(), newTargetsCmd(), newServerCmd(), newWorkerCmd(), newKeygenCmd(), newHealthcheckCmd(), newCompareCmd(), newReportCmd(),
		newLoginCmd(), newStartCmd(), newPushCmd(), newRunsCmd(), newSchedulesCmd(), newStopCmd(false), newStopCmd(true), newWorkersCmd(),
		newUpCmd(), newDownCmd(), newDoctorCmd(), newPackCmd(), newInitCmd(), newPluginCmd())
	root.AddCommand(newGenerateCmd(), newGenDocsCmd(), newAgentCmd(), newCoverageCmd(), newDriftCmd(), newBackupCmd(), newRestoreCmd())
	root.AddCommand(newSetupCmd(), newLogoutCmd(), newWhoamiCmd(), newPasswordCmd(),
		newUsersCmd(), newTokensCmd(), newProjectsCmd(), newCapsCmd(), newSecretsCmd(), newScenariosCmd(),
		newAICmd(), newNarrativeCmd(), newIntegrationsCmd(), newNotifyCmd(), newAuditCmd(), newSettingsCmd())
	root.AddCommand(newConsoleExecCmd())
	return root
}

// consoleCommands lists the subcommands the console runs for /name: those
// that ask for a password or print a secret get the terminal, and servers
// and workers, which run until stopped, are left to another terminal.
func consoleCommands(root *cobra.Command) map[string]tui.Kind {
	kinds := map[string]tui.Kind{}
	for _, c := range root.Commands() {
		if c.Hidden || c.Name() == "help" || c.Name() == "completion" {
			continue
		}
		k := tui.Inline
		switch c.Name() {
		case "setup", "login", "password", "users", "secrets", "tokens", "ai", "keygen":
			k = tui.Terminal
		case "server", "worker", "agent":
			k = tui.Daemon
		}
		for _, n := range append([]string{c.Name()}, c.Aliases...) {
			kinds[n] = k
		}
	}
	return kinds
}

// newConsoleExecCmd runs a command for the console with the terminal, then
// waits for Enter so its output can be read before the console returns.
func newConsoleExecCmd() *cobra.Command {
	return &cobra.Command{
		Use:                tui.ExecArg + " -- <command> [args...]",
		Hidden:             true,
		DisableFlagParsing: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 && args[0] == "--" {
				args = args[1:]
			}
			if len(args) == 0 {
				return errors.New("no command given")
			}
			sub := NewRoot()
			sub.SetArgs(args)
			err := sub.ExecuteContext(cmd.Context())
			if err != nil {
				fmt.Fprintln(os.Stderr, "error:", err)
			}
			fmt.Fprint(os.Stderr, "\nPress Enter to return to the console…")
			_, _ = bufio.NewReader(os.Stdin).ReadString('\n')
			if err != nil {
				os.Exit(ExitCode(err))
			}
			return nil
		},
	}
}

// consoleOptions give the terminal console the CLI's init flow and the
// safety rules of stampede run.
func consoleOptions() tui.Options {
	return tui.Options{
		Init: func(ctx context.Context, w io.Writer, target, dir string, env []string) error {
			// Typing /init is the confirmation.
			return runInit(ctx, strings.NewReader(""), w, initOptions{target: target, dir: dir, env: env, yes: true})
		},
		CheckTarget: func(ctx context.Context, w io.Writer, s *scenario.Scenario, env map[string]string) (func(*url.URL) bool, error) {
			_, secrets, err := runEnv(nil)
			if err != nil {
				return nil, err
			}
			base, err := renderBaseURL(s, env, secrets)
			if err != nil {
				return nil, err
			}
			if base == "" {
				return safety.NewHostPolicy("", nil).Allow, nil
			}
			plan, err := s.Load.Plan()
			if err != nil {
				return nil, err
			}
			return checkTarget(ctx, w, base, plan, nil)
		},
	}
}

func newVersionCmd() *cobra.Command {
	var server bool
	cmd := &cobra.Command{
		Use:   "version",
		Short: "Print version information",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			fmt.Fprintln(cmd.OutOrStdout(), version.String())
			if !server {
				return nil
			}
			c, err := client.New()
			if err != nil {
				return err
			}
			var v gen.VersionInfo
			if err := c.Do(cmd.Context(), "GET", "/version", nil, &v); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "server %s: %s (%s)\n", c.Server, v.Version, v.Commit)
			return nil
		},
	}
	cmd.Flags().BoolVar(&server, "server", false, "also print the version of the server you signed in to")
	return cmd
}
