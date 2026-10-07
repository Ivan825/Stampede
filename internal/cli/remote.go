package cli

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/Ivan825/Stampede/internal/api/gen"
	"github.com/Ivan825/Stampede/internal/client"
	"github.com/Ivan825/Stampede/internal/report"
	"github.com/Ivan825/Stampede/internal/scenario"
)

func newLoginCmd() *cobra.Command {
	var server, email string
	cmd := &cobra.Command{
		Use:   "login",
		Short: "Sign in to a Stampede server and store an API token for this CLI",
		RunE: func(cmd *cobra.Command, _ []string) error {
			in := bufio.NewReader(cmd.InOrStdin())
			out := cmd.ErrOrStderr()
			if server == "" {
				server = "http://localhost:8080"
			}
			if email == "" {
				fmt.Fprint(out, "Email: ")
				line, _ := in.ReadString('\n')
				email = strings.TrimSpace(line)
			}
			fmt.Fprint(out, "Password: ")
			var pw string
			if f, ok := cmd.InOrStdin().(*os.File); ok && term.IsTerminal(int(f.Fd())) {
				b, err := term.ReadPassword(int(f.Fd()))
				fmt.Fprintln(out)
				if err != nil {
					return err
				}
				pw = string(b)
			} else {
				line, _ := in.ReadString('\n')
				pw = strings.TrimSpace(line)
			}
			host, _ := os.Hostname()
			tok, me, err := client.Login(cmd.Context(), server, email, pw, "cli on "+host)
			if err != nil {
				return err
			}
			cfg, _ := client.LoadConfig()
			cfg.Server, cfg.Token = server, tok
			path, err := client.SaveConfig(cfg)
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Signed in to %s as %s (%s, %s). Token saved in %s\n", server, me.Email, me.Role, me.OrgName, path)
			return nil
		},
	}
	cmd.Flags().StringVar(&server, "server", os.Getenv("STAMPEDE_SERVER"), "server URL (default http://localhost:8080)")
	cmd.Flags().StringVar(&email, "email", "", "account email")
	return cmd
}

type remoteRunFlags struct {
	project, scenarioRef, target, file, note string
	shape, rate, duration, start, max        string
	vus, workers                             int
	env                                      []string
	detach                                   bool
	md                                       string
}

func newStartCmd() *cobra.Command {
	f := &remoteRunFlags{}
	cmd := &cobra.Command{
		Use:   "start",
		Short: "Start a run on a Stampede server and follow it live",
		Long: `Start a run on the server you signed in to with stampede login. Pass a
scenario already saved on the server (--scenario) or a local file (--file),
which is saved as a new version first. Exits like stampede run: 0 pass,
3 targets failed.`,
		Example: `  stampede start --project shop --file checkout.yaml --target staging --shape spike
  stampede start --scenario shoplab-mix --target shoplab --duration 2m --detach`,
		RunE: func(cmd *cobra.Command, _ []string) error { return startRemote(cmd, f) },
	}
	fl := cmd.Flags()
	fl.StringVar(&f.project, "project", "", "project name, slug or id (default: the only project)")
	fl.StringVar(&f.scenarioRef, "scenario", "", "scenario name or id on the server")
	fl.StringVarP(&f.file, "file", "f", "", "local scenario file to save as a new version and run")
	fl.StringVar(&f.target, "target", "", "target name, base URL or id (default: the only target)")
	fl.StringVar(&f.shape, "shape", "", "traffic shape override")
	fl.StringVar(&f.rate, "rate", "", "arrival rate override, e.g. 100/s")
	fl.IntVar(&f.vus, "vus", 0, "virtual users override")
	fl.StringVar(&f.duration, "duration", "", "duration override")
	fl.StringVar(&f.start, "start", "", "shape start level override")
	fl.StringVar(&f.max, "max", "", "shape max level override")
	fl.IntVar(&f.workers, "workers", 0, "number of workers (0 = all)")
	fl.StringArrayVarP(&f.env, "env", "e", nil, "KEY=VALUE for ${env.KEY} (repeatable)")
	fl.StringVar(&f.note, "note", "", "note recorded with the run")
	fl.BoolVarP(&f.detach, "detach", "d", false, "print the run id and return without following")
	fl.StringVar(&f.md, "md", "", "write the report as Markdown when done (- for stdout)")
	return cmd
}

