package cli

import (
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/Ivan825/Stampede/internal/api/gen"
	"github.com/Ivan825/Stampede/internal/client"
	"github.com/Ivan825/Stampede/internal/report"
)

func newRunsCmd() *cobra.Command {
	var project string
	list := newRunsListCmd(&project)
	cmd := &cobra.Command{
		Use:   "runs",
		Short: "List runs on the server, and show a run's details, events, timeline and workers",
		Long: `With no subcommand, list a project's recent runs (as stampede runs list).
Runs are named by id or by a unique start of it, as printed by the list.
stampede report <run> prints or downloads a finished run's report.`,
		Args: cobra.NoArgs,
		RunE: list.RunE,
	}
	cmd.PersistentFlags().StringVar(&project, "project", "", "project name, slug or id (default: the default or only project)")
	cmd.Flags().AddFlagSet(list.Flags())
	cmd.AddCommand(list, newRunShowCmd(), newRunFollowCmd(), newRunEventsCmd(), newRunTimelineCmd(), newRunWorkersCmd())
	return cmd
}

func newRunsListCmd(project *string) *cobra.Command {
	var limit int
	var scenarioRef string
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List a project's recent runs, newest first",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			c, pid, err := projectClient(ctx, *project)
			if err != nil {
				return err
			}
			sid := ""
			if scenarioRef != "" {
				s, err := c.FindScenarioIn(ctx, pid, scenarioRef)
				if err != nil {
					return err
				}
				sid = s.Id.String()
			}
			var runs []gen.Run
			if err := c.Do(ctx, "GET", "/projects/"+pid+"/runs"+client.Query("limit", strconv.Itoa(limit), "scenarioId", sid), nil, &runs); err != nil {
				return err
			}
			return show(cmd, asJSON, runs, func(w io.Writer) error {
				var rows [][]string
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
					rows = append(rows, []string{r.Id.String()[:8], name, string(r.Status), verdict, reqs, p95, errs, r.CreatedAt.Local().Format("Jan 2 15:04")})
				}
				return table(w, "RUN\tSCENARIO\tSTATUS\tVERDICT\tREQUESTS\tP95\tERRORS\tSTARTED", rows)
			})
		},
	}
	cmd.Flags().IntVar(&limit, "limit", 20, "how many runs (at most 200)")
	cmd.Flags().StringVar(&scenarioRef, "scenario", "", "only runs of this scenario")
	jsonFlag(cmd, &asJSON)
	return cmd
}

// runClient resolves a run reference for the run subcommands.
func runClient(cmd *cobra.Command, ref string) (*client.Client, string, error) {
	c, err := client.New()
	if err != nil {
		return nil, "", err
	}
	id, err := resolveRun(cmd.Context(), c, ref)
	return c, id, err
}

func newRunShowCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "show <run>",
		Short: "Show a run's status, load, summary and outcome",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, id, err := runClient(cmd, args[0])
			if err != nil {
				return err
			}
			var r gen.Run
			if err := c.Do(cmd.Context(), "GET", "/runs/"+id, nil, &r); err != nil {
				return err
			}
			return show(cmd, asJSON, r, func(w io.Writer) error { return writeRun(w, r) })
		},
	}
	jsonFlag(cmd, &asJSON)
	return cmd
}

func writeRun(w io.Writer, r gen.Run) error {
	fmt.Fprintf(w, "Run %s\n", r.Id)
	fmt.Fprintf(w, "Scenario:  %s v%d\n", or(r.ScenarioName, r.ScenarioId.String()), r.ScenarioVersion)
	fmt.Fprintf(w, "Target:    %s\n", or(r.TargetURL, r.TargetId.String()))
	status := string(r.Status)
	if r.Verdict != nil {
		status += ", verdict " + string(*r.Verdict)
	}
	fmt.Fprintf(w, "Status:    %s\n", status)
	when := "created " + r.CreatedAt.Local().Format(time.RFC1123)
	if r.StartedAt != nil {
		when = "started " + r.StartedAt.Local().Format(time.RFC1123)
		if r.EndedAt != nil {
			when += fmt.Sprintf(", ran %s", r.EndedAt.Sub(*r.StartedAt).Round(time.Second))
		}
	}
	fmt.Fprintf(w, "When:      %s\n", when)
	if r.Plan != nil {
		fmt.Fprintf(w, "Load:      %s\n", planText(r.Plan))
	}
	if r.Workers != nil {
		fmt.Fprintf(w, "Workers:   %d\n", *r.Workers)
	}
	if s := r.Summary; s != nil && s.Requests != nil {
		fmt.Fprintf(w, "Results:   %d requests, %.1f/s, p95 %s, p99 %s, errors %s\n", *s.Requests, deref(s.Rps), report.Ms(deref(s.P95)), report.Ms(deref(s.P99)), report.Pct(deref(s.ErrorRate)))
	}
	if v := or(r.CreatedBy, ""); v != "" {
		fmt.Fprintf(w, "By:        %s\n", v)
	}
	if v := or(r.Note, ""); v != "" {
		fmt.Fprintf(w, "Note:      %s\n", v)
	}
	if r.StopReason != nil && *r.StopReason != "" {
		fmt.Fprintf(w, "Stopped:   %s\n", *r.StopReason)
	}
	if r.Error != nil && *r.Error != "" {
		fmt.Fprintf(w, "Error:     %s\n", *r.Error)
	}
	if r.Status == gen.Completed || r.Status == gen.Aborted {
		fmt.Fprintf(w, "Report:    stampede report %s\n", r.Id.String()[:8])
	}
	return nil
}

