// Package cli wires the stampede command tree.
package cli

import (
	"github.com/spf13/cobra"

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
	}
	root.AddCommand(newVersionCmd(), newRunCmd(), newValidateCmd(), newTargetCmd(), newServerCmd(), newKeygenCmd())
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