func startRemote(cmd *cobra.Command, f *remoteRunFlags) error {
	ctx := cmd.Context()
	c, err := client.New()
	if err != nil {
		return err
	}
	run, err := createRemoteRun(ctx, cmd.ErrOrStderr(), c, f, "pushed by stampede start")
	if err != nil {
		return err
	}
	if f.detach {
		fmt.Fprintln(cmd.OutOrStdout(), run.Id)
		return nil
	}
	return followRun(ctx, cmd, c, run.Id.String(), f.md)
}

// createRemoteRun starts a run on the server: the scenario named by
// f.scenarioRef, or f.file saved as a new version first, against the
// target named by f.target. It reports the start on errw.
func createRemoteRun(ctx context.Context, errw io.Writer, c *client.Client, f *remoteRunFlags, pushMsg string) (gen.Run, error) {
	proj, err := c.FindProject(ctx, f.project)
	if err != nil {
		return gen.Run{}, err
	}
	pid := proj.Id.String()
	var sc gen.Scenario
	if f.file != "" {
		sc, err = pushScenario(ctx, c, pid, f.file, pushMsg)
	} else {
		sc, err = c.FindScenario(ctx, pid, f.scenarioRef)
	}
	if err != nil {
		return gen.Run{}, err
	}
	tgt, err := c.FindTarget(ctx, pid, f.target)
	if err != nil {
		return gen.Run{}, err
	}
	body := map[string]any{"scenarioId": sc.Id, "targetId": tgt.Id, "workers": f.workers}
	if ov := overridesFrom(f.shape, f.rate, f.duration, f.start, f.max, f.vus); len(ov) > 0 {
		body["overrides"] = ov
	}
	env, err := envFrom(f.env)
	if err != nil {
		return gen.Run{}, err
	}
	if len(env) > 0 {
		body["env"] = env
	}
	if f.note != "" {
		body["note"] = f.note
	}
	var run gen.Run
	if err := c.Do(ctx, "POST", "/projects/"+pid+"/runs", body, &run); err != nil {
		return gen.Run{}, err
	}
	if !f.detach {
		fmt.Fprintf(errw, "stampede: run %s of %s against %s started; Ctrl-C stops following (the run continues)\n", run.Id, sc.Name, tgt.BaseURL)
	}
	return run, nil
}

// followRun prints live progress, then the report, and maps the verdict to
// an exit code.
func followRun(ctx context.Context, cmd *cobra.Command, c *client.Client, runID, md string) error {
	return followRunWith(ctx, cmd, c, runID, false, func(rep *report.Report) error {
		rep.WriteText(cmd.OutOrStdout())
		return writeFile(cmd.OutOrStdout(), md, func(w io.Writer) error { rep.WriteMarkdown(w); return nil })
	})
}

// followRunWith follows a run like followRun and hands its report to
// write. quiet leaves out the per-second progress lines.
func followRunWith(ctx context.Context, cmd *cobra.Command, c *client.Client, runID string, quiet bool, write func(*report.Report) error) error {
	errw := cmd.ErrOrStderr()
	var final gen.Run
	err := c.Follow(ctx, runID, func(ev client.Event) {
		switch ev.Type {
		case "point":
			var p gen.Point
			if !quiet && json.Unmarshal(ev.Data, &p) == nil {
				fmt.Fprintf(errw, "  %5.0fs  vus %-5d rps %-7.0f p95 %-9s err %-7s dropped %d\n", p.T+1, p.Vus, p.Rps, report.Ms(p.P95), report.Pct(p.ErrorRate), p.Dropped)
			}
		case "status":
			_ = json.Unmarshal(ev.Data, &final)
			if final.Status != gen.Running && final.Status != gen.Completed {
				fmt.Fprintf(errw, "  status: %s\n", final.Status)
			}
		case "event":
			var e struct{ Message, Worker string }
			if json.Unmarshal(ev.Data, &e) == nil {
				fmt.Fprintf(errw, "  · %s\n", e.Message)
			}
		}
	})
	if err != nil {
		return err
	}
	if final.Status == gen.Failed {
		msg := "run failed"
		if final.Error != nil {
			msg += ": " + *final.Error
		}
		return errors.New(msg)
	}
	rep, err := fetchReport(ctx, c, runID)
	if err != nil {
		return err
	}
	if err := write(rep); err != nil {
		return err
	}
	if rep.Verdict == report.VerdictFail {
		return &exitError{code: ExitTargetsFailed, msg: "one or more targets failed"}
	}
	return nil
}

