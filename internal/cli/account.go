package cli

import (
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"

	"github.com/Ivan825/Stampede/internal/api/gen"
	"github.com/Ivan825/Stampede/internal/client"
)

func newSetupCmd() *cobra.Command {
	var server, org, name, email string
	cmd := &cobra.Command{
		Use:   "setup",
		Short: "Create the first organisation and owner account on a new server",
		Long: `Set up a server that has no accounts yet: create the organisation and its
owner, then sign in as the owner and store an API token for this CLI, as
stampede login does. Questions you leave out as flags are asked. The
password is read from STAMPEDE_PASSWORD or asked without echo; it is
never a flag, so it stays out of shell history. It needs at least ten
characters.`,
		Example: `  stampede setup --server http://localhost:8080 --organisation Acme --name "Ada Lovelace" --email ada@acme.test
  STAMPEDE_PASSWORD=... stampede setup --organisation Acme --name Ada --email ada@acme.test`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			p := newPrompter(cmd)
			if server == "" {
				server = "http://localhost:8080"
			}
			if org == "" {
				org = p.line("Organisation: ")
			}
			if name == "" {
				name = p.line("Your name: ")
			}
			if email == "" {
				email = p.line("Email: ")
			}
			pw, err := p.newSecret("Password (at least 10 characters): ", "STAMPEDE_PASSWORD")
			if err != nil {
				return err
			}
			host, _ := os.Hostname()
			tok, me, err := client.Setup(cmd.Context(), server, org, name, email, pw, "cli on "+host)
			if err != nil {
				return err
			}
			path, err := saveLogin(server, tok)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "Created %s with owner %s on %s. Token saved in %s\n", me.OrgName, me.Email, server, path)
			fmt.Fprintln(out, "Next: stampede projects create <name>, then stampede targets create.")
			return nil
		},
	}
	fl := cmd.Flags()
	fl.StringVar(&server, "server", os.Getenv("STAMPEDE_SERVER"), "server URL (default http://localhost:8080)")
	fl.StringVar(&org, "organisation", "", "organisation name")
	fl.StringVar(&name, "name", "", "your name")
	fl.StringVar(&email, "email", "", "your email, used to sign in")
	return cmd
}

// saveLogin stores the server and token, keeping the default project
// when the server is the same.
func saveLogin(server, token string) (string, error) {
	cfg, err := client.ReadConfigFile()
	if err != nil {
		return "", err
	}
	if cfg.Server != server {
		cfg.Project = ""
	}
	cfg.Server, cfg.Token = server, token
	return client.SaveConfig(cfg)
}

func newLogoutCmd() *cobra.Command {
	var keep bool
	cmd := &cobra.Command{
		Use:   "logout",
		Short: "Revoke this CLI's API token and forget it",
		Long: `Revoke the API token stampede login stored, on the server, and remove it
from the config file. With --keep-token the token is only forgotten here
and stays valid. A token set in STAMPEDE_TOKEN is revoked as well; unset
the variable afterwards.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			out := cmd.OutOrStdout()
			if !keep {
				c, err := client.New()
				if err != nil {
					return err
				}
				if err := revokeOwnToken(cmd, c); err != nil {
					return err
				}
				fmt.Fprintf(out, "Revoked the token on %s.\n", c.Server)
			}
			stored, err := client.ReadConfigFile()
			if err != nil {
				return err
			}
			if stored.Token != "" {
				stored.Token = ""
				path, err := client.SaveConfig(stored)
				if err != nil {
					return err
				}
				fmt.Fprintf(out, "Removed the token from %s.\n", path)
			} else if keep {
				return errors.New("no token is stored in the config file")
			}
			if os.Getenv("STAMPEDE_TOKEN") != "" {
				fmt.Fprintln(out, "STAMPEDE_TOKEN is still set in your environment; unset it.")
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&keep, "keep-token", false, "only forget the token here; it stays valid on the server")
	return cmd
}

// revokeOwnToken deletes the token c signs in with.
func revokeOwnToken(cmd *cobra.Command, c *client.Client) error {
	if len(c.Token) < 12 {
		return errors.New("the stored token is malformed; remove it from the config file")
	}
	t, err := c.FindToken(cmd.Context(), c.Token[:12])
	if err != nil {
		return err
	}
	return c.Do(cmd.Context(), "DELETE", "/tokens/"+t.Id.String(), nil, nil)
}

func newWhoamiCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "whoami",
		Short: "Show who you are signed in as, and where",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := client.New()
			if err != nil {
				return err
			}
			var me gen.Me
			if err := c.Do(cmd.Context(), "GET", "/me", nil, &me); err != nil {
				return err
			}
			v := struct {
				Server         string `json:"server"`
				DefaultProject string `json:"defaultProject,omitempty"`
				gen.Me
			}{c.Server, c.Project, me}
			return show(cmd, asJSON, v, func(w io.Writer) error {
				fmt.Fprintf(w, "%s (%s), %s of %s on %s\n", me.Email, me.Name, me.Role, me.OrgName, c.Server)
				if c.Project != "" {
					fmt.Fprintf(w, "Default project: %s\n", c.Project)
				}
				return nil
			})
		},
	}
	jsonFlag(cmd, &asJSON)
	return cmd
}

func newPasswordCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "password",
		Short: "Change your password",
		Long: `Change the password you sign in with. Asks for the current and the new
password without echo, or reads them from STAMPEDE_PASSWORD and
STAMPEDE_NEW_PASSWORD. Your other browser sessions are signed out; API
tokens keep working.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := client.New()
			if err != nil {
				return err
			}
			p := newPrompter(cmd)
			cur, err := p.secret("Current password: ", "STAMPEDE_PASSWORD")
			if err != nil {
				return err
			}
			next, err := p.newSecret("New password (at least 10 characters): ", "STAMPEDE_NEW_PASSWORD")
			if err != nil {
				return err
			}
			if err := c.Do(cmd.Context(), "PUT", "/me/password", map[string]string{"current": cur, "new": next}, nil); err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), "Password changed. Other sessions were signed out.")
			return nil
		},
	}
}
