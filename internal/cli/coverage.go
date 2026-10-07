package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Ivan825/Stampede/internal/ai"
	"github.com/Ivan825/Stampede/internal/scenario"
)

// ExitDrift is returned by drift when a journey broke.
const ExitDrift = 4

type specFlags struct {
	openapi, graphql, graphqlPath, har, log string
}

func (f *specFlags) register(cmd *cobra.Command, what string) {
	fl := cmd.Flags()
	fl.StringVar(&f.openapi, "from-openapi", "", what+" as an OpenAPI 3.x spec (YAML or JSON)")
	fl.StringVar(&f.graphql, "from-graphql", "", what+" as a GraphQL schema (SDL or introspection JSON)")
	fl.StringVar(&f.graphqlPath, "graphql-path", "/graphql", "path of the GraphQL API")
	fl.StringVar(&f.har, "from-har", "", what+" as seen in a HAR recording")
	fl.StringVar(&f.log, "from-log", "", what+" as seen in an access log")
}

func (f *specFlags) understand() (*ai.Understanding, error) {
	var in ai.Inputs
	var err error
	if in.OpenAPI, err = readOptional(f.openapi); err != nil {
		return nil, err
	}
	if in.GraphQL, err = readOptional(f.graphql); err != nil {
		return nil, err
	}
	in.GraphQLPath = f.graphqlPath
	if in.HAR, err = readOptional(f.har); err != nil {
		return nil, err
	}
	if in.AccessLog, err = readOptional(f.log); err != nil {
		return nil, err
	}
	if len(in.OpenAPI)+len(in.GraphQL)+len(in.HAR)+len(in.AccessLog) == 0 {
		return nil, errors.New("give the API with --from-openapi, --from-graphql, --from-har or --from-log")
	}
	u, err := ai.Understand(in, ai.NewRedactor())
	if err != nil {
		return nil, err
	}
	if len(u.Endpoints) == 0 {
		return nil, errors.New("no endpoints found in the given API description")
	}
	return u, nil
}

func newCoverageCmd() *cobra.Command {
	var f specFlags
	var sf serverSpecFlags
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "coverage [scenario.yaml]",
		Short: "Show which API endpoints a scenario's journeys exercise and which none does",
		Long: `Map a scenario's requests onto the endpoints of an API: an OpenAPI spec, a
GraphQL schema, or the endpoints seen in a HAR recording or access log. The
result lists every endpoint with the journeys that call it, the endpoints
no journey touches, and requests that match no endpoint (often a typo or an
endpoint that was renamed). No model and no network access are needed.

With --scenario, the scenario saved on the server is checked by the
server, against an OpenAPI document given with --from-openapi or fetched
by the server from --spec-url (a URL on the host of one of the project's
targets).`,
		Example: `  stampede coverage shop.yaml --from-openapi openapi.yaml
  stampede coverage shop.yaml --from-log access.log --json
  stampede coverage --scenario checkout --spec-url https://staging.example.com/openapi.json`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := sf.check(cmd, args, []string{"from-graphql", "graphql-path", "from-har", "from-log"}); err != nil {
				return err
			}
			if sf.scenarioRef != "" {
				return serverCoverage(cmd, &sf, f.openapi, asJSON)
			}
			s, err := scenario.LoadFile(args[0])
			if err != nil {
				return err
			}
			if err := s.LoadReplay(); err != nil {
				return err
			}
			u, err := f.understand()
			if err != nil {
				return err
			}
			c := ai.CoverageOf(s, u)
			if asJSON {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(c)
			}
			writeCoverage(cmd.OutOrStdout(), c)
			return nil
		},
	}
	f.register(cmd, "the API")
	sf.register(cmd, false)
	cmd.Flags().BoolVar(&asJSON, "json", false, "print JSON")
	return cmd
}

