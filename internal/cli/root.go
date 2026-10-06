// Package cli wires the stampede command tree.
package cli

import (
	"os"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/Ivan825/Stampede/internal/client"
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
			return tui.Run(c)
		},
	}
	root.AddCommand(newVersionCmd(), newRunCmd(), newValidateCmd(), newTargetCmd(), newServerCmd(), newWorkerCmd(), newKeygenCmd(), newHealthcheckCmd(), newCompareCmd(), newReportCmd(),
		newLoginCmd(), newStartCmd(), newPushCmd(), newRunsCmd(), newStopCmd(false), newStopCmd(true), newWorkersCmd(),
		newUpCmd(), newDownCmd(), newDoctorCmd(), newPackCmd(), newInitCmd())
	root.AddCommand(newGenerateCmd(), newGenDocsCmd())
	return root
}

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print version information",
		Args:  cobra.NoArgs,
		Run: func(cmd *cobra.Command, _ []string) {
			cmd.Println(version.String())
		},
	}
}
