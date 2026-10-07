package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/Ivan825/Stampede/internal/api/gen"
	"github.com/Ivan825/Stampede/internal/client"
)

func newSchedulesCmd() *cobra.Command {
	var project string
	cmd := &cobra.Command{
		Use:     "schedules",
		Aliases: []string{"schedule"},
		Short:   "List and manage scheduled runs on the server",
		Long: `Schedules start a run of a saved scenario against a target on a cron
expression, such as "0 2 * * *" for 02:00 every day. Times are UTC unless
--timezone names an IANA zone. Runs start as the schedule's owner: whoever
created or last changed it. Creating, changing and deleting schedules
needs the editor role; starting one by hand needs runner.`,
	}
	cmd.PersistentFlags().StringVar(&project, "project", "", "project name, slug or id (default: the only project)")
	cmd.AddCommand(
		newSchedulesListCmd(&project),
		newSchedulesCreateCmd(&project),
		newSchedulesToggleCmd(&project, true),
		newSchedulesToggleCmd(&project, false),
		newSchedulesDeleteCmd(&project),
		newSchedulesRunCmd(&project),
	)
	return cmd
}

func newSchedulesListCmd(project *string) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List a project's schedules",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, pid, err := projectClient(cmd.Context(), *project)
			if err != nil {
				return err
			}
			var ss []gen.Schedule
			if err := c.Do(cmd.Context(), "GET", "/projects/"+pid+"/schedules", nil, &ss); err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if len(ss) == 0 {
				fmt.Fprintln(out, "No schedules. Create one with stampede schedules create.")
				return nil
			}
			tw := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
			fmt.Fprintln(tw, "NAME\tKIND\tCRON\tTIMEZONE\tSCENARIO\tTARGET\tNEXT RUN\tLAST RUN")
			for _, s := range ss {
				next := "disabled"
				if s.NextRunAt != nil {
					next = s.NextRunAt.Local().Format("Jan 2 15:04")
				}
				kind := "run"
				if s.Kind != nil {
					kind = string(*s.Kind)
				}
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n", s.Name, kind, s.Cron, s.Timezone, deref0(s.ScenarioName), deref0(s.TargetName), next, lastRun(s))
			}
			return tw.Flush()
		},
	}
}