// fetchReport downloads a finished run's JSON report from the server.
func fetchReport(ctx context.Context, c *client.Client, runID string) (*report.Report, error) {
	var raw []byte
	if err := c.Do(ctx, "GET", "/runs/"+runID+"/report", nil, &raw); err != nil {
		return nil, err
	}
	return report.ReadJSON(bytes.NewReader(raw))
}

// pushScenario saves a local file as a new scenario or a new version of
// the scenario with the same name.
func pushScenario(ctx context.Context, c *client.Client, projectID, path, msg string) (gen.Scenario, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return gen.Scenario{}, err
	}
	s, err := scenario.Decode(b)
	if err != nil {
		return gen.Scenario{}, err
	}
	existing, err := c.FindScenario(ctx, projectID, s.Metadata.Name)
	if err == nil {
		var v gen.ScenarioVersion
		if err := c.Do(ctx, "POST", "/scenarios/"+existing.Id.String()+"/versions", map[string]string{"yaml": string(b), "message": msg}, &v); err != nil {
			return gen.Scenario{}, err
		}
		existing.LatestVersion = v
		return existing, nil
	}
	var sc gen.Scenario
	err = c.Do(ctx, "POST", "/projects/"+projectID+"/scenarios", map[string]string{"yaml": string(b), "message": msg}, &sc)
	return sc, err
}

func newPushCmd() *cobra.Command {
	var project, msg string
	cmd := &cobra.Command{
		Use:   "push <scenario.yaml>...",
		Short: "Save local scenario files to the server as new versions",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := client.New()
			if err != nil {
				return err
			}
			p, err := c.FindProject(cmd.Context(), project)
			if err != nil {
				return err
			}
			for _, path := range args {
				sc, err := pushScenario(cmd.Context(), c, p.Id.String(), path, msg)
				if err != nil {
					return fmt.Errorf("%s: %w", path, err)
				}
				fmt.Fprintf(cmd.OutOrStdout(), "%s → %s v%d\n", path, sc.Name, sc.LatestVersion.Version)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&project, "project", "", "project name, slug or id")
	cmd.Flags().StringVarP(&msg, "message", "m", "", "version message")
	return cmd
}

func newRunsCmd() *cobra.Command {
	var project string
	var limit int
	cmd := &cobra.Command{
		Use:   "runs",
		Short: "List recent runs on the server",
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := client.New()
			if err != nil {
				return err
			}
			p, err := c.FindProject(cmd.Context(), project)
			if err != nil {
				return err
			}
			var runs []gen.Run
			if err := c.Do(cmd.Context(), "GET", fmt.Sprintf("/projects/%s/runs?limit=%d", p.Id, limit), nil, &runs); err != nil {
				return err
			}
			tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
			fmt.Fprintln(tw, "RUN\tSCENARIO\tSTATUS\tVERDICT\tREQUESTS\tP95\tERRORS\tSTARTED")
			for _, r := range runs {
				verdict, reqs, p95, errs := "-", "-", "-", "-"
				if r.Verdict != nil {
					verdict = string(*r.Verdict)
				}
				if s := r.Summary; s != nil && s.Requests != nil {
					reqs = fmt.Sprint(*s.Requests)
					p95 = report.Ms(deref(s.P95))
					errs = report.Pct(deref(s.ErrorRate))
				}
				name := ""
				if r.ScenarioName != nil {
					name = fmt.Sprintf("%s v%d", *r.ScenarioName, r.ScenarioVersion)
				}
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n", r.Id.String()[:8], name, r.Status, verdict, reqs, p95, errs, r.CreatedAt.Local().Format("Jan 2 15:04"))
			}
			return tw.Flush()
		},
	}
	cmd.Flags().StringVar(&project, "project", "", "project name, slug or id")
	cmd.Flags().IntVar(&limit, "limit", 20, "how many runs")
	return cmd
}

