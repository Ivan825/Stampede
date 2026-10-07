package cli

import (
	"context"
	"fmt"
	"net/url"
	"time"

	"github.com/spf13/cobra"

	"github.com/Ivan825/Stampede/internal/ai"
	"github.com/Ivan825/Stampede/internal/pack"
	"github.com/Ivan825/Stampede/internal/safety"
	"github.com/Ivan825/Stampede/internal/scenario"
)

type validateFlags struct {
	dryRun     bool
	env        []string
	baseURL    string
	allowHosts []string
	timeout    time.Duration
}

func newValidateCmd() *cobra.Command {
	f := &validateFlags{}
	cmd := &cobra.Command{
		Use:   "validate <scenario.yaml>...",
		Short: "Check scenario files without running them",
		Long: `Checks scenario files against the scenario format: structure, templates,
expressions, extractors and that variables are defined before use. Plugin
steps are also checked against the schemas of the plugins installed on this
machine; a plugin that is not installed here is reported, and its steps'
settings are checked when a run starts on a worker that has it.

With --dry-run, each valid file's journeys then run once each, with one
user, against the target (set it as for stampede run: -e or --base-url),
and each journey is reported as passing or failing with its first error.
Unique data is read in order rather than used up. The target's safety
rules apply as for a run. Replay scenarios are checked statically only.`,
		Example: `  stampede validate checkout.yaml
  stampede validate journeys/*.yaml --dry-run -e TARGET_URL=http://localhost:8090`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if !f.dryRun {
				for _, name := range []string{"env", "base-url", "allow-host", "timeout"} {
					if cmd.Flags().Changed(name) {
						return fmt.Errorf("--%s applies only with --dry-run", name)
					}
				}
			}
			failed, journeysFailed := 0, 0
			for _, path := range args {
				s, err := scenario.LoadFile(path)
				if err == nil {
					if err = s.LoadReplay(); err != nil {
						err = fmt.Errorf("%s: %w", path, err)
					}
				}
				if err != nil {
					failed++
					fmt.Fprintf(cmd.ErrOrStderr(), "✗ %v\n", err)
					continue
				}
				skipped, err := checkPluginSteps(cmd.Context(), s)
				if err != nil {
					failed++
					fmt.Fprintf(cmd.ErrOrStderr(), "✗ %s: %v\n", path, err)
					continue
				}
				for _, name := range skipped {
					fmt.Fprintf(cmd.ErrOrStderr(), "! %s: plugin %s is not installed here, so its steps' settings were not checked (stampede plugin install %s)\n", path, name, name)
				}
				for _, c := range ai.ThirdPartyCalls(s) {
					fmt.Fprintf(cmd.ErrOrStderr(), "! %s: %s; load tests must not call payment, SMS, email or CAPTCHA services (a server refuses the run unless the host is one of the target's allowed hosts)\n", path, c)
				}
				plan, _ := s.Load.Plan()
				load := fmt.Sprintf("peak %.0f VUs, %s", plan.Peak(), plan.TotalDuration())
				switch {
				case plan.Executor == scenario.ExecIterations:
					load = fmt.Sprintf("%d iterations over %d VUs", plan.Iterations, plan.VUs)
				case plan.Mode == scenario.ModeRate:
					load = fmt.Sprintf("peak %.0f/s, %s", plan.Peak(), plan.TotalDuration())
				}
				fmt.Fprintf(cmd.OutOrStdout(), "✓ %s: %d journeys, %s, %s\n", path, len(s.Journeys), plan.Executor, load)
				if f.dryRun {
					n, err := dryRunScenario(cmd, path, s, f)
					if err != nil {
						failed++
						fmt.Fprintf(cmd.ErrOrStderr(), "✗ %s: %v\n", path, err)
						continue
					}
					journeysFailed += n
				}
			}
			if failed > 0 {
				return fmt.Errorf("%d of %d scenarios are invalid", failed, len(args))
			}
			if journeysFailed > 0 {
				return fmt.Errorf("%d journey(s) failed their dry run", journeysFailed)
			}
			return nil
		},
	}
	fl := cmd.Flags()
	fl.BoolVar(&f.dryRun, "dry-run", false, "also run each journey once with one user against the target")
	fl.StringArrayVarP(&f.env, "env", "e", nil, "with --dry-run: set ${env.KEY} (KEY=VALUE, repeatable)")
	fl.StringVar(&f.baseURL, "base-url", "", "with --dry-run: override target.baseURL")
	fl.StringSliceVar(&f.allowHosts, "allow-host", nil, "with --dry-run: extra public hosts requests may reach besides the target")
	fl.DurationVar(&f.timeout, "timeout", 2*time.Minute, "with --dry-run: time allowed for each file's journeys, think times included")
	return cmd
}