func deref0(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// lastRun summarises a schedule's last firing.
func lastRun(s gen.Schedule) string {
	if s.LastSkipReason != "" {
		return "skipped: " + s.LastSkipReason
	}
	if s.Kind != nil && *s.Kind == gen.ScheduleKindDrift {
		if s.LastDriftStatus == nil {
			return "-"
		}
		out := *s.LastDriftStatus
		if s.LastDriftBroken != nil && len(*s.LastDriftBroken) > 0 {
			out += " (" + strings.Join(*s.LastDriftBroken, ", ") + ")"
		}
		if s.LastFiredAt != nil {
			out += " " + s.LastFiredAt.Local().Format("Jan 2 15:04")
		}
		return out
	}
	if s.LastRunStatus == nil {
		return "-"
	}
	out := string(*s.LastRunStatus)
	if s.LastRunVerdict != nil {
		out += " (" + *s.LastRunVerdict + ")"
	}
	if s.LastRunAt != nil {
		out += " " + s.LastRunAt.Local().Format("Jan 2 15:04")
	}
	return out
}

type scheduleFlags struct {
	scenarioRef, target, cron, timezone, note string
	shape, rate, duration, start, max         string
	vus, workers                              int
	env, regions                              []string
	disabled                                  bool
	kind, specURL                             string
}

func newSchedulesCreateCmd(project *string) *cobra.Command {
	f := &scheduleFlags{}
	cmd := &cobra.Command{
		Use:   "create <name>",
		Short: "Create a schedule",
		Example: `  stampede schedules create nightly --scenario checkout --target staging --cron "0 2 * * *"
  stampede schedules create weekday-soak --scenario browse --target staging \
    --cron "30 6 * * MON-FRI" --timezone Europe/London --shape soak --duration 30m
  stampede schedules create api-drift --kind drift --scenario checkout --target staging \
    --cron "0 6 * * *" --spec-url https://staging.example.com/openapi.json`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			c, pid, err := projectClient(ctx, *project)
			if err != nil {
				return err
			}
			if f.cron == "" {
				return fmt.Errorf("--cron is required, e.g. --cron \"0 2 * * *\"")
			}
			sc, err := c.FindScenario(ctx, pid, f.scenarioRef)
			if err != nil {
				return err
			}
			tgt, err := c.FindTarget(ctx, pid, f.target)
			if err != nil {
				return err
			}
			body := map[string]any{"name": args[0], "scenarioId": sc.Id, "targetId": tgt.Id, "cron": f.cron, "workers": f.workers, "enabled": !f.disabled}
			if f.timezone != "" {
				body["timezone"] = f.timezone
			}
			if f.kind != "" {
				body["kind"] = f.kind
			}
			if f.specURL != "" {
				body["specURL"] = f.specURL
			}
			regions, err := regionsFrom(f.regions)
			if err != nil {
				return err
			}
			ov := overridesFrom(f.shape, f.rate, f.duration, f.start, f.max, f.vus)
			if regions != nil {
				ov["regions"] = regions
			}
			if len(ov) > 0 {
				body["overrides"] = ov
			}
			env, err := envFrom(f.env)
			if err != nil {
				return err
			}
			if len(env) > 0 {
				body["env"] = env
			}
			if f.note != "" {
				body["note"] = f.note
			}
			var s gen.Schedule
			if err := c.Do(ctx, "POST", "/projects/"+pid+"/schedules", body, &s); err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "Created schedule %s (%s, %s).\n", s.Name, s.Cron, s.Timezone)
			if s.NextRunAt != nil {
				fmt.Fprintf(out, "Next run: %s\n", s.NextRunAt.Local().Format(time.RFC1123))
			} else {
				fmt.Fprintln(out, "It is disabled; enable it with stampede schedules enable "+s.Name)
			}
			return nil
		},
	}
	fl := cmd.Flags()
	fl.StringVar(&f.scenarioRef, "scenario", "", "scenario name or id on the server")
	fl.StringVar(&f.target, "target", "", "target name, base URL or id (default: the only target)")
	fl.StringVar(&f.cron, "cron", "", `five-field cron expression, e.g. "0 2 * * *", or @hourly, @daily, @weekly`)
	fl.StringVar(&f.timezone, "timezone", "", "IANA time zone for the cron expression (default UTC)")
	fl.StringVar(&f.shape, "shape", "", "traffic shape override")
	fl.StringVar(&f.rate, "rate", "", "arrival rate override, e.g. 100/s")
	fl.IntVar(&f.vus, "vus", 0, "virtual users override")
	fl.StringVar(&f.duration, "duration", "", "duration override")
	fl.StringVar(&f.start, "start", "", "shape start level override")
	fl.StringVar(&f.max, "max", "", "shape max level override")
	fl.IntVar(&f.workers, "workers", 0, "number of workers (0 = all)")
	fl.StringArrayVarP(&f.env, "env", "e", nil, "KEY=VALUE for ${env.KEY} (repeatable; stored with the schedule, so use secrets for sensitive values)")
	fl.StringVar(&f.note, "note", "", "description of the schedule")
	fl.BoolVar(&f.disabled, "disabled", false, "create it disabled")
	fl.StringArrayVar(&f.regions, "region", nil, regionFlagHelp)
	fl.StringVar(&f.kind, "kind", "run", "run (start a load test) or drift (dry-run each journey once and record what broke; no load)")
	fl.StringVar(&f.specURL, "spec-url", "", "with --kind drift: OpenAPI document on the target to compare the scenario with on each check")
	return cmd
}

func newSchedulesToggleCmd(project *string, enable bool) *cobra.Command {
	use, short := "disable <schedule>", "Stop a schedule firing until it is enabled again"
	if enable {
		use, short = "enable <schedule>", "Let a schedule fire again (you become its owner)"
	}
	return &cobra.Command{
		Use:   use,
		Short: short,
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			c, s, err := findSchedule(ctx, *project, args[0])
			if err != nil {
				return err
			}
			if err := c.Do(ctx, "PATCH", "/schedules/"+s.Id.String(), map[string]any{"enabled": enable}, &s); err != nil {
				return err
			}
			if enable {
				fmt.Fprintf(cmd.OutOrStdout(), "Enabled %s. Next run: %s\n", s.Name, s.NextRunAt.Local().Format(time.RFC1123))
			} else {
				fmt.Fprintf(cmd.OutOrStdout(), "Disabled %s.\n", s.Name)
			}
			return nil
		},
	}
}

func newSchedulesDeleteCmd(project *string) *cobra.Command {
	return &cobra.Command{
		Use:   "delete <schedule>",
		Short: "Delete a schedule (the runs it started are kept)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			c, s, err := findSchedule(ctx, *project, args[0])
			if err != nil {
				return err
			}
			if err := c.Do(ctx, "DELETE", "/schedules/"+s.Id.String(), nil, nil); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Deleted %s.\n", s.Name)
			return nil
		},
	}
}

