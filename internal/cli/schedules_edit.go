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

func newSchedulesShowCmd(project *string) *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "show <schedule>",
		Short: "Show a schedule in full",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			_, s, err := findSchedule(cmd.Context(), *project, args[0])
			if err != nil {
				return err
			}
			return show(cmd, asJSON, s, func(w io.Writer) error { return writeSchedule(w, s) })
		},
	}
	jsonFlag(cmd, &asJSON)
	return cmd
}

func writeSchedule(w io.Writer, s gen.Schedule) error {
	kind := "run"
	if s.Kind != nil {
		kind = string(*s.Kind)
	}
	fmt.Fprintf(w, "%s (%s schedule, id %s)\n", s.Name, kind, s.Id)
	fmt.Fprintf(w, "Cron:       %s (%s)\n", s.Cron, s.Timezone)
	fmt.Fprintf(w, "Scenario:   %s\n", or(s.ScenarioName, s.ScenarioId.String()))
	fmt.Fprintf(w, "Target:     %s\n", or(s.TargetName, s.TargetId.String()))
	if s.SpecURL != nil && *s.SpecURL != "" {
		fmt.Fprintf(w, "Spec URL:   %s\n", *s.SpecURL)
	}
	if ov := overridesText(s.Overrides); ov != "" {
		fmt.Fprintf(w, "Overrides:  %s\n", ov)
	}
	if s.Env != nil && len(*s.Env) > 0 {
		keys := make([]string, 0, len(*s.Env))
		for k := range *s.Env {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		var kv []string
		for _, k := range keys {
			kv = append(kv, k+"="+(*s.Env)[k])
		}
		fmt.Fprintf(w, "Env:        %s\n", strings.Join(kv, " "))
	}
	workers := "all"
	if s.Workers > 0 {
		workers = strconv.Itoa(s.Workers)
	}
	fmt.Fprintf(w, "Workers:    %s\n", workers)
	if n := or(s.Note, ""); n != "" {
		fmt.Fprintf(w, "Note:       %s\n", n)
	}
	fmt.Fprintf(w, "Owner:      %s (runs start as them)\n", or(s.OwnerEmail, "-"))
	next := "disabled"
	if s.NextRunAt != nil {
		next = s.NextRunAt.Local().Format(time.RFC1123)
	}
	fmt.Fprintf(w, "Next run:   %s\n", next)
	_, err := fmt.Fprintf(w, "Last:       %s\n", lastRun(s))
	return err
}

// overridesText lists a run's load overrides, e.g. "shape=soak duration=30m".
func overridesText(o *gen.RunOverrides) string {
	m := overridesMap(o)
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var parts []string
	for _, k := range keys {
		v := m[k]
		if k == "regions" {
			b, _ := json.Marshal(v)
			parts = append(parts, "regions="+string(b))
			continue
		}
		parts = append(parts, fmt.Sprintf("%s=%v", k, v))
	}
	return strings.Join(parts, " ")
}

func overridesMap(o *gen.RunOverrides) map[string]any {
	m := map[string]any{}
	if o == nil {
		return m
	}
	b, _ := json.Marshal(o)
	_ = json.Unmarshal(b, &m)
	return m
}

type scheduleUpdateFlags struct {
	scheduleFlags
	name                     string
	enabled                  bool
	clearOverrides, clearEnv bool
	unsetEnv                 []string
	asJSON                   bool
}

func newSchedulesUpdateCmd(project *string) *cobra.Command {
	f := &scheduleUpdateFlags{}
	cmd := &cobra.Command{
		Use:   "update <schedule>",
		Short: "Change a schedule; only the flags given change",
		Long: `Change any of a schedule's settings. Only the flags given change: load
overrides are merged into the current ones (--clear-overrides drops them
first), --env adds or replaces variables (--unset-env and --clear-env
remove them). The schedule is checked in full again, as when it was
created, and you become its owner, so later runs start as you.`,
		Example: `  stampede schedules update nightly --cron "0 3 * * *" --timezone Europe/Berlin
  stampede schedules update nightly --scenario checkout --duration 10m --env STAGE=ci
  stampede schedules update api-drift --spec-url https://staging.example.com/openapi.json`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			fl := cmd.Flags()
			c, s, err := findSchedule(ctx, *project, args[0])
			if err != nil {
				return err
			}
			body := map[string]any{}
			for flag, key := range map[string]string{"name": "name", "cron": "cron", "timezone": "timezone", "note": "note", "spec-url": "specURL"} {
				if fl.Changed(flag) {
					v, _ := fl.GetString(flag)
					body[key] = v
				}
			}
			if fl.Changed("workers") {
				body["workers"] = f.workers
			}
			if fl.Changed("enabled") {
				body["enabled"] = f.enabled
			}
			if fl.Changed("scenario") || fl.Changed("target") {
				if err := scheduleRefs(cmd, c, s, f, body); err != nil {
					return err
				}
			}
			ov := overridesMap(s.Overrides)
			if f.clearOverrides {
				ov = map[string]any{}
			}
			changedOv := f.clearOverrides
			for k, v := range overridesFrom(f.shape, f.rate, f.duration, f.start, f.max, f.vus) {
				ov[k], changedOv = v, true
			}
			if len(f.regions) > 0 {
				regions, err := regionsFrom(f.regions)
				if err != nil {
					return err
				}
				ov["regions"], changedOv = regions, true
			}
			if changedOv {
				body["overrides"] = ov
			}
			if len(f.env) > 0 || len(f.unsetEnv) > 0 || f.clearEnv {
				env := map[string]string{}
				if s.Env != nil && !f.clearEnv {
					for k, v := range *s.Env {
						env[k] = v
					}
				}
				add, err := envFrom(f.env)
				if err != nil {
					return err
				}
				for k, v := range add {
					env[k] = v
				}
				for _, k := range f.unsetEnv {
					delete(env, k)
				}
				body["env"] = env
			}
			if len(body) == 0 {
				return errors.New("nothing to change: give the settings to change as flags (see --help)")
			}
			if err := c.Do(ctx, "PATCH", "/schedules/"+s.Id.String(), body, &s); err != nil {
				return err
			}
			return show(cmd, f.asJSON, s, func(w io.Writer) error {
				fmt.Fprintf(w, "Updated schedule %s.\n", s.Name)
				return writeSchedule(w, s)
			})
		},
	}
	fl := cmd.Flags()
	fl.StringVar(&f.name, "name", "", "rename the schedule")
	fl.StringVar(&f.scenarioRef, "scenario", "", "run this scenario (name or id) instead")
	fl.StringVar(&f.target, "target", "", "run against this target (name, base URL or id) instead")
	fl.StringVar(&f.cron, "cron", "", `five-field cron expression, e.g. "0 2 * * *", or @hourly, @daily, @weekly`)
	fl.StringVar(&f.timezone, "timezone", "", "IANA time zone for the cron expression")
	fl.StringVar(&f.shape, "shape", "", "traffic shape override")
	fl.StringVar(&f.rate, "rate", "", "arrival rate override, e.g. 100/s")
	fl.IntVar(&f.vus, "vus", 0, "virtual users override")
	fl.StringVar(&f.duration, "duration", "", "duration override")
	fl.StringVar(&f.start, "start", "", "shape start level override")
	fl.StringVar(&f.max, "max", "", "shape max level override")
	fl.StringArrayVar(&f.regions, "region", nil, regionFlagHelp+" (replaces the regions)")
	fl.BoolVar(&f.clearOverrides, "clear-overrides", false, "drop the current overrides (before applying any given)")
	fl.IntVar(&f.workers, "workers", 0, "number of workers (0 = all)")
	fl.StringArrayVarP(&f.env, "env", "e", nil, "add or replace KEY=VALUE for ${env.KEY} (repeatable)")
	fl.StringArrayVar(&f.unsetEnv, "unset-env", nil, "remove an environment variable (repeatable)")
	fl.BoolVar(&f.clearEnv, "clear-env", false, "remove every environment variable (before adding any given)")
	fl.StringVar(&f.note, "note", "", "description of the schedule")
	fl.BoolVar(&f.enabled, "enabled", true, "enable (--enabled) or disable (--enabled=false) the schedule")
	fl.StringVar(&f.specURL, "spec-url", "", `drift schedules: OpenAPI document compared with the scenario on each check ("" removes it)`)
	jsonFlag(cmd, &f.asJSON)
	return cmd
}

