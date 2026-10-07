package cli

import (
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/Ivan825/Stampede/internal/api/gen"
	"github.com/Ivan825/Stampede/internal/client"
)

func newScenariosCmd() *cobra.Command {
	var project string
	cmd := &cobra.Command{
		Use:     "scenarios",
		Aliases: []string{"scenario"},
		Short:   "List, show and delete the scenarios saved on the server",
		Long: `Scenarios are saved on the server with every version kept. stampede push
saves local files as new versions; these commands read them back.
Scenarios are named by name or id (or a unique start of the id).`,
	}
	cmd.PersistentFlags().StringVar(&project, "project", "", "project name, slug or id (default: the default or only project)")
	cmd.AddCommand(newScenariosListCmd(&project), newScenariosShowCmd(&project), newScenariosVersionsCmd(&project),
		newScenariosDeleteCmd(&project), newScenariosValidateCmd())
	return cmd
}

// planText summarises a load plan, e.g. "constant-arrival-rate, peak 100/s, 5m0s, 2 journeys, 6 steps".
func planText(p *gen.PlanSummary) string {
	if p == nil {
		return "-"
	}
	parts := []string{p.Executor}
	if p.Shape != nil && *p.Shape != "" {
		parts = append(parts, *p.Shape)
	}
	unit := " VUs"
	if p.Mode == "rate" {
		unit = "/s"
	}
	parts = append(parts, "peak "+strconv.FormatFloat(p.Peak, 'f', -1, 64)+unit,
		(time.Duration(p.DurationSeconds * float64(time.Second))).String(),
		fmt.Sprintf("%d journeys, %d steps", p.Journeys, p.Steps))
	return strings.Join(parts, ", ")
}

func newScenariosListCmd(project *string) *cobra.Command {
	var tag string
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List a project's scenarios with their latest version",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, pid, err := projectClient(cmd.Context(), *project)
			if err != nil {
				return err
			}
			var ss []gen.Scenario
			if err := c.Do(cmd.Context(), "GET", "/projects/"+pid+"/scenarios"+client.Query("tag", tag), nil, &ss); err != nil {
				return err
			}
			return show(cmd, asJSON, ss, func(w io.Writer) error {
				if len(ss) == 0 {
					_, err := fmt.Fprintln(w, "No scenarios. Save one with stampede push <scenario.yaml>.")
					return err
				}
				var rows [][]string
				for _, s := range ss {
					tags := strings.Join(s.Tags, ",")
					if tags == "" {
						tags = "-"
					}
					rows = append(rows, []string{s.Name, "v" + strconv.Itoa(s.LatestVersion.Version), tags, planText(s.LatestVersion.Plan), stamp(&s.UpdatedAt)})
				}
				return table(w, "NAME\tLATEST\tTAGS\tLOAD\tUPDATED", rows)
			})
		},
	}
	cmd.Flags().StringVar(&tag, "tag", "", "only scenarios with this tag")
	jsonFlag(cmd, &asJSON)
	return cmd
}

