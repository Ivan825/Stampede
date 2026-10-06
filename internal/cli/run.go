package cli

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"cel.dev/cel-go/interpreter"
	"github.com/spf13/cobra"

	"github.com/Ivan825/Stampede/internal/report"
	"github.com/Ivan825/Stampede/internal/runner"
	"github.com/Ivan825/Stampede/internal/safety"
	"github.com/Ivan825/Stampede/internal/scenario"
)

// Exit codes for `stampede run`.
const (
	ExitOK            = 0
	ExitError         = 1
	ExitTargetsFailed = 3
)

// ExitError carries a process exit code.
type exitError struct {
	code int
	msg  string
}

func (e *exitError) Error() string { return e.msg }

// ExitCode returns the exit code for err (1 for ordinary errors).
func ExitCode(err error) int {
	var ee *exitError
	if errors.As(err, &ee) {
		return ee.code
	}
	return ExitError
}

type runFlags struct {
	html, json, junit, md string
	env                   []string
	baseURL               string
	vus                   int
	rate                  string
	duration              string
	shape                 string
	iterations            int
	allowHosts            []string
	quiet                 bool
	verbose               bool
}

func newRunCmd() *cobra.Command {
	f := &runFlags{}
	cmd := &cobra.Command{
		Use:   "run <scenario.yaml>",
		Short: "Run a scenario in-process and write a report (no server needed)",
		Long: `Run a scenario with an in-process engine. Nothing else needs to be running:
no server, no database. Prints a summary and can write HTML, JSON, JUnit
and Markdown reports.

Exit codes: 0 when every target passes (or none are set), 3 when a target
fails, 1 on any other error.`,
		Example: `  stampede run checkout.yaml
  stampede run checkout.yaml --shape spike --rate 200/s -o report.html
  stampede run smoke.yaml -e TARGET_URL=http://localhost:8090 --junit junit.xml`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runScenario(cmd.Context(), cmd.OutOrStdout(), cmd.ErrOrStderr(), args[0], f)
		},
	}
	fl := cmd.Flags()
	fl.StringVarP(&f.html, "out", "o", "", "write an HTML report to this file")
	fl.StringVar(&f.json, "json", "", "write a JSON report to this file (- for stdout)")
	fl.StringVar(&f.junit, "junit", "", "write JUnit XML (one test per target) to this file")
	fl.StringVar(&f.md, "md", "", "write a Markdown summary to this file (- for stdout)")
	fl.StringArrayVarP(&f.env, "env", "e", nil, "set ${env.KEY} for the scenario (KEY=VALUE, repeatable)")
	fl.StringVar(&f.baseURL, "base-url", "", "override target.baseURL")
	fl.IntVar(&f.vus, "vus", 0, "override the number of virtual users")
	fl.StringVar(&f.rate, "rate", "", "override the arrival rate, e.g. 100/s (switches to rate mode)")
	fl.StringVar(&f.duration, "duration", "", "override the duration, e.g. 30s or 5m")
	fl.StringVar(&f.shape, "shape", "", "apply a traffic shape: "+strings.Join(scenario.Shapes, ", "))
	fl.IntVar(&f.iterations, "iterations", 0, "run a fixed number of iterations instead")
	fl.StringSliceVar(&f.allowHosts, "allow-host", nil, "extra public hosts requests may reach besides the target")
	fl.BoolVarP(&f.quiet, "quiet", "q", false, "no live progress")
	fl.BoolVarP(&f.verbose, "verbose", "v", false, "log step errors as they happen")
	return cmd
}

func runScenario(ctx context.Context, stdout, stderr io.Writer, path string, f *runFlags) error {
	s, err := scenario.LoadFile(path)
	if err != nil {
		return err
	}
	applyOverrides(s, f)
	if err := s.Validate(); err != nil {
		return err
	}

	env := map[string]string{}
	for _, kv := range os.Environ() {
		k, v, _ := strings.Cut(kv, "=")
		env[k] = v
	}
	for _, kv := range f.env {
		k, v, ok := strings.Cut(kv, "=")
		if !ok {
			return fmt.Errorf("--env %q: use KEY=VALUE", kv)
		}
		env[k] = v
	}
	secrets := map[string]string{}
	for k, v := range env {
		if name, ok := strings.CutPrefix(k, "STAMPEDE_SECRET_"); ok {
			secrets[name] = v
		} else {
			secrets[k] = v
		}
	}

	level := slog.LevelError
	if f.verbose {
		level = slog.LevelWarn
	}
	logger := slog.New(slog.NewTextHandler(stderr, &slog.HandlerOptions{Level: level}))

	plan, err := s.Load.Plan()
	if err != nil {
		return err
	}
	base, err := renderBaseURL(s, env, secrets)
	if err != nil {
		return err
	}
	policy, err := checkTarget(ctx, stderr, base, plan, f.allowHosts)
	if err != nil {
		return err
	}

	runID := newRunID()
	var prog func(runner.Progress)
	if !f.quiet {
		fmt.Fprintf(stderr, "stampede: running %s against %s (%s, %s planned)\n", s.Metadata.Name, base, plan.Executor, plan.TotalDuration())
		prog = progressPrinter(stderr)
	}
	rep, err := runner.Run(ctx, runner.Options{
		Scenario: s, RunID: runID, Env: env, Secrets: secrets,
		AllowHost: policy, Logger: logger, Progress: prog,
	})
	if err != nil {
		return err
	}

	if f.json != "-" && f.md != "-" {
		rep.WriteText(stdout)
	}
	if err := writeOutputs(stdout, rep, f); err != nil {
		return err
	}
	if rep.Verdict == report.VerdictFail {
		return &exitError{code: ExitTargetsFailed, msg: "one or more targets failed"}
	}
	return nil
}