func newRunFollowCmd() *cobra.Command {
	var md string
	cmd := &cobra.Command{
		Use:   "follow <run>",
		Short: "Follow a run live, then print its report",
		Long: `Follow a running run's live progress, as stampede start does, then print
its report. Ctrl-C stops following; the run continues. Exits like
stampede start: 0 pass, 3 targets failed. For a run that has already
finished, prints the report straight away.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, id, err := runClient(cmd, args[0])
			if err != nil {
				return err
			}
			return followRun(cmd.Context(), cmd, c, id, md)
		},
	}
	cmd.Flags().StringVar(&md, "md", "", "write the report as Markdown when done (- for stdout)")
	return cmd
}

func newRunEventsCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "events <run>",
		Short: "List a run's events: workers, safety stops, dry runs",
		Long: `Events recorded for a run, oldest first: workers joining or being lost,
safety stops, breakpoint confirmations and, when the project requires
one, the dry run before load.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, id, err := runClient(cmd, args[0])
			if err != nil {
				return err
			}
			var es []gen.RunEvent
			if err := c.Do(cmd.Context(), "GET", "/runs/"+id+"/events", nil, &es); err != nil {
				return err
			}
			return show(cmd, asJSON, es, func(w io.Writer) error {
				if len(es) == 0 {
					_, err := fmt.Fprintln(w, "No events.")
					return err
				}
				var rows [][]string
				for _, e := range es {
					rows = append(rows, []string{e.At.Local().Format("15:04:05.000"), e.Type, or(e.Worker, "-"), e.Message})
				}
				return table(w, "TIME\tTYPE\tWORKER\tMESSAGE", rows)
			})
		},
	}
	jsonFlag(cmd, &asJSON)
	return cmd
}

func newRunTimelineCmd() *cobra.Command {
	var resolution string
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "timeline <run>",
		Short: "Print a run's metrics over time",
		Long: `Print a run's points: per second by default, or rolled up per 10s or 1m
(requests summed, rps averaged, p50 the mean and p95/p99 the worst
per-second value in each bucket). Rollups outlast the per-second points
when the server keeps metrics for a limited time.`,
		Example: `  stampede runs timeline 3f2a91c0 --resolution 10s`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, id, err := runClient(cmd, args[0])
			if err != nil {
				return err
			}
			var ps []gen.Point
			if err := c.Do(cmd.Context(), "GET", "/runs/"+id+"/timeline"+client.Query("resolution", resolution), nil, &ps); err != nil {
				return err
			}
			return show(cmd, asJSON, ps, func(w io.Writer) error {
				if len(ps) == 0 {
					_, err := fmt.Fprintln(w, "No points recorded.")
					return err
				}
				var rows [][]string
				for _, p := range ps {
					rows = append(rows, []string{fmt.Sprintf("%.0fs", p.T), fmt.Sprintf("%.1f", p.Rps), report.Ms(p.P50), report.Ms(p.P95), report.Ms(p.P99),
						report.Pct(p.ErrorRate), strconv.Itoa(p.Vus), strconv.Itoa(p.Dropped)})
				}
				return table(w, "T\tRPS\tP50\tP95\tP99\tERRORS\tVUS\tDROPPED", rows)
			})
		},
	}
	cmd.Flags().StringVar(&resolution, "resolution", "1s", "1s, 10s or 1m")
	jsonFlag(cmd, &asJSON)
	return cmd
}

func newRunWorkersCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "workers <run>",
		Short: "Show the health of the load generators running a run",
		Long: `Each worker's latest self-monitoring while the run executes: CPU,
scheduling lag, whether it reports itself saturated (and so may be what
limits the measured load) and its last heartbeat. Empty once the run has
ended.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, id, err := runClient(cmd, args[0])
			if err != nil {
				return err
			}
			var rw gen.RunWorkers
			if err := c.Do(cmd.Context(), "GET", "/runs/"+id+"/workers", nil, &rw); err != nil {
				return err
			}
			return show(cmd, asJSON, rw, func(w io.Writer) error {
				if !rw.Live || len(rw.Workers) == 0 {
					_, err := fmt.Fprintln(w, "The run is not executing on this server now, so there is no live worker health.")
					return err
				}
				var rows [][]string
				for _, wk := range rw.Workers {
					status := string(wk.Status)
					if wk.Reasons != nil && len(*wk.Reasons) > 0 {
						status += " (" + strings.Join(*wk.Reasons, ", ") + ")"
					}
					dropped := "-"
					if wk.Dropped != nil {
						dropped = strconv.FormatInt(*wk.Dropped, 10)
					}
					hb := "-"
					if wk.LastHeartbeatAt != nil {
						hb = time.Since(*wk.LastHeartbeatAt).Round(time.Second).String() + " ago"
					}
					rows = append(rows, []string{wk.Name, or(wk.Region, "-"), status, fmt.Sprintf("%.0f%%", wk.CpuPercent), report.Ms(wk.SchedLagP99), dropped, hb})
				}
				return table(w, "WORKER\tREGION\tSTATUS\tCPU\tSCHED LAG P99\tDROPPED\tHEARTBEAT", rows)
			})
		},
	}
	jsonFlag(cmd, &asJSON)
	return cmd
}