func deref(f *float64) float64 {
	if f == nil {
		return 0
	}
	return *f
}

// resolveRun accepts a full id or a unique prefix of a recent run.
func resolveRun(ctx context.Context, c *client.Client, ref string) (string, error) {
	if len(ref) == 36 {
		return ref, nil
	}
	ps, err := c.Projects(ctx)
	if err != nil {
		return "", err
	}
	var match []string
	for _, p := range ps {
		var runs []gen.Run
		if err := c.Do(ctx, "GET", "/projects/"+p.Id.String()+"/runs?limit=200", nil, &runs); err != nil {
			return "", err
		}
		for _, r := range runs {
			if strings.HasPrefix(r.Id.String(), ref) {
				match = append(match, r.Id.String())
			}
		}
	}
	switch len(match) {
	case 1:
		return match[0], nil
	case 0:
		return "", fmt.Errorf("no run starts with %q", ref)
	default:
		return "", fmt.Errorf("%q matches %d runs; give more characters", ref, len(match))
	}
}

func newStopCmd(kill bool) *cobra.Command {
	var all bool
	use, short := "stop [run]", "Stop a run gracefully (in-flight iterations may finish)"
	if kill {
		use, short = "kill [run]", "Kill switch: stop a run's load immediately"
	}
	cmd := &cobra.Command{
		Use:   use,
		Short: short,
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := client.New()
			if err != nil {
				return err
			}
			ctx := cmd.Context()
			if all {
				var res struct{ Killed []string }
				if err := c.Do(ctx, "POST", "/runs/kill-all", nil, &res); err != nil {
					return err
				}
				fmt.Fprintf(cmd.OutOrStdout(), "killed %d run(s)\n", len(res.Killed))
				return nil
			}
			if len(args) != 1 {
				return errors.New("name a run, or use --all")
			}
			id, err := resolveRun(ctx, c, args[0])
			if err != nil {
				return err
			}
			action := "stop"
			if kill {
				action = "kill"
			}
			if err := c.Do(ctx, "POST", "/runs/"+id+"/"+action, nil, nil); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s requested for %s\n", action, id)
			return nil
		},
	}
	cmd.Flags().BoolVar(&all, "all", false, "kill every active run in the organisation (always immediate)")
	return cmd
}

func newWorkersCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "workers",
		Short: "List workers connected to the server",
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := client.New()
			if err != nil {
				return err
			}
			var ws []gen.Worker
			if err := c.Do(cmd.Context(), "GET", "/workers", nil, &ws); err != nil {
				return err
			}
			if len(ws) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "No workers connected; runs execute inside the server.")
				return nil
			}
			tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
			fmt.Fprintln(tw, "NAME\tREGION\tSTATUS\tCPUS\tPLUGINS\tLAST SEEN")
			for _, w := range ws {
				cpus := 0
				if w.Cpus != nil {
					cpus = *w.Cpus
				}
				plugins := "-"
				if w.Plugins != nil && len(*w.Plugins) > 0 {
					plugins = strings.Join(*w.Plugins, ",")
				}
				fmt.Fprintf(tw, "%s\t%s\t%s\t%d\t%s\t%s ago\n", w.Name, w.Region, w.Status, cpus, plugins, time.Since(w.LastSeenAt).Round(time.Second))
			}
			return tw.Flush()
		},
	}
}
