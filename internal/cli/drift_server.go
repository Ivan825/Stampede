package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Ivan825/Stampede/internal/api/gen"
	"github.com/Ivan825/Stampede/internal/client"
)

// serverSpecFlags select a scenario saved on the server and the API to
// compare it with, for stampede coverage and stampede drift.
type serverSpecFlags struct {
	project, scenarioRef string
	version              int
	specURL              string
	prevSpecURL          string
}

func (f *serverSpecFlags) register(cmd *cobra.Command, drift bool) {
	fl := cmd.Flags()
	fl.StringVar(&f.scenarioRef, "scenario", "", "check a scenario saved on the server (name or id) instead of a file")
	fl.StringVar(&f.project, "project", "", "with --scenario: project name, slug or id (default: the default or only project)")
	fl.IntVar(&f.version, "version", 0, "with --scenario: the scenario version to check (default the latest)")
	fl.StringVar(&f.specURL, "spec-url", "", "with --scenario: have the server fetch the OpenAPI document from this URL on a target's host")
	if drift {
		fl.StringVar(&f.prevSpecURL, "previous-spec-url", "", "with --scenario: have the server fetch the previous OpenAPI document from this URL")
	}
}

// check refuses flags that only work on local files, and the server-only
// flags without --scenario.
func (f *serverSpecFlags) check(cmd *cobra.Command, args []string, localOnly []string) error {
	fl := cmd.Flags()
	if f.scenarioRef == "" {
		for _, n := range []string{"project", "version", "spec-url", "previous-spec-url"} {
			if fl.Lookup(n) != nil && fl.Changed(n) {
				return fmt.Errorf("--%s applies only with --scenario", n)
			}
		}
		if len(args) != 1 {
			return errors.New("give a scenario file, or a scenario saved on the server with --scenario")
		}
		return nil
	}
	if len(args) > 0 {
		return errors.New("give a scenario file or --scenario, not both")
	}
	for _, n := range localOnly {
		if fl.Changed(n) {
			return fmt.Errorf("--%s applies only to scenario files; with --scenario give the API with --from-openapi or --spec-url", n)
		}
	}
	return nil
}

