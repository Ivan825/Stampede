package cli

import (
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Ivan825/Stampede/internal/api/gen"
	"github.com/Ivan825/Stampede/internal/client"
)

func newIntegrationsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "integrations",
		Aliases: []string{"integration"},
		Short:   "List, add and delete Prometheus, trace and agent integrations (admin)",
		Long: `Scenarios run on the server refer to integrations by name, such as
observe: { prometheus: { integration: prom, queries: ... } }, so the server
only contacts URLs an admin configured here. A prometheus integration is a
Prometheus base URL; traces is a link template containing {traceId}; agent
is a stampede agent's control API, for fault injection. Bearer tokens are
encrypted and never shown.`,
	}
	var asJSON bool
	list := &cobra.Command{
		Use:   "list",
		Short: "List integrations (tokens are never shown)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := client.New()
			if err != nil {
				return err
			}
			var is []gen.Integration
			if err := c.Do(cmd.Context(), "GET", "/integrations", nil, &is); err != nil {
				return err
			}
			return show(cmd, asJSON, is, func(w io.Writer) error {
				if len(is) == 0 {
					_, err := fmt.Fprintln(w, "No integrations. Add one with stampede integrations create <name> --kind prometheus --url <url>.")
					return err
				}
				var rows [][]string
				for _, i := range is {
					tok := "-"
					if i.HasToken {
						tok = "token stored"
					}
					rows = append(rows, []string{i.Name, string(i.Kind), i.Url, tok, stamp(&i.CreatedAt)})
				}
				return table(w, "NAME\tKIND\tURL\tTOKEN\tCREATED", rows)
			})
		},
	}
	jsonFlag(list, &asJSON)

	var kind, url, tokenEnv string
	var tokenStdin, createJSON bool
	create := &cobra.Command{
		Use:   "create <name>",
		Short: "Add an integration",
		Long: `Add a named integration. A bearer token (optional for Prometheus,
required for an agent) is read from the environment variable named by
--token-env or from stdin with --token-stdin; it is stored encrypted.`,
		Example: `  stampede integrations create prom --kind prometheus --url http://prometheus:9090
  stampede integrations create jaeger --kind traces --url "https://jaeger.example.com/trace/{traceId}"
  stampede integrations create shop-agent --kind agent --url http://agent.shop.svc:7070 --token-env AGENT_TOKEN`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if kind == "" || url == "" {
				return errors.New("--kind and --url are required")
			}
			if tokenEnv != "" && tokenStdin {
				return errors.New("give --token-env or --token-stdin, not both")
			}
			c, err := client.New()
			if err != nil {
				return err
			}
			body := map[string]string{"name": args[0], "kind": kind, "url": url}
			if tokenEnv != "" || tokenStdin {
				tok, err := readValue(cmd, "", tokenEnv)
				if err != nil {
					return err
				}
				if tok == "" {
					return errors.New("the token is empty")
				}
				body["bearerToken"] = tok
			}
			var i gen.Integration
			if err := c.Do(cmd.Context(), "POST", "/integrations", body, &i); err != nil {
				return err
			}
			return show(cmd, createJSON, i, func(w io.Writer) error {
				_, err := fmt.Fprintf(w, "Added %s integration %s (%s).\n", i.Kind, i.Name, i.Url)
				return err
			})
		},
	}
	create.Flags().StringVar(&kind, "kind", "", "prometheus, traces or agent")
	create.Flags().StringVar(&url, "url", "", "Prometheus base URL, trace link template with {traceId}, or agent control API URL")
	create.Flags().StringVar(&tokenEnv, "token-env", "", "read the bearer token from this environment variable")
	create.Flags().BoolVar(&tokenStdin, "token-stdin", false, "read the bearer token from stdin")
	jsonFlag(create, &createJSON)

	del := &cobra.Command{
		Use:   "delete <name>",
		Short: "Delete an integration",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := client.New()
			if err != nil {
				return err
			}
			i, err := c.FindIntegration(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			if err := c.Do(cmd.Context(), "DELETE", "/integrations/"+i.Id.String(), nil, nil); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Deleted integration %s.\n", i.Name)
			return nil
		},
	}
	cmd.AddCommand(list, create, del)
	return cmd
}

func newNotifyCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "notify",
		Short: "Manage notification channels and read their delivery log (admin)",
		Long: `Notification channels (signed webhooks, Slack and Discord) hear when a run
finishes, misses a target or is killed, and when a drift check finds
broken journeys. Deliveries are retried and logged.`,
	}
	cmd.AddCommand(newNotifyChannelsCmd(), newNotifyDeliveriesCmd())
	return cmd
}

func deliveryText(d *gen.NotificationDelivery) string {
	if d == nil {
		return "-"
	}
	res := "ok"
	if !d.Ok {
		res = "failed"
		if d.Error != "" {
			res += ": " + d.Error
		}
	}
	if d.StatusCode != 0 {
		res += fmt.Sprintf(" (HTTP %d)", d.StatusCode)
	}
	return fmt.Sprintf("%s %s %s", d.Event, res, stamp(&d.At))
}

func newNotifyChannelsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "channels",
		Aliases: []string{"channel"},
		Short:   "List, add, delete and test notification channels",
	}
	var asJSON bool
	list := &cobra.Command{
		Use:   "list",
		Short: "List channels (URLs and secrets are never shown)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := client.New()
			if err != nil {
				return err
			}
			var cs []gen.NotificationChannel
			if err := c.Do(cmd.Context(), "GET", "/notifications/channels", nil, &cs); err != nil {
				return err
			}
			return show(cmd, asJSON, cs, func(w io.Writer) error {
				if len(cs) == 0 {
					_, err := fmt.Fprintln(w, "No channels. Add one with stampede notify channels create <name> --kind slack --url <webhook>.")
					return err
				}
				var rows [][]string
				for _, ch := range cs {
					events := make([]string, len(ch.Events))
					for i, e := range ch.Events {
						events[i] = string(e)
					}
					rows = append(rows, []string{ch.Name, string(ch.Kind), ch.UrlHint, strings.Join(events, ","), deliveryText(ch.LastDelivery)})
				}
				return table(w, "NAME\tKIND\tDESTINATION\tEVENTS\tLAST DELIVERY", rows)
			})
		},
	}
	jsonFlag(list, &asJSON)

	var kind, url, urlEnv, secretEnv string
	var events []string
	var allowPrivate, createJSON bool
	create := &cobra.Command{
		Use:   "create <name>",
		Short: "Add a webhook, Slack or Discord channel",
		Long: `Add a channel. The destination URL is stored encrypted and never shown
again; give it with --url, or with --url-env to keep a Slack or Discord
webhook (which works as a password) out of shell history. A generic
webhook signs each body with HMAC-SHA256 in X-Stampede-Signature: its
secret is read from --secret-env, or generated and printed once.
Destinations on private, loopback or link-local addresses need
--allow-private. Events: run.finished, run.target_failed, run.killed,
drift.detected (default all).`,
		Example: `  stampede notify channels create team --kind slack --url-env SLACK_WEBHOOK --event run.target_failed --event run.killed
  stampede notify channels create ci --kind webhook --url https://ci.example.com/hooks/stampede`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if kind == "" {
				return errors.New("--kind is required: webhook, slack or discord")
			}
			if (url == "") == (urlEnv == "") {
				return errors.New("give the destination with --url or --url-env")
			}
			c, err := client.New()
			if err != nil {
				return err
			}
			if urlEnv != "" {
				if url, err = readValue(cmd, "", urlEnv); err != nil {
					return err
				}
			}
			body := map[string]any{"name": args[0], "kind": kind, "url": url, "allowPrivate": allowPrivate}
			if len(events) > 0 {
				body["events"] = events
			}
			if secretEnv != "" {
				sec, err := readValue(cmd, "", secretEnv)
				if err != nil {
					return err
				}
				body["secret"] = sec
			}
			var res gen.NotificationChannelCreated
			if err := c.Do(cmd.Context(), "POST", "/notifications/channels", body, &res); err != nil {
				return err
			}
			if createJSON {
				return writeJSON(cmd.OutOrStdout(), res)
			}
			ch := res.Channel
			fmt.Fprintf(cmd.OutOrStdout(), "Added %s channel %s (%s). Test it with stampede notify channels test %s.\n", ch.Kind, ch.Name, ch.UrlHint, ch.Name)
			if res.Secret != nil && secretEnv == "" {
				fmt.Fprintf(cmd.OutOrStdout(), "Signing secret, shown once: %s\n", *res.Secret)
			}
			return nil
		},
	}
	fl := create.Flags()
	fl.StringVar(&kind, "kind", "", "webhook, slack or discord")
	fl.StringVar(&url, "url", "", "destination URL")
	fl.StringVar(&urlEnv, "url-env", "", "read the destination URL from this environment variable")
	fl.StringArrayVar(&events, "event", nil, "event to send (repeatable; default all)")
	fl.BoolVar(&allowPrivate, "allow-private", false, "allow a destination on a private, loopback or link-local address")
	fl.StringVar(&secretEnv, "secret-env", "", "webhooks: read the signing secret (16+ characters) from this environment variable")
	jsonFlag(create, &createJSON)

	del := &cobra.Command{
		Use:   "delete <channel>",
		Short: "Delete a channel",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := client.New()
			if err != nil {
				return err
			}
			ch, err := c.FindChannel(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			if err := c.Do(cmd.Context(), "DELETE", "/notifications/channels/"+ch.Id.String(), nil, nil); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Deleted channel %s.\n", ch.Name)
			return nil
		},
	}

	var testJSON bool
	test := &cobra.Command{
		Use:   "test <channel>",
		Short: "Send a test notification now (one attempt, no retries)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := client.New()
			if err != nil {
				return err
			}
			ch, err := c.FindChannel(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			var d gen.NotificationDelivery
			if err := c.Do(cmd.Context(), "POST", "/notifications/channels/"+ch.Id.String()+"/test", nil, &d); err != nil {
				return err
			}
			err = show(cmd, testJSON, d, func(w io.Writer) error {
				_, err := fmt.Fprintf(w, "Test to %s: %s in %dms.\n", ch.Name, strings.TrimPrefix(deliveryText(&d), d.Event+" "), d.DurationMs)
				return err
			})
			if err == nil && !d.Ok {
				err = fmt.Errorf("the test notification to %s failed", ch.Name)
			}
			return err
		},
	}
	jsonFlag(test, &testJSON)
	cmd.AddCommand(list, create, del, test)
	return cmd
}