func applyOverrides(s *scenario.Scenario, f *runFlags) {
	l := &s.Load
	if f.baseURL != "" {
		s.Target.BaseURL = f.baseURL
	}
	if f.shape != "" {
		l.Shape = f.shape
		l.Stages = nil
		l.Iterations = 0
	}
	if f.rate != "" {
		if r, err := scenario.ParseRate(f.rate); err == nil {
			l.Mode, l.Rate = scenario.ModeRate, r
			if f.shape == "" && len(l.Stages) == 0 && l.Shape == "" {
				l.Iterations = 0
			}
		}
	}
	if f.vus > 0 {
		l.VUs = f.vus
		if f.rate == "" {
			l.Mode = scenario.ModeVUs
		}
	}
	if f.duration != "" {
		if d, err := scenario.ParseDuration(f.duration); err == nil {
			l.Duration = d
			if f.shape == "" {
				l.Stages, l.Iterations = nil, 0
			}
		}
	}
	if f.iterations > 0 {
		l.Iterations, l.Shape, l.Stages, l.Mode, l.Duration = f.iterations, "", nil, scenario.ModeVUs, 0
	}
}

func renderBaseURL(s *scenario.Scenario, env, secrets map[string]string) (string, error) {
	sc, err := scenario.NewScope()
	if err != nil {
		return "", err
	}
	t, err := sc.CompileTemplate(s.Target.BaseURL)
	if err != nil {
		return "", err
	}
	act := baseActivation{"env": env, "secret": secrets, "vars": s.Vars, "data": map[string]any{}}
	out, err := t.Render(act)
	if err != nil {
		return "", fmt.Errorf("target.baseURL: %w (set it with -e or --base-url)", err)
	}
	if out == "" {
		return "", errors.New("target.baseURL is empty (set it in the scenario, with -e, or with --base-url)")
	}
	return out, nil
}

type baseActivation map[string]any

func (b baseActivation) ResolveName(n string) (any, bool) { v, ok := b[n]; return v, ok }
func (b baseActivation) Parent() interpreter.Activation   { return nil }

// checkTarget applies the safety rules: private targets are always
// allowed; public targets need verified ownership to exceed low caps.
func checkTarget(ctx context.Context, stderr io.Writer, base string, plan *scenario.Plan, extra []string) (func(*url.URL) bool, error) {
	u, err := url.Parse(base)
	if err != nil || u.Host == "" {
		return nil, fmt.Errorf("invalid target URL %q", base)
	}
	policy := safety.NewHostPolicy(u.Hostname(), extra)
	lctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	private, err := safety.IsPrivateHost(lctx, u.Hostname())
	if err != nil {
		return nil, fmt.Errorf("resolve target %s: %w", u.Hostname(), err)
	}
	if private {
		return policy.Allow, nil
	}
	secret, err := safety.InstallSecret()
	if err != nil {
		return nil, err
	}
	vctx, vcancel := context.WithTimeout(ctx, 15*time.Second)
	defer vcancel()
	if method, err := safety.Verify(vctx, u, safety.Token(secret, u.Hostname())); err == nil {
		fmt.Fprintf(stderr, "stampede: ownership of %s verified (%s)\n", u.Hostname(), method)
		return policy.Allow, nil
	}
	if err := safety.CheckPlan(plan, safety.UnverifiedPublicCaps); err != nil {
		return nil, fmt.Errorf("%s is a public host whose ownership is not verified, so load is capped: %w.\nRun `stampede target verify %s` to verify it", u.Hostname(), err, base)
	}
	fmt.Fprintf(stderr, "stampede: %s is public and unverified; running under the low default caps\n", u.Hostname())
	return policy.Allow, nil
}

func writeOutputs(stdout io.Writer, rep *report.Report, f *runFlags) error {
	write := func(path string, fn func(io.Writer) error) error {
		if path == "" {
			return nil
		}
		if path == "-" {
			return fn(stdout)
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			return err
		}
		fh, err := os.Create(path)
		if err != nil {
			return err
		}
		if err := fn(fh); err != nil {
			fh.Close()
			return err
		}
		return fh.Close()
	}
	if err := write(f.html, rep.WriteHTML); err != nil {
		return err
	}
	if err := write(f.json, rep.WriteJSON); err != nil {
		return err
	}
	if err := write(f.junit, rep.WriteJUnit); err != nil {
		return err
	}
	return write(f.md, func(w io.Writer) error { rep.WriteMarkdown(w); return nil })
}

func progressPrinter(w io.Writer) func(runner.Progress) {
	return func(p runner.Progress) {
		t := p.Snapshot.Totals()
		errRate := 0.0
		if t.Requests > 0 {
			errRate = float64(t.Failed) / float64(t.Requests)
		}
		planned := fmt.Sprintf("%.0f VUs", p.Planned)
		if p.Mode == scenario.ModeRate {
			planned = fmt.Sprintf("%.0f/s", p.Planned)
		}
		fmt.Fprintf(w, "  %6s/%-6s  plan %-9s vus %-5d rps %-7d p95 %-9s err %-7s dropped %d\n",
			p.Elapsed.Truncate(time.Second), p.Total.Truncate(time.Second), planned, p.Snapshot.VUs, t.Requests,
			report.Ms(float64(t.Latency.Quantile(0.95))/1e6), report.Pct(errRate), p.Snapshot.Dropped)
	}
}

func newRunID() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return time.Now().UTC().Format("20060102-150405") + "-" + hex.EncodeToString(b)
}
