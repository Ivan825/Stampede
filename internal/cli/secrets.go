package cli

import (
	"errors"
	"fmt"
	"io"
	"net/url"

	"github.com/spf13/cobra"

	"github.com/Ivan825/Stampede/internal/api/gen"
)

func newSecretsCmd() *cobra.Command {
	var project string
	cmd := &cobra.Command{
		Use:     "secrets",
		Aliases: []string{"secret"},
		Short:   "List, set and delete a project's secrets",
		Long: `Secrets are values such as passwords and API keys that scenarios use as
${secret.NAME}. They are encrypted at rest with the server's master key,
never returned by the API and never printed here. Names are identifiers
(letters, digits and underscores).`,
	}
	cmd.PersistentFlags().StringVar(&project, "project", "", "project name, slug or id (default: the default or only project)")

	var asJSON bool
	list := &cobra.Command{
		Use:   "list",
		Short: "List a project's secret names (values are never shown)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, pid, err := projectClient(cmd.Context(), project)
			if err != nil {
				return err
			}
			var ss []gen.Secret
			if err := c.Do(cmd.Context(), "GET", "/projects/"+pid+"/secrets", nil, &ss); err != nil {
				return err
			}
			return show(cmd, asJSON, ss, func(w io.Writer) error {
				if len(ss) == 0 {
					_, err := fmt.Fprintln(w, "No secrets. Set one with stampede secrets set <NAME>.")
					return err
				}
				var rows [][]string
				for _, s := range ss {
					rows = append(rows, []string{s.Name, stamp(&s.UpdatedAt)})
				}
				return table(w, "NAME\tUPDATED", rows)
			})
		},
	}
	jsonFlag(list, &asJSON)

	var fromEnv string
	set := &cobra.Command{
		Use:   "set <NAME>",
		Short: "Create or replace a secret",
		Long: `Store a secret. The value is read from the environment variable named by
--from-env, or from stdin when it is piped, or asked without echo. It is
never a flag value and never printed, so it stays out of shell history and
logs.`,
		Example: `  stampede secrets set SHOP_PASSWORD                      # asks for the value
  stampede secrets set API_KEY --from-env STAGING_API_KEY
  vault read -field=key secret/shop | stampede secrets set API_KEY`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, pid, err := projectClient(cmd.Context(), project)
			if err != nil {
				return err
			}
			v, err := readValue(cmd, "Value for "+args[0]+": ", fromEnv)
			if err != nil {
				return err
			}
			if v == "" {
				return errors.New("the value is empty")
			}
			var s gen.Secret
			if err := c.Do(cmd.Context(), "PUT", "/projects/"+pid+"/secrets", map[string]string{"name": args[0], "value": v}, &s); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Saved secret %s (encrypted; use it as ${secret.%s}).\n", s.Name, s.Name)
			return nil
		},
	}
	set.Flags().StringVar(&fromEnv, "from-env", "", "read the value from this environment variable")

	del := &cobra.Command{
		Use:   "delete <NAME>",
		Short: "Delete a secret",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, pid, err := projectClient(cmd.Context(), project)
			if err != nil {
				return err
			}
			if err := c.Do(cmd.Context(), "DELETE", "/projects/"+pid+"/secrets/"+url.PathEscape(args[0]), nil, nil); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Deleted secret %s.\n", args[0])
			return nil
		},
	}
	cmd.AddCommand(list, set, del)
	return cmd
}