func newScenariosShowCmd(project *string) *cobra.Command {
	var version int
	var yaml, asJSON bool
	cmd := &cobra.Command{
		Use:   "show <scenario>",
		Short: "Show a scenario, or print one version's YAML",
		Long: `Without flags, summarise a scenario and its latest version. With --yaml,
print the latest version's YAML; with --version N, print version N's YAML,
so stampede scenarios show checkout --version 3 > checkout.yaml restores
an old version locally.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			c, pid, err := projectClient(ctx, *project)
			if err != nil {
				return err
			}
			s, err := c.FindScenarioIn(ctx, pid, args[0])
			if err != nil {
				return err
			}
			if version > 0 {
				var v gen.ScenarioVersion
				if err := c.Do(ctx, "GET", fmt.Sprintf("/scenarios/%s/versions/%d", s.Id, version), nil, &v); err != nil {
					return err
				}
				return show(cmd, asJSON, v, func(w io.Writer) error {
					_, err := io.WriteString(w, v.Yaml)
					return err
				})
			}
			if yaml && !asJSON {
				_, err := io.WriteString(cmd.OutOrStdout(), s.LatestVersion.Yaml)
				return err
			}
			return show(cmd, asJSON, s, func(w io.Writer) error {
				v := s.LatestVersion
				fmt.Fprintf(w, "%s (id %s)\n", s.Name, s.Id)
				if d := or(s.Description, ""); d != "" {
					fmt.Fprintf(w, "  %s\n", d)
				}
				if len(s.Tags) > 0 {
					fmt.Fprintf(w, "Tags:     %s\n", strings.Join(s.Tags, ", "))
				}
				fmt.Fprintf(w, "Latest:   v%d by %s on %s", v.Version, or(v.CreatedBy, "?"), v.CreatedAt.Local().Format(time.RFC1123))
				if m := or(v.Message, ""); m != "" {
					fmt.Fprintf(w, ": %s", m)
				}
				fmt.Fprintf(w, "\nLoad:     %s\n", planText(v.Plan))
				_, err := fmt.Fprintf(w, "YAML:     stampede scenarios show %s --yaml (versions: stampede scenarios versions %s)\n", s.Name, s.Name)
				return err
			})
		},
	}
	cmd.Flags().IntVar(&version, "version", 0, "print this version's YAML")
	cmd.Flags().BoolVar(&yaml, "yaml", false, "print the latest version's YAML")
	jsonFlag(cmd, &asJSON)
	return cmd
}

func newScenariosVersionsCmd(project *string) *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "versions <scenario>",
		Short: "List a scenario's versions, newest first",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			c, pid, err := projectClient(ctx, *project)
			if err != nil {
				return err
			}
			s, err := c.FindScenarioIn(ctx, pid, args[0])
			if err != nil {
				return err
			}
			var vs []gen.ScenarioVersion
			if err := c.Do(ctx, "GET", "/scenarios/"+s.Id.String()+"/versions", nil, &vs); err != nil {
				return err
			}
			return show(cmd, asJSON, vs, func(w io.Writer) error {
				var rows [][]string
				for _, v := range vs {
					rows = append(rows, []string{"v" + strconv.Itoa(v.Version), or(v.Message, "-"), or(v.CreatedBy, "-"), stamp(&v.CreatedAt), planText(v.Plan)})
				}
				return table(w, "VERSION\tMESSAGE\tBY\tCREATED\tLOAD", rows)
			})
		},
	}
	jsonFlag(cmd, &asJSON)
	return cmd
}

func newScenariosDeleteCmd(project *string) *cobra.Command {
	return &cobra.Command{
		Use:   "delete <scenario>",
		Short: "Delete a scenario with all its versions",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			c, pid, err := projectClient(ctx, *project)
			if err != nil {
				return err
			}
			s, err := c.FindScenarioIn(ctx, pid, args[0])
			if err != nil {
				return err
			}
			if err := c.Do(ctx, "DELETE", "/scenarios/"+s.Id.String(), nil, nil); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Deleted scenario %s.\n", s.Name)
			return nil
		},
	}
}

func newScenariosValidateCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "validate <scenario.yaml>...",
		Short: "Check scenario files the way the server will",
		Long: `Send scenario files to the server's validator, which knows the plugins
installed on the server and its workers, and print each file's problems
or its load plan. Nothing is saved. stampede validate checks files on this
machine instead.`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := client.New()
			if err != nil {
				return err
			}
			type result struct {
				File string `json:"file"`
				gen.Validation
			}
			var results []result
			failed := 0
			for _, path := range args {
				b, err := os.ReadFile(path)
				if err != nil {
					return err
				}
				var v gen.Validation
				if err := c.Do(cmd.Context(), "POST", "/scenarios/validate", map[string]string{"yaml": string(b)}, &v); err != nil {
					return fmt.Errorf("%s: %w", path, err)
				}
				if !v.Valid {
					failed++
				}
				results = append(results, result{path, v})
			}
			err = show(cmd, asJSON, results, func(w io.Writer) error {
				for _, r := range results {
					if r.Valid {
						fmt.Fprintf(w, "✓ %s: %s\n", r.File, planText(r.Plan))
						continue
					}
					fmt.Fprintf(w, "✗ %s\n", r.File)
					for _, p := range r.Problems {
						fmt.Fprintf(w, "    %s\n", p)
					}
				}
				return nil
			})
			if err == nil && failed > 0 {
				err = fmt.Errorf("%d of %d scenarios are invalid", failed, len(args))
			}
			return err
		},
	}
	jsonFlag(cmd, &asJSON)
	return cmd
}
