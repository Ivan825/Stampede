package cli

import (
	"context"
	"fmt"
	"net/url"
	"time"

	"github.com/spf13/cobra"

	"github.com/Ivan825/Stampede/internal/safety"
	"github.com/Ivan825/Stampede/internal/scenario"
)

func newValidateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "validate <scenario.yaml>...",
		Short: "Check scenario files without running them",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			failed := 0
			for _, path := range args {
				s, err := scenario.LoadFile(path)
				if err != nil {
					failed++
					fmt.Fprintf(cmd.ErrOrStderr(), "✗ %v\n", err)
					continue
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
			}
			if failed > 0 {
				return fmt.Errorf("%d of %d scenarios are invalid", failed, len(args))
			}
			return nil
		},
	}
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
