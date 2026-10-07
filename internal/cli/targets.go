package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/Ivan825/Stampede/internal/api/gen"
	"github.com/Ivan825/Stampede/internal/client"
	"github.com/Ivan825/Stampede/internal/safety"
)

func newTargetsCmd() *cobra.Command {
	var project string
	cmd := &cobra.Command{
		Use:     "targets",
		Aliases: []string{"target"},
		Short:   "List and manage a project's targets, and prove you own them",
		Long: `A target is the system a project's runs send load to: a base URL, the
other hosts its requests may reach, and optional caps. Private and
loopback targets need no verification; public ones run under low caps
until you prove you own them (stampede targets verify). Targets are
named by name, base URL or id.`,
	}
	cmd.PersistentFlags().StringVar(&project, "project", "", "project name, slug or id (default: the default or only project)")
	cmd.AddCommand(newTargetsListCmd(&project), newTargetsCreateCmd(&project), newTargetsShowCmd(&project),
		newTargetsUpdateCmd(&project), newTargetsDeleteCmd(&project), newTargetsVerifyCmd(&project))
	return cmd
}

func verification(t gen.Target) string {
	switch {
	case t.Private:
		return "private"
	case t.Verified:
		return "verified"
	default:
		return "unverified"
	}
}

func newTargetsListCmd(project *string) *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List a project's targets",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, pid, err := projectClient(cmd.Context(), *project)
			if err != nil {
				return err
			}
			var ts []gen.Target
			if err := c.Do(cmd.Context(), "GET", "/projects/"+pid+"/targets", nil, &ts); err != nil {
				return err
			}
			return show(cmd, asJSON, ts, func(w io.Writer) error {
				if len(ts) == 0 {
					_, err := fmt.Fprintln(w, "No targets. Add one with stampede targets create <name> --base-url <url>.")
					return err
				}
				var rows [][]string
				for _, t := range ts {
					allow := "-"
					if t.AllowHosts != nil && len(*t.AllowHosts) > 0 {
						allow = strings.Join(*t.AllowHosts, ",")
					}
					rows = append(rows, []string{t.Name, t.BaseURL, verification(t), allow, capsText(t.Caps)})
				}
				return table(w, "NAME\tBASE URL\tOWNERSHIP\tALSO ALLOWS\tCAPS", rows)
			})
		},
	}
	jsonFlag(cmd, &asJSON)
	return cmd
}

func newTargetsCreateCmd(project *string) *cobra.Command {
	var baseURL string
	var allow []string
	var caps capsFlags
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "create <name>",
		Short: "Add a target to a project",
		Example: `  stampede targets create staging --base-url https://staging.example.com --allow-host cdn.example.com
  stampede targets create local --base-url http://localhost:8090 --max-rate 500`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if baseURL == "" {
				return errors.New("--base-url is required, e.g. --base-url https://staging.example.com")
			}
			c, pid, err := projectClient(cmd.Context(), *project)
			if err != nil {
				return err
			}
			body := gen.TargetCreate{Name: args[0], BaseURL: baseURL, Caps: &gen.Caps{}}
			if len(allow) > 0 {
				body.AllowHosts = &allow
			}
			if err := caps.apply(cmd.Flags(), body.Caps); err != nil {
				return err
			}
			var t gen.Target
			if err := c.Do(cmd.Context(), "POST", "/projects/"+pid+"/targets", body, &t); err != nil {
				return err
			}
			return show(cmd, asJSON, t, func(w io.Writer) error {
				fmt.Fprintf(w, "Created target %s (%s, %s).\n", t.Name, t.BaseURL, verification(t))
				if !t.Private && !t.Verified {
					writeVerifyHelp(w, t)
				}
				return nil
			})
		},
	}
	cmd.Flags().StringVar(&baseURL, "base-url", "", "the target's base URL (required)")
	cmd.Flags().StringSliceVar(&allow, "allow-host", nil, "another host the target's requests may reach (repeatable)")
	caps.register(cmd.Flags())
	jsonFlag(cmd, &asJSON)
	return cmd
}