// scheduleRefs resolves --scenario and --target into body.
func scheduleRefs(cmd *cobra.Command, c *client.Client, s gen.Schedule, f *scheduleUpdateFlags, body map[string]any) error {
	pid := s.ProjectId.String()
	if cmd.Flags().Changed("scenario") {
		sc, err := c.FindScenarioIn(cmd.Context(), pid, f.scenarioRef)
		if err != nil {
			return err
		}
		body["scenarioId"] = sc.Id
	}
	if cmd.Flags().Changed("target") {
		t, err := c.FindTarget(cmd.Context(), pid, f.target)
		if err != nil {
			return err
		}
		body["targetId"] = t.Id
	}
	return nil
}

func newSchedulesPreviewCmd() *cobra.Command {
	var cron, tz string
	var count int
	var asJSON bool
	cmd := &cobra.Command{
		Use:     "preview",
		Short:   "Show the next times a cron expression fires",
		Example: `  stampede schedules preview --cron "30 6 * * MON-FRI" --timezone Europe/London --count 5`,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if cron == "" {
				return errors.New(`--cron is required, e.g. --cron "0 2 * * *"`)
			}
			c, err := client.New()
			if err != nil {
				return err
			}
			var p gen.SchedulePreview
			if err := c.Do(cmd.Context(), "GET", "/schedules/preview"+client.Query("cron", cron, "timezone", tz, "count", strconv.Itoa(count)), nil, &p); err != nil {
				return err
			}
			return show(cmd, asJSON, p, func(w io.Writer) error {
				fmt.Fprintf(w, "%q in %s fires next at:\n", cron, p.Timezone)
				for _, t := range p.Next {
					fmt.Fprintf(w, "  %s\n", t.Format("Mon 2 Jan 2006 15:04 MST"))
				}
				return nil
			})
		},
	}
	cmd.Flags().StringVar(&cron, "cron", "", `cron expression, e.g. "0 2 * * *" or @daily`)
	cmd.Flags().StringVar(&tz, "timezone", "", "IANA time zone (default UTC)")
	cmd.Flags().IntVar(&count, "count", 3, "how many firings, 1 to 20")
	jsonFlag(cmd, &asJSON)
	return cmd
}