func newSchedulesRunCmd(project *string) *cobra.Command {
	var follow bool
	var md string
	cmd := &cobra.Command{
		Use:   "run <schedule>",
		Short: "Start a schedule's run now, as you",
		Long: `Start a schedule's run now, without changing when it next fires. Prints
the run id; with --follow, follows it live and exits like stampede start.

For a drift schedule, runs its drift check now and prints the result:
each journey's dry run, endpoints removed from the spec and requests that
match no endpoint. Exit code 4 when something drifted.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			c, s, err := findSchedule(ctx, *project, args[0])
			if err != nil {
				return err
			}
			if s.Kind != nil && *s.Kind == gen.ScheduleKindDrift {
				var res gen.DriftResult
				if err := c.Do(ctx, "POST", "/schedules/"+s.Id.String()+"/run", nil, &res); err != nil {
					return err
				}
				return writeDriftResult(cmd.OutOrStdout(), res)
			}
			var run gen.Run
			if err := c.Do(ctx, "POST", "/schedules/"+s.Id.String()+"/run", nil, &run); err != nil {
				return err
			}
			if !follow {
				fmt.Fprintln(cmd.OutOrStdout(), run.Id)
				return nil
			}
			fmt.Fprintf(cmd.ErrOrStderr(), "stampede: run %s of schedule %s started; Ctrl-C stops following (the run continues)\n", run.Id, s.Name)
			return followRun(ctx, cmd, c, run.Id.String(), md)
		},
	}
	cmd.Flags().BoolVarP(&follow, "follow", "f", false, "follow the run live and print its report")
	cmd.Flags().StringVar(&md, "md", "", "with --follow, write the report as Markdown when done (- for stdout)")
	return cmd
}

func projectClient(ctx context.Context, project string) (*client.Client, string, error) {
	c, err := client.New()
	if err != nil {
		return nil, "", err
	}
	p, err := c.FindProject(ctx, project)
	if err != nil {
		return nil, "", err
	}
	return c, p.Id.String(), nil
}

// findSchedule resolves a schedule in a project by name or id.
func findSchedule(ctx context.Context, project, ref string) (*client.Client, gen.Schedule, error) {
	c, pid, err := projectClient(ctx, project)
	if err != nil {
		return nil, gen.Schedule{}, err
	}
	var ss []gen.Schedule
	if err := c.Do(ctx, "GET", "/projects/"+pid+"/schedules", nil, &ss); err != nil {
		return nil, gen.Schedule{}, err
	}
	for _, s := range ss {
		if s.Id.String() == ref || strings.EqualFold(s.Name, ref) {
			return c, s, nil
		}
	}
	return nil, gen.Schedule{}, fmt.Errorf("schedule %q not found in the project", ref)
}

// overridesFrom builds a run's overrides from flags, leaving out unset ones.
func overridesFrom(shape, rate, duration, start, max string, vus int) map[string]any {
	ov := map[string]any{}
	for k, v := range map[string]string{"shape": shape, "rate": rate, "duration": duration, "start": start, "max": max} {
		if v != "" {
			ov[k] = v
		}
	}
	if vus > 0 {
		ov["vus"] = vus
	}
	return ov
}

// envFrom parses repeated KEY=VALUE flags.
func envFrom(kvs []string) (map[string]string, error) {
	env := map[string]string{}
	for _, kv := range kvs {
		k, v, ok := strings.Cut(kv, "=")
		if !ok {
			return nil, fmt.Errorf("--env %q: use KEY=VALUE", kv)
		}
		env[k] = v
	}
	return env, nil
}

// writeDriftResult prints a server drift check and maps it to the exit
// code of stampede drift.
func writeDriftResult(w io.Writer, r gen.DriftResult) error {
	fmt.Fprintf(w, "Drift check %s of %s against %s: %s\n", r.Id, deref0(r.ScenarioName), deref0(r.TargetURL), r.Status)
	if r.Journeys != nil {
		for _, j := range *r.Journeys {
			if j.Ok {
				fmt.Fprintf(w, "  ✓ %s passes its dry run\n", j.Journey)
			} else {
				fmt.Fprintf(w, "  ✗ %s fails its dry run: %s\n", j.Journey, deref0(j.Problem))
			}
		}
	}
	if r.RemovedEndpoints != nil {
		for _, e := range *r.RemovedEndpoints {
			fmt.Fprintf(w, "  - %s was removed from the API\n", e)
		}
	}
	if r.Unmatched != nil {
		for _, u := range *r.Unmatched {
			fmt.Fprintf(w, "  ✗ %s is not an endpoint of the current API\n", u)
		}
	}
	if r.Error != nil {
		fmt.Fprintf(w, "  ! %s\n", *r.Error)
	}
	switch r.Status {
	case gen.DriftDrifted:
		fmt.Fprintf(w, "Propose a repair with POST /api/v1/drift-results/%s/repair (an AI provider is needed).\n", r.Id)
		return &exitError{code: ExitDrift, msg: "the scenario drifted from the API: " + strings.Join(r.Broken, ", ")}
	case gen.DriftError:
		return errors.New("the drift check could not run")
	}
	return nil
}