// writeVerifyHelp tells how to prove ownership of a public target.
func writeVerifyHelp(w io.Writer, t gen.Target) {
	u, _ := url.Parse(t.BaseURL)
	host, scheme := t.BaseURL, "https"
	if u != nil {
		host, scheme = u.Host, u.Scheme
	}
	name := host
	if u != nil {
		name = u.Hostname()
	}
	fmt.Fprintf(w, "Public targets run under low caps until you prove you own them. Publish one of these, then run stampede targets verify %s:\n", t.Name)
	fmt.Fprintf(w, "  DNS TXT on %s or _stampede.%s:  %s%s\n", name, name, safety.TXTPrefix, t.VerificationToken)
	fmt.Fprintf(w, "  or a file at %s://%s%s containing:  %s\n", scheme, host, safety.WellKnownPath, t.VerificationToken)
}

func newTargetsShowCmd(project *string) *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "show <target>",
		Short: "Show a target, and how to verify it when it is public",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, pid, err := projectClient(cmd.Context(), *project)
			if err != nil {
				return err
			}
			t, err := c.FindTarget(cmd.Context(), pid, args[0])
			if err != nil {
				return err
			}
			return show(cmd, asJSON, t, func(w io.Writer) error {
				fmt.Fprintf(w, "%s  %s (id %s)\n", t.Name, t.BaseURL, t.Id)
				switch {
				case t.Private:
					fmt.Fprintln(w, "Ownership:    private address, no verification needed")
				case t.Verified:
					fmt.Fprintf(w, "Ownership:    verified by %s on %s\n", or(t.VerificationMethod, "?"), stamp(t.VerifiedAt))
				default:
					fmt.Fprintln(w, "Ownership:    unverified, so low caps apply")
				}
				allow := "none"
				if t.AllowHosts != nil && len(*t.AllowHosts) > 0 {
					allow = strings.Join(*t.AllowHosts, ", ")
				}
				fmt.Fprintf(w, "Also allows:  %s\n", allow)
				fmt.Fprintf(w, "Caps:         %s\n", capsText(t.Caps))
				fmt.Fprintf(w, "Created:      %s\n", t.CreatedAt.Local().Format(time.RFC1123))
				if !t.Private && !t.Verified {
					writeVerifyHelp(w, t)
				}
				return nil
			})
		},
	}
	jsonFlag(cmd, &asJSON)
	return cmd
}

func newTargetsUpdateCmd(project *string) *cobra.Command {
	var name, baseURL string
	var allow []string
	var caps capsFlags
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "update <target>",
		Short: "Change a target's name, base URL, allowed hosts or caps",
		Long: `Change a target. Only the flags given change; --allow-host replaces the
list of allowed hosts (--allow-host "" empties it), and a cap of 0 removes
that cap. Changing the base URL's host clears its verification.`,
		Example: `  stampede targets update staging --max-rate 200 --allow-host cdn.example.com --allow-host auth.example.com`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			fl := cmd.Flags()
			if !fl.Changed("name") && !fl.Changed("base-url") && !fl.Changed("allow-host") && !caps.changed(fl) {
				return errors.New("nothing to change: give --name, --base-url, --allow-host or a cap")
			}
			c, pid, err := projectClient(cmd.Context(), *project)
			if err != nil {
				return err
			}
			t, err := c.FindTarget(cmd.Context(), pid, args[0])
			if err != nil {
				return err
			}
			body := gen.TargetCreate{Name: t.Name, BaseURL: t.BaseURL, AllowHosts: t.AllowHosts, Caps: &t.Caps}
			if fl.Changed("name") {
				body.Name = name
			}
			if fl.Changed("base-url") {
				body.BaseURL = baseURL
			}
			if fl.Changed("allow-host") {
				hosts := []string{}
				for _, h := range allow {
					if h != "" {
						hosts = append(hosts, h)
					}
				}
				body.AllowHosts = &hosts
			}
			if err := caps.apply(fl, body.Caps); err != nil {
				return err
			}
			if err := c.Do(cmd.Context(), "PATCH", "/targets/"+t.Id.String(), body, &t); err != nil {
				return err
			}
			return show(cmd, asJSON, t, func(w io.Writer) error {
				_, err := fmt.Fprintf(w, "Updated target %s (%s, %s; caps %s).\n", t.Name, t.BaseURL, verification(t), capsText(t.Caps))
				return err
			})
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "new name")
	cmd.Flags().StringVar(&baseURL, "base-url", "", "new base URL")
	cmd.Flags().StringSliceVar(&allow, "allow-host", nil, "hosts the target's requests may reach besides its own (repeatable; replaces the list)")
	caps.register(cmd.Flags())
	jsonFlag(cmd, &asJSON)
	return cmd
}

