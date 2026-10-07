package cli

import (
	"fmt"
	"io"
	"time"

	"github.com/spf13/cobra"

	"github.com/Ivan825/Stampede/internal/api/gen"
	"github.com/Ivan825/Stampede/internal/client"
)

const roleHelp = "owner, admin, editor, runner or viewer"

func newUsersCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "users",
		Aliases: []string{"user"},
		Short:   "List and manage the organisation's members and their roles",
		Long: `Members sign in with an email and password (or single sign-on) and have one
role in the organisation: owner, admin, editor, runner or viewer. Admins
add, change and remove members; only owners grant or remove the owner
role, and the last owner cannot be removed. A member's role can differ in
one project with stampede projects roles. Users are named by email or id.`,
	}
	cmd.AddCommand(newUsersListCmd(), newUsersCreateCmd(), newUsersUpdateCmd(), newUsersDeleteCmd())
	return cmd
}

func newUsersListCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List members of the organisation",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := client.New()
			if err != nil {
				return err
			}
			var us []gen.User
			if err := c.Do(cmd.Context(), "GET", "/users", nil, &us); err != nil {
				return err
			}
			return show(cmd, asJSON, us, func(w io.Writer) error {
				var rows [][]string
				for _, u := range us {
					rows = append(rows, []string{u.Email, u.Name, string(u.Role), stamp(u.LastLoginAt), u.Id.String()})
				}
				return table(w, "EMAIL\tNAME\tROLE\tLAST SIGN-IN\tID", rows)
			})
		},
	}
	jsonFlag(cmd, &asJSON)
	return cmd
}

func newUsersCreateCmd() *cobra.Command {
	var name, role string
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "create <email>",
		Short: "Add a member with a password (admin)",
		Long: `Add a member to the organisation. Their first password is read from
STAMPEDE_NEW_PASSWORD or asked without echo; share it with them and ask
them to change it with stampede password.`,
		Example: `  stampede users create pat@acme.test --name "Pat Lee" --role editor`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := client.New()
			if err != nil {
				return err
			}
			if name == "" {
				return fmt.Errorf("--name is required")
			}
			pw, err := newPrompter(cmd).newSecret("Password for "+args[0]+" (at least 10 characters): ", "STAMPEDE_NEW_PASSWORD")
			if err != nil {
				return err
			}
			var u gen.User
			body := map[string]string{"email": args[0], "name": name, "role": role, "password": pw}
			if err := c.Do(cmd.Context(), "POST", "/users", body, &u); err != nil {
				return err
			}
			return show(cmd, asJSON, u, func(w io.Writer) error {
				_, err := fmt.Fprintf(w, "Added %s (%s) as %s.\n", u.Email, u.Name, u.Role)
				return err
			})
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "the member's name (required)")
	cmd.Flags().StringVar(&role, "role", "viewer", roleHelp)
	jsonFlag(cmd, &asJSON)
	return cmd
}

func newUsersUpdateCmd() *cobra.Command {
	var name, role string
	var asJSON bool
	cmd := &cobra.Command{
		Use:     "update <user>",
		Short:   "Rename a member or change their role",
		Example: `  stampede users update pat@acme.test --role admin`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := client.New()
			if err != nil {
				return err
			}
			body := map[string]string{}
			if cmd.Flags().Changed("name") {
				body["name"] = name
			}
			if cmd.Flags().Changed("role") {
				body["role"] = role
			}
			if len(body) == 0 {
				return fmt.Errorf("nothing to change: give --name or --role")
			}
			u, err := c.FindUser(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			if err := c.Do(cmd.Context(), "PATCH", "/users/"+u.Id.String(), body, &u); err != nil {
				return err
			}
			return show(cmd, asJSON, u, func(w io.Writer) error {
				_, err := fmt.Fprintf(w, "Updated %s: %s, %s.\n", u.Email, u.Name, u.Role)
				return err
			})
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "new name")
	cmd.Flags().StringVar(&role, "role", "", "new role: "+roleHelp)
	jsonFlag(cmd, &asJSON)
	return cmd
}

func newUsersDeleteCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "delete <user>",
		Short: "Remove a member from the organisation (admin)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := client.New()
			if err != nil {
				return err
			}
			u, err := c.FindUser(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			if err := c.Do(cmd.Context(), "DELETE", "/users/"+u.Id.String(), nil, nil); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Removed %s.\n", u.Email)
			return nil
		},
	}
}

func newTokensCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "tokens",
		Aliases: []string{"token"},
		Short:   "List, create and revoke your API tokens",
		Long: `API tokens let scripts and CI use the API as you, with your role or a
lower one: set STAMPEDE_SERVER and STAMPEDE_TOKEN. A token's secret is
shown once, when it is created, and never again. Tokens are named by
name, id or prefix.`,
	}
	cmd.AddCommand(newTokensListCmd(), newTokensCreateCmd(), newTokensDeleteCmd())
	return cmd
}

func newTokensListCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List your API tokens (secrets are never shown)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := client.New()
			if err != nil {
				return err
			}
			var ts []gen.Token
			if err := c.Do(cmd.Context(), "GET", "/tokens", nil, &ts); err != nil {
				return err
			}
			return show(cmd, asJSON, ts, func(w io.Writer) error {
				var rows [][]string
				for _, t := range ts {
					this := ""
					if len(c.Token) >= 12 && c.Token[:12] == t.Prefix {
						this = " (this CLI)"
					}
					rows = append(rows, []string{t.Name + this, t.Prefix + "…", string(t.Role), stamp(&t.CreatedAt), stamp(t.LastUsedAt), stamp(t.ExpiresAt)})
				}
				return table(w, "NAME\tPREFIX\tROLE\tCREATED\tLAST USED\tEXPIRES", rows)
			})
		},
	}
	jsonFlag(cmd, &asJSON)
	return cmd
}

func newTokensCreateCmd() *cobra.Command {
	var role string
	var days int
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "create <name>",
		Short: "Create an API token and print its secret once",
		Long: `Create an API token. API tokens cannot create other tokens, so this signs
in with your password (STAMPEDE_PASSWORD, or asked without echo). The
secret is printed once on stdout; store it now, it cannot be shown again.`,
		Example: `  stampede tokens create ci --role runner --expires-in-days 90
  export STAMPEDE_TOKEN=$(stampede tokens create nightly --role runner)`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := client.New()
			if err != nil {
				return err
			}
			var me gen.Me
			if err := c.Do(cmd.Context(), "GET", "/me", nil, &me); err != nil {
				return err
			}
			pw, err := newPrompter(cmd).secret("Password for "+me.Email+": ", "STAMPEDE_PASSWORD")
			if err != nil {
				return err
			}
			body := map[string]any{"name": args[0]}
			if role != "" {
				body["role"] = role
			}
			if days > 0 {
				body["expiresInDays"] = days
			}
			tok, _, err := client.PasswordToken(cmd.Context(), c.Server, me.Email, pw, body)
			if err != nil {
				return err
			}
			if asJSON {
				return writeJSON(cmd.OutOrStdout(), tok)
			}
			exp := "never expires"
			if tok.ExpiresAt != nil {
				exp = "expires " + tok.ExpiresAt.Local().Format(time.RFC1123)
			}
			fmt.Fprintf(cmd.ErrOrStderr(), "Created token %s (%s, %s). Its secret is shown once:\n", tok.Name, tok.Role, exp)
			fmt.Fprintln(cmd.OutOrStdout(), tok.Secret)
			return nil
		},
	}
	cmd.Flags().StringVar(&role, "role", "", "the token's role, at most yours (default yours): "+roleHelp)
	cmd.Flags().IntVar(&days, "expires-in-days", 0, "expire the token after this many days (default never)")
	jsonFlag(cmd, &asJSON)
	return cmd
}

func newTokensDeleteCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "delete <token>",
		Aliases: []string{"revoke"},
		Short:   "Revoke an API token",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := client.New()
			if err != nil {
				return err
			}
			t, err := c.FindToken(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			if err := c.Do(cmd.Context(), "DELETE", "/tokens/"+t.Id.String(), nil, nil); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Revoked %s (%s…).\n", t.Name, t.Prefix)
			return nil
		},
	}
}