// dryRunScenario runs each journey of a valid scenario once with one user
// and prints a line per journey. It returns how many journeys failed.
func dryRunScenario(cmd *cobra.Command, path string, s *scenario.Scenario, f *validateFlags) (int, error) {
	out := cmd.OutOrStdout()
	if s.Load.Mode == scenario.ModeReplay {
		fmt.Fprintf(out, "  - %s is a replay; its requests come from the recording and are not dry-run\n", path)
		return 0, nil
	}
	env, secrets, err := runEnv(f.env)
	if err != nil {
		return 0, err
	}
	// The dry run hands one map to both ${env.X} and ${secret.X}.
	for k, v := range secrets {
		if _, ok := env[k]; !ok {
			env[k] = v
		}
	}
	if f.baseURL != "" {
		s.Target.BaseURL = f.baseURL
	}
	base, err := renderBaseURL(s, env, env)
	if err != nil {
		return 0, err
	}
	var policy func(*url.URL) bool
	if base == "" {
		policy = safety.NewHostPolicy("", f.allowHosts).Allow
	} else {
		one, err := scenario.Load{Mode: scenario.ModeVUs, Iterations: 1, VUs: 1}.Plan()
		if err != nil {
			return 0, err
		}
		if policy, err = checkTarget(cmd.Context(), cmd.ErrOrStderr(), base, one, f.allowHosts); err != nil {
			return 0, err
		}
	}
	ctx, cancel := context.WithTimeout(cmd.Context(), f.timeout)
	defer cancel()
	res, err := pack.DryRun(ctx, path, s, env, policy)
	if err != nil {
		return 0, err
	}
	failed := 0
	for _, r := range res {
		if r.OK {
			fmt.Fprintf(out, "  ✓ %s\n", r.Journey)
		} else {
			failed++
			fmt.Fprintf(out, "  ✗ %s: %s\n", r.Journey, r.Problem)
		}
	}
	return failed, nil
}

func newTargetCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "target", Short: "Manage load test targets"}
	cmd.AddCommand(&cobra.Command{
		Use:   "verify <url>",
		Short: "Prove you own a public target so higher load is allowed",
		Long: `Public targets run under low caps until you prove ownership. This prints a
token and checks whether it is published either as a DNS TXT record
(stampede-verify=<token>) on the host or on _stampede.<host>, or at
/.well-known/stampede-verify.txt. Private and loopback targets need no
verification.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			u, err := url.Parse(args[0])
			if err != nil || u.Host == "" {
				return fmt.Errorf("give a full URL such as https://staging.example.com")
			}
			ctx := cmd.Context()
			if private, err := safety.IsPrivateHost(ctx, u.Hostname()); err == nil && private {
				fmt.Fprintf(cmd.OutOrStdout(), "%s is a private address; no verification is needed.\n", u.Hostname())
				return nil
			}
			secret, err := safety.InstallSecret()
			if err != nil {
				return err
			}
			token := safety.Token(secret, u.Hostname())
			out := cmd.OutOrStdout()
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
		},
	})
	return cmd
}