func newTargetsDeleteCmd(project *string) *cobra.Command {
	return &cobra.Command{
		Use:   "delete <target>",
		Short: "Delete a target",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, pid, err := projectClient(cmd.Context(), *project)
			if err != nil {
				return err
			}
			t, err := c.FindTarget(cmd.Context(), pid, args[0])
			if err != nil {
				return err
			}
			if err := c.Do(cmd.Context(), "DELETE", "/targets/"+t.Id.String(), nil, nil); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Deleted target %s.\n", t.Name)
			return nil
		},
	}
}

func newTargetsVerifyCmd(project *string) *cobra.Command {
	var local, asJSON bool
	cmd := &cobra.Command{
		Use:   "verify <target>",
		Short: "Prove you own a public target so higher load is allowed",
		Long: `Public targets run under low caps until you prove ownership by publishing a
token either as a DNS TXT record (stampede-verify=<token>) on the host or
on _stampede.<host>, or at /.well-known/stampede-verify.txt. Private and
loopback targets need no verification.

Signed in to a server, this checks the token of one of the project's
targets (by name, base URL or id) on the server and prints how to publish
it while it is missing. Given a URL that is not a target on the server, or
not signed in, or with --local, it checks this machine's token instead,
which is what stampede run uses.`,
		Example: `  stampede targets verify staging
  stampede target verify https://staging.example.com --local`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if local {
				return verifyLocal(ctx, cmd.OutOrStdout(), args[0])
			}
			c, err := client.New()
			if err != nil {
				if looksLikeURL(args[0]) {
					return verifyLocal(ctx, cmd.OutOrStdout(), args[0])
				}
				return err
			}
			t, err := findTargetIn(ctx, c, *project, args[0])
			if err != nil {
				if looksLikeURL(args[0]) {
					fmt.Fprintf(cmd.ErrOrStderr(), "stampede: %s is not a target on the server (%v); checking this machine's token (for stampede run)\n", args[0], err)
					return verifyLocal(ctx, cmd.OutOrStdout(), args[0])
				}
				return err
			}
			if err := c.Do(ctx, "POST", "/targets/"+t.Id.String()+"/verify", nil, &t); err != nil {
				return err
			}
			return show(cmd, asJSON, t, func(w io.Writer) error {
				switch {
				case t.Private:
					fmt.Fprintf(w, "%s is a private address; no verification is needed.\n", t.Name)
				case t.Verified:
					fmt.Fprintf(w, "%s is verified by %s.\n", t.Name, or(t.VerificationMethod, "?"))
				default:
					writeVerifyHelp(w, t)
					fmt.Fprintln(w, "Not verified yet.")
				}
				return nil
			})
		},
	}
	cmd.Flags().BoolVar(&local, "local", false, "check this machine's token for <url> (for stampede run) instead of a server target")
	jsonFlag(cmd, &asJSON)
	return cmd
}

func findTargetIn(ctx context.Context, c *client.Client, project, ref string) (gen.Target, error) {
	p, err := c.FindProject(ctx, project)
	if err != nil {
		return gen.Target{}, err
	}
	return c.FindTarget(ctx, p.Id.String(), ref)
}

func looksLikeURL(s string) bool {
	u, err := url.Parse(s)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
}

// verifyLocal checks this installation's ownership token for a URL, as
// stampede run does before sending load to a public host.
func verifyLocal(ctx context.Context, out io.Writer, raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return fmt.Errorf("give a full URL such as https://staging.example.com")
	}
	if private, err := safety.IsPrivateHost(ctx, u.Hostname()); err == nil && private {
		fmt.Fprintf(out, "%s is a private address; no verification is needed.\n", u.Hostname())
		return nil
	}
	secret, err := safety.InstallSecret()
	if err != nil {
		return err
	}
	token := safety.Token(secret, u.Hostname())
	fmt.Fprintf(out, "Publish one of these, then run this command again:\n\n")
	fmt.Fprintf(out, "  DNS TXT on %s or _stampede.%s:\n    %s%s\n\n", u.Hostname(), u.Hostname(), safety.TXTPrefix, token)
	fmt.Fprintf(out, "  or a file at %s://%s%s containing:\n    %s\n\n", u.Scheme, u.Host, safety.WellKnownPath, token)
	vctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	method, err := safety.Verify(vctx, u, token)
	if err != nil {
		fmt.Fprintf(out, "Not verified yet.\n")
		return nil
	}
	fmt.Fprintf(out, "Verified by %s.\n", method)
	return nil
}