func writeCoverage(w io.Writer, c *ai.Coverage) {
	fmt.Fprintf(w, "\n  %d of %d endpoints covered (%.0f%%)\n\n", c.Covered, c.Total, c.Percent())
	for _, e := range c.Endpoints {
		mark, by := "✗", "no journey"
		if len(e.Journeys) > 0 {
			mark, by = "✓", strings.Join(e.Journeys, ", ")
		}
		fmt.Fprintf(w, "  %s %-7s %-44s %s\n", mark, e.Method, e.Path, by)
	}
	if len(c.Unmatched) > 0 {
		fmt.Fprintf(w, "\n  requests that match no endpoint\n")
		for _, r := range c.Unmatched {
			fmt.Fprintf(w, "    %-7s %-44s in %s\n", r.Method, r.URL, r.Journey)
		}
	}
	if c.Templated > 0 {
		fmt.Fprintf(w, "\n  %d requests have a fully templated URL and were not matched\n", c.Templated)
	}
	fmt.Fprintln(w)
}

func newDriftCmd() *cobra.Command {
	var f, prev specFlags
	var sf serverSpecFlags
	var (
		target     string
		envs       []string
		allowHosts []string
		asJSON     bool
		noDryRun   bool
	)
	cmd := &cobra.Command{
		Use:   "drift [scenario.yaml]",
		Short: "Find journeys an API change broke",
		Long: `Check a saved scenario against the current version of an API, so journeys that
broke after an API change are found before the next load test:

  - with --previous-openapi (or the other --previous-* flags), endpoints
    removed since the previous version and the journeys that call them;
  - requests that use no endpoint of the current API;
  - with --target, a dry run of every journey with one user (real requests,
    as in stampede generate), reporting the journeys that now fail.

Exit code 4 when something drifted. To repair, regenerate with the scenario
as the starting point: stampede generate --from-openapi new.yaml
--diff-against scenario.yaml --target URL -o scenario.yaml.

With --scenario, the scenario saved on the server is checked by the
server: the API comes from --from-openapi or --spec-url (and
--previous-openapi or --previous-spec-url), and --target names one of the
project's targets for the dry run.

The subcommands read the checks that drift schedules run on the server
(stampede schedules create --kind drift): drift results lists them, drift
show prints one, and drift repair asks the AI generator to fix the broken
journeys. A scenario file named like a subcommand needs a path, such as
./results.`,
		Example: `  stampede drift shop.yaml --from-openapi v2.yaml --previous-openapi v1.yaml
  stampede drift shop.yaml --from-openapi openapi.yaml --target http://staging:8080
  stampede drift --scenario checkout --spec-url https://staging.example.com/openapi.json --target staging
  stampede drift results --project shop
  stampede drift repair 7c1e0a2b`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := sf.check(cmd, args, []string{"from-graphql", "graphql-path", "from-har", "from-log", "previous-graphql", "previous-har", "previous-log", "env", "allow-host", "no-dry-run"}); err != nil {
				return err
			}
			if sf.scenarioRef != "" {
				return serverDrift(cmd, &sf, f.openapi, prev.openapi, target, asJSON)
			}
			out := cmd.OutOrStdout()
			s, err := scenario.LoadFile(args[0])
			if err != nil {
				return err
			}
			if err := s.LoadReplay(); err != nil {
				return err
			}
			cur, err := f.understand()
			if err != nil {
				return err
			}
			report := struct {
				Diff      *ai.SpecDiff      `json:"diff,omitempty"`
				Unmatched []ai.RequestRef   `json:"unmatched,omitempty"`
				DryRun    []ai.JourneyCheck `json:"dryRun,omitempty"`
				Drifted   bool              `json:"drifted"`
			}{}
			if prev.openapi+prev.graphql+prev.har+prev.log != "" {
				old, err := prev.understand()
				if err != nil {
					return fmt.Errorf("previous version: %w", err)
				}
				report.Diff = ai.DiffSpecs(s, old, cur)
				report.Drifted = report.Drifted || len(report.Diff.Broken) > 0
			}
			report.Unmatched = ai.CoverageOf(s, cur).Unmatched
			report.Drifted = report.Drifted || len(report.Unmatched) > 0
			if !noDryRun && target != "" {
				env := map[string]string{"TARGET_URL": target}
				for _, kv := range os.Environ() {
					if k, v, ok := strings.Cut(kv, "="); ok {
						if _, set := env[k]; !set {
							env[k] = v
						}
					}
				}
				for _, kv := range envs {
					k, v, ok := strings.Cut(kv, "=")
					if !ok {
						return fmt.Errorf("--env %q: use KEY=VALUE", kv)
					}
					env[k] = v
				}
				secrets := map[string]string{}
				for _, kv := range os.Environ() {
					k, v, _ := strings.Cut(kv, "=")
					if name, ok := strings.CutPrefix(k, "STAMPEDE_SECRET_"); ok {
						secrets[name] = v
					}
				}
				s.Target.BaseURL = target
				checks, err := ai.DryRunScenario(cmd.Context(), s, target, allowHosts, env, secrets, "")
				if err != nil {
					return err
				}
				report.DryRun = checks
				for _, c := range checks {
					report.Drifted = report.Drifted || !c.OK
				}
			}
			if asJSON {
				enc := json.NewEncoder(out)
				enc.SetIndent("", "  ")
				if err := enc.Encode(report); err != nil {
					return err
				}
			} else {
				writeDrift(out, report.Diff, report.Unmatched, report.DryRun, target != "" && !noDryRun)
			}
			if report.Drifted {
				return &exitError{code: ExitDrift, msg: "the scenario drifted from the API"}
			}
			return nil
		},
	}
	f.register(cmd, "the current API")
	fl := cmd.Flags()
	fl.StringVar(&prev.openapi, "previous-openapi", "", "the previous OpenAPI spec, to list removed endpoints")
	fl.StringVar(&prev.graphql, "previous-graphql", "", "the previous GraphQL schema")
	fl.StringVar(&prev.har, "previous-har", "", "a HAR recording of the previous version")
	fl.StringVar(&prev.log, "previous-log", "", "an access log of the previous version")
	prev.graphqlPath = "/graphql"
	fl.StringVar(&target, "target", "", "dry-run every journey once against this URL (sends real requests); with --scenario, a target of the project (name, base URL or id)")
	fl.StringArrayVarP(&envs, "env", "e", nil, "set ${env.KEY} for the dry run (KEY=VALUE, repeatable)")
	fl.StringSliceVar(&allowHosts, "allow-host", nil, "extra public hosts the dry run may reach")
	fl.BoolVar(&noDryRun, "no-dry-run", false, "skip the dry run even with --target")
	fl.BoolVar(&asJSON, "json", false, "print JSON")
	sf.register(cmd, true)
	cmd.AddCommand(newDriftResultsCmds()...)
	return cmd
}

