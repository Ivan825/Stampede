package cli

import (
	"os"

	"github.com/spf13/cobra"
	"github.com/spf13/cobra/doc"
)

// newGenDocsCmd writes the CLI reference from the command definitions, so
// the docs cannot drift from the binary.
func newGenDocsCmd() *cobra.Command {
	return &cobra.Command{
		Use:    "gen-docs <dir>",
		Short:  "Write Markdown reference pages for every command",
		Hidden: true,
		Args:   cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := os.MkdirAll(args[0], 0o750); err != nil {
				return err
			}
			root := cmd.Root()
			root.DisableAutoGenTag = true
			return doc.GenMarkdownTree(root, args[0])
		},
	}
}