// body builds a coverage or drift request.
func (f *serverSpecFlags) body(openapi, previous string) (map[string]any, error) {
	b := map[string]any{}
	if f.version > 0 {
		b["version"] = f.version
	}
	for key, path := range map[string]string{"openapi": openapi, "previousOpenapi": previous} {
		if path == "" {
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		b[key] = string(data)
	}
	if f.specURL != "" {
		b["specURL"] = f.specURL
	}
	if f.prevSpecURL != "" {
		b["previousSpecURL"] = f.prevSpecURL
	}
	if b["openapi"] == nil && b["specURL"] == nil {
		return nil, errors.New("give the API with --from-openapi (a file) or --spec-url (fetched by the server)")
	}
	return b, nil
}

func (f *serverSpecFlags) scenario(cmd *cobra.Command) (*client.Client, string, gen.Scenario, error) {
	c, pid, err := projectClient(cmd.Context(), f.project)
	if err != nil {
		return nil, "", gen.Scenario{}, err
	}
	s, err := c.FindScenarioIn(cmd.Context(), pid, f.scenarioRef)
	return c, pid, s, err
}

func serverCoverage(cmd *cobra.Command, f *serverSpecFlags, openapi string, asJSON bool) error {
	body, err := f.body(openapi, "")
	if err != nil {
		return err
	}
	c, _, s, err := f.scenario(cmd)
	if err != nil {
		return err
	}
	var cov gen.ScenarioCoverage
	if err := c.Do(cmd.Context(), "POST", "/scenarios/"+s.Id.String()+"/coverage", body, &cov); err != nil {
		return err
	}
	return show(cmd, asJSON, cov, func(w io.Writer) error {
		pct := 0.0
		if cov.Total > 0 {
			pct = float64(cov.Covered) * 100 / float64(cov.Total)
		}
		fmt.Fprintf(w, "\n  %s v%d: %d of %d endpoints covered (%.0f%%)\n\n", s.Name, cov.Version, cov.Covered, cov.Total, pct)
		for _, e := range cov.Endpoints {
			mark, by := "✗", "no journey"
			if len(e.Journeys) > 0 {
				mark, by = "✓", strings.Join(e.Journeys, ", ")
			}
			fmt.Fprintf(w, "  %s %-7s %-44s %s\n", mark, e.Method, e.Path, by)
		}
		if len(cov.Unmatched) > 0 {
			fmt.Fprintf(w, "\n  requests that match no endpoint\n")
			for _, r := range cov.Unmatched {
				fmt.Fprintf(w, "    %-7s %-44s in %s\n", r.Method, r.Url, r.Journey)
			}
		}
		if cov.Templated > 0 {
			fmt.Fprintf(w, "\n  %d requests have a fully templated URL and were not matched\n", cov.Templated)
		}
		_, err := fmt.Fprintln(w)
		return err
	})
}

func serverDrift(cmd *cobra.Command, f *serverSpecFlags, openapi, previous, target string, asJSON bool) error {
	body, err := f.body(openapi, previous)
	if err != nil {
		return err
	}
	c, pid, s, err := f.scenario(cmd)
	if err != nil {
		return err
	}
	if target != "" {
		t, err := c.FindTarget(cmd.Context(), pid, target)
		if err != nil {
			return err
		}
		body["targetId"] = t.Id
	}
	var d gen.ScenarioDrift
	if err := c.Do(cmd.Context(), "POST", "/scenarios/"+s.Id.String()+"/drift", body, &d); err != nil {
		return err
	}
	err = show(cmd, asJSON, d, func(w io.Writer) error {
		fmt.Fprintf(w, "\n  %s v%d\n", s.Name, d.Version)
		if d.Removed != nil || d.Added != nil {
			fmt.Fprintf(w, "  API change: %d endpoints added, %d removed\n", lenOf(d.Added), lenOf(d.Removed))
			if d.Removed != nil {
				for _, e := range *d.Removed {
					fmt.Fprintf(w, "    - %s %s\n", e.Method, e.Path)
				}
			}
			if d.Added != nil {
				for _, e := range *d.Added {
					fmt.Fprintf(w, "    + %s %s\n", e.Method, e.Path)
				}
			}
		}
		if d.Broken != nil {
			for _, b := range *d.Broken {
				var keys []string
				for _, e := range b.Endpoints {
					keys = append(keys, e.Method+" "+e.Path)
				}
				fmt.Fprintf(w, "  ✗ journey %s calls removed endpoints: %s\n", b.Journey, strings.Join(keys, ", "))
			}
		}
		for _, r := range d.Unmatched {
			fmt.Fprintf(w, "  ✗ %s: %s %s is not an endpoint of the current API\n", r.Journey, r.Method, r.Url)
		}
		if d.DryRun != nil {
			for _, j := range *d.DryRun {
				if j.Ok {
					fmt.Fprintf(w, "  ✓ %s passes its dry run\n", j.Journey)
					continue
				}
				fmt.Fprintf(w, "  ✗ %s fails its dry run", j.Journey)
				if j.Step != nil {
					fmt.Fprintf(w, " (step %s", *j.Step)
					if j.Error != nil && *j.Error != "" {
						fmt.Fprintf(w, ": %s", *j.Error)
					} else if j.Status != nil {
						fmt.Fprintf(w, ": status %d", *j.Status)
					}
					fmt.Fprint(w, ")")
				} else if j.Error != nil {
					fmt.Fprintf(w, ": %s", *j.Error)
				}
				fmt.Fprintln(w)
			}
		}
		if !d.Drifted {
			fmt.Fprintln(w, "  no drift found")
		}
		_, err := fmt.Fprintln(w)
		return err
	})
	if err == nil && d.Drifted {
		err = &exitError{code: ExitDrift, msg: "the scenario drifted from the API"}
	}
	return err
}

func lenOf[T any](p *[]T) int {
	if p == nil {
		return 0
	}
	return len(*p)
}

// newDriftResultsCmds are the drift subcommands for checks run by drift
// schedules on the server.
func newDriftResultsCmds() []*cobra.Command {
	var project string
	withProject := func(c *cobra.Command) *cobra.Command {
		c.Flags().StringVar(&project, "project", "", "project name, slug or id (default: the default or only project)")
		return c
	}

	var schedule string
	var limit int
	var asJSON bool
	results := withProject(&cobra.Command{
		Use:   "results",
		Short: "List the results of a project's scheduled drift checks, newest first",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			c, pid, err := projectClient(ctx, project)
			if err != nil {
				return err
			}
			sid := ""
			if schedule != "" {
				_, s, err := findSchedule(ctx, project, schedule)
				if err != nil {
					return err
				}
				sid = s.Id.String()
			}
			var ds []gen.DriftResult
			if err := c.Do(ctx, "GET", "/projects/"+pid+"/drift-results"+client.Query("scheduleId", sid, "limit", strconv.Itoa(limit)), nil, &ds); err != nil {
				return err
			}
			return show(cmd, asJSON, ds, func(w io.Writer) error {
				if len(ds) == 0 {
					_, err := fmt.Fprintln(w, "No drift checks yet. Create a drift schedule with stampede schedules create <name> --kind drift.")
					return err
				}
				var rows [][]string
				for _, d := range ds {
					broken := strings.Join(d.Broken, ",")
					if broken == "" {
						broken = "-"
					}
					repair := "-"
					if d.RepairJobId != nil {
						repair = d.RepairJobId.String()[:8]
					}
					rows = append(rows, []string{d.Id.String()[:8], string(d.Status), or(d.ScheduleName, "-"), fmt.Sprintf("%s v%d", or(d.ScenarioName, "?"), d.ScenarioVersion), broken, repair, stamp(&d.CreatedAt)})
				}
				return table(w, "CHECK\tSTATUS\tSCHEDULE\tSCENARIO\tBROKEN\tREPAIR JOB\tWHEN", rows)
			})
		},
	})
	results.Flags().StringVar(&schedule, "schedule", "", "only the checks of this schedule")
	results.Flags().IntVar(&limit, "limit", 50, "how many results (at most 200)")
	jsonFlag(results, &asJSON)

	var showJSON bool
	showCmd := withProject(&cobra.Command{
		Use:   "show <check>",
		Short: "Show one scheduled drift check: each journey's dry run and the spec diff",
		Long: `Show a drift check run by a drift schedule. --json includes each journey's
redacted dry-run traces.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			c, pid, err := projectClient(ctx, project)
			if err != nil {
				return err
			}
			d, err := c.FindDriftResult(ctx, pid, args[0])
			if err != nil {
				return err
			}
			if err := c.Do(ctx, "GET", "/drift-results/"+d.Id.String(), nil, &d); err != nil {
				return err
			}
			return show(cmd, showJSON, d, func(w io.Writer) error { return printDriftResult(w, d) })
		},
	})
	jsonFlag(showCmd, &showJSON)

	var provider string
	var maxRepairs int
	var wait, repairJSON bool
	repair := withProject(&cobra.Command{
		Use:   "repair <check>",
		Short: "Ask the AI generator to repair the journeys a drift check found broken",
		Long: `Start an AI job that repairs the broken journeys of a drift check, with the
scenario as the starting point and the dry-run evidence (redacted) as the
task. Prints the job id; follow it with stampede ai jobs show <job> and
save the proposal with stampede ai jobs approve <job>. Nothing changes
until then. Needs an AI provider (stampede ai providers set) and the
editor role.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			c, pid, err := projectClient(ctx, project)
			if err != nil {
				return err
			}
			d, err := c.FindDriftResult(ctx, pid, args[0])
			if err != nil {
				return err
			}
			body := map[string]any{}
			if provider != "" {
				p, err := c.FindAIProvider(ctx, provider)
				if err != nil {
					return err
				}
				body["providerId"] = p.Id
			}
			if cmd.Flags().Changed("max-repairs") {
				body["maxRepairs"] = maxRepairs
			}
			var job gen.AIJob
			if err := c.Do(ctx, "POST", "/drift-results/"+d.Id.String()+"/repair", body, &job); err != nil {
				return err
			}
			if wait {
				if job, err = waitAIJob(cmd, c, job.Id.String()); err != nil {
					return err
				}
				return show(cmd, repairJSON, job, func(w io.Writer) error { return writeAIJob(w, job, false) })
			}
			if repairJSON {
				return writeJSON(cmd.OutOrStdout(), job)
			}
			fmt.Fprintln(cmd.OutOrStdout(), job.Id)
			fmt.Fprintf(cmd.ErrOrStderr(), "stampede: repair job started; follow it with stampede ai jobs show %s --wait\n", job.Id.String()[:8])
			return nil
		},
	})
	repair.Flags().StringVar(&provider, "provider", "", "AI provider name or id (default: the only one, or the one named default)")
	repair.Flags().IntVar(&maxRepairs, "max-repairs", 3, "repair rounds, 0 to 3")
	repair.Flags().BoolVar(&wait, "wait", false, "wait for the job to finish and show it")
	jsonFlag(repair, &repairJSON)
	return []*cobra.Command{results, showCmd, repair}
}

// printDriftResult prints a server drift check.
func printDriftResult(w io.Writer, r gen.DriftResult) error {
	fmt.Fprintf(w, "Drift check %s of %s v%d against %s: %s\n", r.Id, deref0(r.ScenarioName), r.ScenarioVersion, deref0(r.TargetURL), r.Status)
	if r.ScheduleName != nil {
		fmt.Fprintf(w, "  schedule %s, %s\n", *r.ScheduleName, stamp(&r.CreatedAt))
	}
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
	if r.AddedEndpoints != nil {
		for _, e := range *r.AddedEndpoints {
			fmt.Fprintf(w, "  + %s was added to the API\n", e)
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
	if r.RepairJobId != nil {
		fmt.Fprintf(w, "Repair job: %s (stampede ai jobs show %s)\n", r.RepairJobId, r.RepairJobId.String()[:8])
	} else if r.Status == gen.DriftDrifted {
		fmt.Fprintf(w, "Propose a repair with stampede drift repair %s (an AI provider is needed).\n", r.Id.String()[:8])
	}
	return nil
}
