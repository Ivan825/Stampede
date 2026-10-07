package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/Ivan825/Stampede/internal/api/gen"
	"github.com/Ivan825/Stampede/internal/client"
)

// parseWhen reads a time as RFC 3339, a date, or a duration ago (24h, 7d).
func parseWhen(s string, now time.Time) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}
	if t, err := time.ParseInLocation("2006-01-02", s, time.Local); err == nil {
		return t, nil
	}
	if d, ok := strings.CutSuffix(s, "d"); ok {
		if n, err := strconv.Atoi(d); err == nil && n >= 0 {
			return now.Add(-time.Duration(n) * 24 * time.Hour), nil
		}
	}
	if d, err := time.ParseDuration(s); err == nil && d >= 0 {
		return now.Add(-d), nil
	}
	return time.Time{}, fmt.Errorf("%q is not a time: use a duration such as 24h or 7d, a date such as 2026-10-01, or RFC 3339", s)
}

func newAuditCmd() *cobra.Command {
	var limit int
	var since, before, action string
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "audit",
		Short: "Read the organisation's audit log, newest first (admin)",
		Long: `Print who did what and when: sign-ins, runs started and killed, changes to
users, tokens, projects, targets, secrets, schedules, caps, AI providers,
integrations and channels. --since and --before take a duration ago (24h,
7d), a date or an RFC 3339 time; --action keeps entries whose action
starts with the given text, such as run. or token.`,
		Example: `  stampede audit --since 24h
  stampede audit --action run.kill --limit 20 --json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if limit < 1 {
				return errors.New("--limit must be at least 1")
			}
			now := time.Now()
			var from, until time.Time
			var err error
			if since != "" {
				if from, err = parseWhen(since, now); err != nil {
					return fmt.Errorf("--since: %w", err)
				}
			}
			if before != "" {
				if until, err = parseWhen(before, now); err != nil {
					return fmt.Errorf("--before: %w", err)
				}
			}
			c, err := client.New()
			if err != nil {
				return err
			}
			var out []gen.AuditEntry
		pages:
			for len(out) < limit {
				page := limit - len(out)
				if since != "" || action != "" {
					page = 500 // filtered here, so read whole pages
				}
				b := ""
				if !until.IsZero() {
					b = until.UTC().Format(time.RFC3339Nano)
				}
				var es []gen.AuditEntry
				if err := c.Do(cmd.Context(), "GET", "/audit"+client.Query("limit", strconv.Itoa(min(page, 500)), "before", b), nil, &es); err != nil {
					return err
				}
				for _, e := range es {
					if !from.IsZero() && e.At.Before(from) {
						break pages
					}
					if strings.HasPrefix(e.Action, action) {
						out = append(out, e)
						if len(out) == limit {
							break pages
						}
					}
				}
				if len(es) < min(page, 500) {
					break
				}
				until = es[len(es)-1].At
			}
			if out == nil {
				out = []gen.AuditEntry{}
			}
			return show(cmd, asJSON, out, func(w io.Writer) error {
				if len(out) == 0 {
					_, err := fmt.Fprintln(w, "No audit entries.")
					return err
				}
				var rows [][]string
				for _, e := range out {
					rows = append(rows, []string{e.At.Local().Format("Jan 2 15:04:05"), e.Actor, e.Action, or(e.Subject, "-"), detailsText(e.Details), or(e.Ip, "-")})
				}
				return table(w, "WHEN\tACTOR\tACTION\tSUBJECT\tDETAILS\tIP", rows)
			})
		},
	}
	cmd.Flags().IntVar(&limit, "limit", 100, "most entries to print")
	cmd.Flags().StringVar(&since, "since", "", "only entries after this time (24h, 7d, 2026-10-01 or RFC 3339)")
	cmd.Flags().StringVar(&before, "before", "", "only entries before this time")
	cmd.Flags().StringVar(&action, "action", "", "only actions starting with this, such as run. or token.create")
	jsonFlag(cmd, &asJSON)
	return cmd
}

// detailsText renders audit details as sorted key=value pairs.
func detailsText(d *map[string]any) string {
	if d == nil || len(*d) == 0 {
		return "-"
	}
	keys := make([]string, 0, len(*d))
	for k := range *d {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		v := (*d)[k]
		s, ok := v.(string)
		if !ok {
			b, _ := json.Marshal(v)
			s = string(b)
		}
		parts = append(parts, k+"="+s)
	}
	return strings.Join(parts, " ")
}

func newSettingsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "settings",
		Short: "Show the server's single sign-on configuration and run limits (admin)",
		Long: `Read-only views of the server's configuration. Single sign-on is set with
stampede server --oidc-* flags; server caps with stampede server flags;
organisation caps with stampede caps; project caps with stampede projects
settings; target caps with stampede targets update.`,
	}
	var ssoJSON bool
	sso := &cobra.Command{
		Use:   "sso",
		Short: "Show the single sign-on configuration (the client secret is never shown)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := client.New()
			if err != nil {
				return err
			}
			var s gen.SSOSettings
			if err := c.Do(cmd.Context(), "GET", "/settings/sso", nil, &s); err != nil {
				return err
			}
			return show(cmd, ssoJSON, s, func(w io.Writer) error {
				if !s.Enabled {
					_, err := fmt.Fprintln(w, "Single sign-on is off; people sign in with email and password (stampede server --oidc-issuer turns it on).")
					return err
				}
				fmt.Fprintf(w, "Single sign-on:  on, button %q\n", or(s.Name, "Single sign-on"))
				fmt.Fprintf(w, "Issuer:          %s\n", or(s.Issuer, "-"))
				fmt.Fprintf(w, "Redirect URL:    %s\n", or(s.RedirectURL, "-"))
				fmt.Fprintf(w, "Scopes:          %s\n", strings.Join(s.Scopes, " "))
				domains := "any"
				if len(s.AllowedDomains) > 0 {
					domains = strings.Join(s.AllowedDomains, ", ")
				}
				fmt.Fprintf(w, "Email domains:   %s\n", domains)
				role := or(s.DefaultRole, "")
				if role == "" {
					role = "none: only existing accounts may sign in"
				}
				fmt.Fprintf(w, "First sign-in:   %s\n", role)
				_, err := fmt.Fprintf(w, "Passwords:       %s\n", map[bool]string{true: "also allowed", false: "turned off"}[s.PasswordLogin])
				return err
			})
		},
	}
	jsonFlag(sso, &ssoJSON)

	var limitsJSON bool
	limits := &cobra.Command{
		Use:   "limits",
		Short: "Show every cap a run is checked against, and each target's effective caps",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := client.New()
			if err != nil {
				return err
			}
			var l gen.LimitSettings
			if err := c.Do(cmd.Context(), "GET", "/settings/limits", nil, &l); err != nil {
				return err
			}
			return show(cmd, limitsJSON, l, func(w io.Writer) error {
				fmt.Fprintf(w, "Server:              %s\n", capsText(gen.Caps(l.Server)))
				fmt.Fprintf(w, "Organisation:        %s\n", capsText(gen.Caps(l.Organisation)))
				fmt.Fprintf(w, "Unverified public:   %s\n", capsText(gen.Caps(l.UnverifiedPublic)))
				floor := "off"
				if f := l.AbortFloor; f != nil {
					var parts []string
					if f.ErrorRate != nil {
						parts = append(parts, fmt.Sprintf("errors > %.0f%%", *f.ErrorRate*100))
					}
					if f.P95Seconds != nil {
						parts = append(parts, fmt.Sprintf("p95 > %gs", *f.P95Seconds))
					}
					floor = strings.Join(parts, " or ") + fmt.Sprintf(" for %gs", f.ForSeconds)
				}
				fmt.Fprintf(w, "Abort floor:         %s\n\n", floor)
				var rows [][]string
				for _, p := range l.Projects {
					rows = append(rows, []string{p.Name, capsText(gen.Caps(p.Caps)), map[bool]string{true: "required", false: "-"}[p.RequireDryRun]})
				}
				if err := table(w, "PROJECT\tCAPS\tDRY RUN", rows); err != nil {
					return err
				}
				fmt.Fprintln(w)
				rows = nil
				for _, t := range l.Targets {
					own := "unverified"
					if t.Private {
						own = "private"
					} else if t.Verified {
						own = "verified"
					}
					rows = append(rows, []string{t.ProjectName + "/" + t.Name, t.BaseURL, own, capsText(gen.Caps(t.Caps)), capsText(gen.Caps(t.Effective))})
				}
				return table(w, "TARGET\tBASE URL\tOWNERSHIP\tOWN CAPS\tEFFECTIVE CAPS", rows)
			})
		},
	}
	jsonFlag(limits, &limitsJSON)
	cmd.AddCommand(sso, limits)
	return cmd
}
