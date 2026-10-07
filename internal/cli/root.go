// Package cli wires the stampede command tree.
package cli

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/Ivan825/Stampede/internal/client"
	"github.com/Ivan825/Stampede/internal/safety"
	"github.com/Ivan825/Stampede/internal/scenario"
	"github.com/Ivan825/Stampede/internal/tui"

	"github.com/Ivan825/Stampede/internal/version"
)

// NewRoot builds the root command with every subcommand attached.
func NewRoot() *cobra.Command {
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
			return tui.Run(c, consoleOptions())
		},
	}
	root.AddCommand(newVersionCmd(), newRunCmd(), newValidateCmd(), newTargetsCmd(), newServerCmd(), newWorkerCmd(), newKeygenCmd(), newHealthcheckCmd(), newCompareCmd(), newReportCmd(),
		newLoginCmd(), newStartCmd(), newPushCmd(), newRunsCmd(), newSchedulesCmd(), newStopCmd(false), newStopCmd(true), newWorkersCmd(),
		newUpCmd(), newDownCmd(), newDoctorCmd(), newPackCmd(), newInitCmd(), newPluginCmd())
	root.AddCommand(newGenerateCmd(), newGenDocsCmd(), newAgentCmd(), newCoverageCmd(), newDriftCmd(), newBackupCmd(), newRestoreCmd())
	root.AddCommand(newSetupCmd(), newLogoutCmd(), newWhoamiCmd(), newPasswordCmd(),
		newUsersCmd(), newTokensCmd(), newProjectsCmd(), newCapsCmd(), newSecretsCmd(), newScenariosCmd(),
		newAICmd(), newNarrativeCmd(), newIntegrationsCmd(), newNotifyCmd(), newAuditCmd(), newSettingsCmd())
	return root
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
	return &cobra.Command{
		Use:   "version",
		Short: "Print version information",
		Args:  cobra.NoArgs,
		Run: func(cmd *cobra.Command, _ []string) {
			fmt.Fprintln(cmd.OutOrStdout(), version.String())
		},
	}
}