func newNotifyDeliveriesCmd() *cobra.Command {
	var limit int
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "deliveries <channel>",
		Short: "List a channel's most recent delivery attempts, newest first",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := client.New()
			if err != nil {
				return err
			}
			ch, err := c.FindChannel(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			var ds []gen.NotificationDelivery
			if err := c.Do(cmd.Context(), "GET", "/notifications/channels/"+ch.Id.String()+"/deliveries"+client.Query("limit", strconv.Itoa(limit)), nil, &ds); err != nil {
				return err
			}
			return show(cmd, asJSON, ds, func(w io.Writer) error {
				if len(ds) == 0 {
					_, err := fmt.Fprintln(w, "No deliveries yet.")
					return err
				}
				var rows [][]string
				for _, d := range ds {
					res := "ok"
					if !d.Ok {
						res = "failed"
					}
					code := "-"
					if d.StatusCode != 0 {
						code = strconv.Itoa(d.StatusCode)
					}
					run := "-"
					if d.RunId != nil {
						run = d.RunId.String()[:8]
					}
					rows = append(rows, []string{stamp(&d.At), d.Event, run, strconv.Itoa(d.Attempt), res, code, fmt.Sprintf("%dms", d.DurationMs), d.Error})
				}
				return table(w, "WHEN\tEVENT\tRUN\tATTEMPT\tRESULT\tHTTP\tTOOK\tERROR", rows)
			})
		},
	}
	cmd.Flags().IntVar(&limit, "limit", 50, "how many attempts (at most 50)")
	jsonFlag(cmd, &asJSON)
	return cmd
}