func writeDrift(w io.Writer, d *ai.SpecDiff, unmatched []ai.RequestRef, checks []ai.JourneyCheck, dryRan bool) {
	fmt.Fprintln(w)
	if d != nil {
		fmt.Fprintf(w, "  API change: %d endpoints added, %d removed\n", len(d.Added), len(d.Removed))
		for _, e := range d.Removed {
			fmt.Fprintf(w, "    - %s %s\n", e.Method, e.Path)
		}
		for _, e := range d.Added {
			fmt.Fprintf(w, "    + %s %s\n", e.Method, e.Path)
		}
		journeys := make([]string, 0, len(d.Broken))
		for j := range d.Broken {
			journeys = append(journeys, j)
		}
		sort.Strings(journeys)
		for _, j := range journeys {
			eps := d.Broken[j]
			var keys []string
			for _, e := range eps {
				keys = append(keys, e.Key())
			}
			fmt.Fprintf(w, "  ✗ journey %s calls removed endpoints: %s\n", j, strings.Join(keys, ", "))
		}
	}
	for _, r := range unmatched {
		fmt.Fprintf(w, "  ✗ %s: %s %s is not an endpoint of the current API\n", r.Journey, r.Method, r.URL)
	}
	if dryRan {
		for _, c := range checks {
			if c.OK {
				fmt.Fprintf(w, "  ✓ %s passes its dry run\n", c.Journey)
				continue
			}
			fmt.Fprintf(w, "  ✗ %s fails its dry run", c.Journey)
			for _, tr := range c.Traces {
				if !tr.OK {
					if tr.Error != "" {
						fmt.Fprintf(w, ": %s", tr.Error)
					}
					for _, st := range tr.Steps {
						if !st.OK {
							why := st.Error
							if why == "" && st.Status != 0 {
								why = fmt.Sprintf("status %d", st.Status)
							}
							fmt.Fprintf(w, " (step %s: %s)", st.Step, why)
							break
						}
					}
					break
				}
			}
			fmt.Fprintln(w)
		}
	}
	fmt.Fprintln(w)
}
