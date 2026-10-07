package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/Ivan825/Stampede/internal/api/gen"
	"github.com/Ivan825/Stampede/internal/client"
)

func newAICmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ai",
		Short: "Manage the server's AI providers and journey generation jobs",
		Long: `AI journey generation on the server is optional and bring-your-own-key:
an admin adds a provider (Anthropic, OpenAI, Gemini, Ollama or any
OpenAI-compatible server), then editors start jobs that draft a scenario
from a description, an OpenAPI document, a HAR file, an access log or
.proto files, dry-run every journey against a target and repair it. A job
is a proposal; nothing is saved until someone approves it. stampede
generate does the same on this machine without a server.`,
	}
	cmd.AddCommand(newAIProvidersCmd(), newAIJobsCmd())
	return cmd
}

func newAIProvidersCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "providers",
		Aliases: []string{"provider"},
		Short:   "List, set and delete the organisation's AI providers (admin)",
	}
	var asJSON bool
	list := &cobra.Command{
		Use:   "list",
		Short: "List AI providers (keys are never shown)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := client.New()
			if err != nil {
				return err
			}
			var ps []gen.AIProvider
			if err := c.Do(cmd.Context(), "GET", "/ai/providers", nil, &ps); err != nil {
				return err
			}
			return show(cmd, asJSON, ps, func(w io.Writer) error {
				if len(ps) == 0 {
					_, err := fmt.Fprintln(w, "No AI providers. Add one with stampede ai providers set default --kind anthropic.")
					return err
				}
				var rows [][]string
				for _, p := range ps {
					key := "no key"
					if p.HasKey {
						key = "key stored"
					}
					rows = append(rows, []string{p.Name, string(p.Kind), p.Model, or(p.BaseURL, "-"), key, fmt.Sprintf("%d / %d", p.UsedTokensThisMonth, p.MonthlyTokenCap)})
				}
				return table(w, "NAME\tKIND\tMODEL\tBASE URL\tKEY\tTOKENS THIS MONTH / CAP", rows)
			})
		},
	}
	jsonFlag(list, &asJSON)

	var kind, model, baseURL, keyEnv string
	var keyStdin, setJSON bool
	var tokenCap int64
	set := &cobra.Command{
		Use:   "set <name>",
		Short: "Create or replace an AI provider",
		Long: `Create a provider, or change one by name; settings not given keep their
current values. The API key is encrypted with the server's master key and
never shown again. It is read from the environment variable named by
--api-key-env, or from stdin with --api-key-stdin, or asked without echo
in a terminal (leave it empty to keep the stored key). Jobs use the
provider named "default", or the only one.`,
		Example: `  stampede ai providers set default --kind anthropic --api-key-env ANTHROPIC_API_KEY
  stampede ai providers set local --kind openai-compatible --base-url http://llm.internal:8000/v1 --model qwen3
  stampede ai providers set default --monthly-token-cap 5000000`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if keyEnv != "" && keyStdin {
				return errors.New("give --api-key-env or --api-key-stdin, not both")
			}
			c, err := client.New()
			if err != nil {
				return err
			}
			body := map[string]any{"name": args[0]}
			existing, err := c.FindAIProvider(cmd.Context(), args[0])
			exists := err == nil
			if exists {
				body["kind"], body["model"], body["monthlyTokenCap"] = existing.Kind, existing.Model, existing.MonthlyTokenCap
				if existing.BaseURL != nil && *existing.BaseURL != "" {
					body["baseURL"] = *existing.BaseURL
				}
			}
			fl := cmd.Flags()
			if fl.Changed("kind") {
				body["kind"] = kind
				if !fl.Changed("model") {
					delete(body, "model") // the new kind's default
				}
			}
			if body["kind"] == nil {
				return errors.New("--kind is required for a new provider: anthropic, openai, gemini, ollama or openai-compatible")
			}
			if fl.Changed("model") {
				body["model"] = model
			}
			if fl.Changed("base-url") {
				body["baseURL"] = baseURL
			}
			if fl.Changed("monthly-token-cap") {
				body["monthlyTokenCap"] = tokenCap
			}
			key := ""
			switch {
			case keyEnv != "" || keyStdin:
				if key, err = readValue(cmd, "", keyEnv); err != nil {
					return err
				}
				if key == "" {
					return errors.New("the API key is empty")
				}
			default:
				p := newPrompter(cmd)
				if _, ok := p.terminal(); ok {
					label := "API key (empty for none): "
					if exists && existing.HasKey {
						label = "API key (empty keeps the stored key): "
					}
					if key, err = p.secret(label, ""); err != nil {
						return err
					}
				}
			}
			if key != "" {
				body["apiKey"] = strings.TrimSpace(key)
			}
			var out gen.AIProvider
			if err := c.Do(cmd.Context(), "POST", "/ai/providers", body, &out); err != nil {
				return err
			}
			return show(cmd, setJSON, out, func(w io.Writer) error {
				verb := "Added"
				if exists {
					verb = "Updated"
				}
				keyNote := "no key"
				if out.HasKey {
					keyNote = "key stored encrypted"
				}
				_, err := fmt.Fprintf(w, "%s AI provider %s: %s %s (%s; cap %d tokens a month).\n", verb, out.Name, out.Kind, out.Model, keyNote, out.MonthlyTokenCap)
				return err
			})
		},
	}
	fl := set.Flags()
	fl.StringVar(&kind, "kind", "", "anthropic, openai, gemini, ollama or openai-compatible (required for a new provider)")
	fl.StringVar(&model, "model", "", "model name (default claude-sonnet-5-5 for anthropic; required for the others)")
	fl.StringVar(&baseURL, "base-url", "", "API base URL (required for openai-compatible)")
	fl.StringVar(&keyEnv, "api-key-env", "", "read the API key from this environment variable")
	fl.BoolVar(&keyStdin, "api-key-stdin", false, "read the API key from stdin")
	fl.Int64Var(&tokenCap, "monthly-token-cap", 0, "refuse jobs once the organisation used this many tokens in a month (default 2000000)")
	jsonFlag(set, &setJSON)

	del := &cobra.Command{
		Use:   "delete <name>",
		Short: "Delete an AI provider and its stored key",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := client.New()
			if err != nil {
				return err
			}
			p, err := c.FindAIProvider(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			if err := c.Do(cmd.Context(), "DELETE", "/ai/providers/"+p.Id.String(), nil, nil); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Deleted AI provider %s.\n", p.Name)
			return nil
		},
	}
	cmd.AddCommand(list, set, del)
	return cmd
}

func newAIJobsCmd() *cobra.Command {
	var project string
	cmd := &cobra.Command{
		Use:     "jobs",
		Aliases: []string{"job"},
		Short:   "Start, follow and approve AI journey generation jobs",
		Long: `A job understands its inputs, drafts a scenario, checks it, dry-runs each
journey once with one user against the target and repairs failures. Its
result is a proposal: approve it to save it as a new scenario or a new
version. Jobs are named by id or a unique start of it.`,
	}
	cmd.PersistentFlags().StringVar(&project, "project", "", "project name, slug or id (default: the default or only project)")
	cmd.AddCommand(newAIJobsListCmd(&project), newAIJobsShowCmd(&project), newAIJobsCreateCmd(&project), newAIJobsApproveCmd(&project))
	return cmd
}

func newAIJobsListCmd(project *string) *cobra.Command {
	var limit int
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List a project's AI jobs, newest first",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, pid, err := projectClient(cmd.Context(), *project)
			if err != nil {
				return err
			}
			var js []gen.AIJobSummary
			if err := c.Do(cmd.Context(), "GET", "/projects/"+pid+"/ai/jobs"+client.Query("limit", strconv.Itoa(limit)), nil, &js); err != nil {
				return err
			}
			return show(cmd, asJSON, js, func(w io.Writer) error {
				if len(js) == 0 {
					_, err := fmt.Fprintln(w, "No AI jobs. Start one with stampede ai jobs create.")
					return err
				}
				var rows [][]string
				for _, j := range js {
					approved := "-"
					if j.ApprovedAt != nil {
						approved = stamp(j.ApprovedAt)
					}
					rows = append(rows, []string{j.Id.String()[:8], string(j.Status), j.Stage, j.ProviderKind + "/" + j.Model,
						strconv.FormatInt(j.Usage.InputTokens+j.Usage.OutputTokens, 10), or(j.CreatedBy, "-"), stamp(&j.CreatedAt), approved})
				}
				return table(w, "JOB\tSTATUS\tSTAGE\tMODEL\tTOKENS\tBY\tCREATED\tAPPROVED", rows)
			})
		},
	}
	cmd.Flags().IntVar(&limit, "limit", 50, "how many jobs (at most 200)")
	jsonFlag(cmd, &asJSON)
	return cmd
}

// aiJobID resolves a job reference; a full id needs no project.
func aiJobID(cmd *cobra.Command, project, ref string) (*client.Client, string, error) {
	c, err := client.New()
	if err != nil {
		return nil, "", err
	}
	if len(ref) == 36 {
		return c, ref, nil
	}
	p, err := c.FindProject(cmd.Context(), project)
	if err != nil {
		return nil, "", err
	}
	j, err := c.FindAIJob(cmd.Context(), p.Id.String(), ref)
	return c, j.Id.String(), err
}

func aiJobDone(s gen.AIJobStatus) bool {
	return s == gen.AIJobSucceeded || s == gen.AIJobNeedsReview || s == gen.AIJobFailed
}

// waitAIJob polls a job until it finishes, reporting its stages.
func waitAIJob(cmd *cobra.Command, c *client.Client, id string) (gen.AIJob, error) {
	stage := ""
	for {
		var j gen.AIJob
		if err := c.Do(cmd.Context(), "GET", "/ai/jobs/"+id, nil, &j); err != nil {
			return j, err
		}
		if j.Stage != stage {
			stage = j.Stage
			fmt.Fprintf(cmd.ErrOrStderr(), "  %s: %s (round %d)\n", j.Status, j.Stage, j.Round)
		}
		if aiJobDone(j.Status) {
			return j, nil
		}
		select {
		case <-cmd.Context().Done():
			return j, cmd.Context().Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
}

func writeAIJob(w io.Writer, j gen.AIJob, withYAML bool) error {
	fmt.Fprintf(w, "AI job %s: %s (stage %s, round %d)\n", j.Id, j.Status, j.Stage, j.Round)
	fmt.Fprintf(w, "Model:    %s/%s, %d input + %d output tokens\n", j.ProviderKind, j.Model, j.Usage.InputTokens, j.Usage.OutputTokens)
	if !j.DryRun {
		fmt.Fprintln(w, "Dry run:  skipped (checked statically only)")
	}
	if j.Error != nil && *j.Error != "" {
		fmt.Fprintf(w, "Error:    %s\n", *j.Error)
	}
	for _, jr := range j.Journeys {
		mark := map[gen.AIJourneyStatus]string{gen.AIJourneyPassed: "✓", gen.AIJourneyFlagged: "✗", gen.AIJourneyNotRun: "-"}[jr.Status]
		fmt.Fprintf(w, "  %s %s: %s after %d dry run(s)\n", mark, jr.Name, jr.Status, jr.Attempts)
		if jr.Problems != nil {
			for _, p := range *jr.Problems {
				fmt.Fprintf(w, "      %s\n", p)
			}
		}
	}
	for _, p := range j.Problems {
		where := ""
		if p.Journey != nil && *p.Journey != "" {
			where = *p.Journey + ": "
		}
		fmt.Fprintf(w, "  ! %s%s\n", where, p.Message)
	}
	if j.ApprovedScenarioId != nil {
		v := 0
		if j.ApprovedVersion != nil {
			v = *j.ApprovedVersion
		}
		fmt.Fprintf(w, "Approved: saved as version %d of scenario %s\n", v, j.ApprovedScenarioId)
	} else if j.Status == gen.AIJobSucceeded || j.Status == gen.AIJobNeedsReview {
		short := j.Id.String()[:8]
		fmt.Fprintf(w, "Review the proposal with stampede ai jobs show %s --yaml (or --diff), then save it with stampede ai jobs approve %s", short, short)
		if j.Status == gen.AIJobNeedsReview {
			fmt.Fprint(w, " --allow-unvalidated")
		}
		fmt.Fprintln(w, ".")
	}
	if withYAML && j.Yaml != nil {
		fmt.Fprintf(w, "\n%s", *j.Yaml)
	}
	return nil
}

func newAIJobsShowCmd(project *string) *cobra.Command {
	var wait, yaml, diff, asJSON bool
	cmd := &cobra.Command{
		Use:   "show <job>",
		Short: "Show a job's status, journeys and problems, or its proposal",
		Long: `Show a job. --wait follows it until it finishes; --yaml prints only the
proposed scenario and --diff only its diff against the scenario it was
compared with. --json includes the dry-run traces.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if yaml && diff {
				return errors.New("give --yaml or --diff, not both")
			}
			c, id, err := aiJobID(cmd, *project, args[0])
			if err != nil {
				return err
			}
			var j gen.AIJob
			if wait {
				j, err = waitAIJob(cmd, c, id)
			} else {
				err = c.Do(cmd.Context(), "GET", "/ai/jobs/"+id, nil, &j)
			}
			if err != nil {
				return err
			}
			if asJSON {
				return writeJSON(cmd.OutOrStdout(), j)
			}
			switch {
			case yaml:
				if j.Yaml == nil {
					return fmt.Errorf("job %s has no proposal yet (%s)", j.Id.String()[:8], j.Status)
				}
				_, err = io.WriteString(cmd.OutOrStdout(), *j.Yaml)
				return err
			case diff:
				if j.Diff == nil || *j.Diff == "" {
					return errors.New("the job has no diff: it was not compared with a scenario, or has no proposal yet")
				}
				_, err = io.WriteString(cmd.OutOrStdout(), *j.Diff)
				return err
			}
			return writeAIJob(cmd.OutOrStdout(), j, false)
		},
	}
	cmd.Flags().BoolVar(&wait, "wait", false, "wait for the job to finish")
	cmd.Flags().BoolVar(&yaml, "yaml", false, "print only the proposed scenario")
	cmd.Flags().BoolVar(&diff, "diff", false, "print only the diff against the scenario it was compared with")
	jsonFlag(cmd, &asJSON)
	return cmd
}

type aiJobFlags struct {
	describe, openapi, har, log string
	protos                      []string
	target, scenarioRef         string
	provider                    string
	noDryRun, wait, asJSON      bool
	maxRepairs                  int
}

func newAIJobsCreateCmd(project *string) *cobra.Command {
	f := &aiJobFlags{}
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Start a job that generates a scenario (asynchronous)",
		Long: `Start a generation job on the server. Give at least one input: a
description, an OpenAPI document, a HAR recording, an access log or .proto
files. Recorded traffic is redacted before it reaches the provider. With
--target, every journey is dry-run against that target and repaired; with
--scenario, the proposal is compared with that scenario and approving it
saves a new version. Prints the job id, or with --wait follows the job and
shows the result.`,
		Example: `  stampede ai jobs create --describe "people browse items, add one to the cart and check out" \
    --from-openapi openapi.yaml --target staging --wait
  stampede ai jobs create --from-har session.har --target staging --scenario checkout`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			c, pid, err := projectClient(ctx, *project)
			if err != nil {
				return err
			}
			body := map[string]any{}
			if f.describe != "" {
				body["description"] = f.describe
			}
			for key, path := range map[string]string{"openapi": f.openapi, "har": f.har, "accessLog": f.log} {
				if path == "" {
					continue
				}
				b, err := os.ReadFile(path)
				if err != nil {
					return err
				}
				body[key] = string(b)
			}
			if len(f.protos) > 0 {
				protos := map[string]string{}
				for _, path := range f.protos {
					b, err := os.ReadFile(path)
					if err != nil {
						return err
					}
					protos[filepath.Base(path)] = string(b)
				}
				body["proto"] = protos
			}
			if len(body) == 0 {
				return errors.New("give at least one input: --describe, --from-openapi, --from-har, --from-log or --proto")
			}
			if f.target != "" {
				t, err := c.FindTarget(ctx, pid, f.target)
				if err != nil {
					return err
				}
				body["targetId"] = t.Id
			}
			if f.scenarioRef != "" {
				s, err := c.FindScenarioIn(ctx, pid, f.scenarioRef)
				if err != nil {
					return err
				}
				body["scenarioId"] = s.Id
			}
			if f.provider != "" {
				p, err := c.FindAIProvider(ctx, f.provider)
				if err != nil {
					return err
				}
				body["providerId"] = p.Id
			}
			if f.noDryRun {
				body["dryRun"] = false
			}
			if cmd.Flags().Changed("max-repairs") {
				body["maxRepairs"] = f.maxRepairs
			}
			var j gen.AIJob
			if err := c.Do(ctx, "POST", "/projects/"+pid+"/ai/jobs", body, &j); err != nil {
				return err
			}
			if f.wait {
				if j, err = waitAIJob(cmd, c, j.Id.String()); err != nil {
					return err
				}
				return show(cmd, f.asJSON, j, func(w io.Writer) error { return writeAIJob(w, j, false) })
			}
			if f.asJSON {
				return writeJSON(cmd.OutOrStdout(), j)
			}
			fmt.Fprintln(cmd.OutOrStdout(), j.Id)
			fmt.Fprintf(cmd.ErrOrStderr(), "stampede: job started; follow it with stampede ai jobs show %s --wait\n", j.Id.String()[:8])
			return nil
		},
	}
	fl := cmd.Flags()
	fl.StringVar(&f.describe, "describe", "", "plain-language description of your users and what they do")
	fl.StringVar(&f.openapi, "from-openapi", "", "OpenAPI 3.x document (YAML or JSON)")
	fl.StringVar(&f.har, "from-har", "", "HAR recording of real use")
	fl.StringVar(&f.log, "from-log", "", "web server access log, used to estimate the journey mix")
	fl.StringArrayVar(&f.protos, "proto", nil, ".proto file whose services become grpc steps (repeatable; imports name files by base name)")
	fl.StringVar(&f.target, "target", "", "target to dry-run against (name, base URL or id); without it the scenario is only checked statically")
	fl.StringVar(&f.scenarioRef, "scenario", "", "existing scenario to compare the proposal with (approving saves a new version)")
	fl.StringVar(&f.provider, "provider", "", "AI provider name or id (default: the only one, or the one named default)")
	fl.BoolVar(&f.noDryRun, "no-dry-run", false, "skip the dry run and only check the scenario statically")
	fl.IntVar(&f.maxRepairs, "max-repairs", 3, "repair rounds before a failing journey is flagged for a human, 0 to 3")
	fl.BoolVar(&f.wait, "wait", false, "wait for the job to finish and show it")
	jsonFlag(cmd, &f.asJSON)
	return cmd
}

func newAIJobsApproveCmd(project *string) *cobra.Command {
	var scenarioRef, msg string
	var allow, asJSON bool
	cmd := &cobra.Command{
		Use:   "approve <job>",
		Short: "Save a finished job's proposal as a scenario or a new version",
		Long: `Save the proposal of a finished job: as a new version of --scenario
(default: the scenario the job was compared with), otherwise as a new
scenario. Jobs with flagged journeys need --allow-unvalidated. Needs the
editor role and is audited.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			c, id, err := aiJobID(cmd, *project, args[0])
			if err != nil {
				return err
			}
			body := map[string]any{}
			if scenarioRef != "" {
				var j gen.AIJob
				if err := c.Do(ctx, "GET", "/ai/jobs/"+id, nil, &j); err != nil {
					return err
				}
				s, err := c.FindScenarioIn(ctx, j.ProjectId.String(), scenarioRef)
				if err != nil {
					return err
				}
				body["scenarioId"] = s.Id
			}
			if msg != "" {
				body["message"] = msg
			}
			if allow {
				body["allowUnvalidated"] = true
			}
			var a gen.AIJobApproval
			if err := c.Do(ctx, "POST", "/ai/jobs/"+id+"/approve", body, &a); err != nil {
				return err
			}
			return show(cmd, asJSON, a, func(w io.Writer) error {
				_, err := fmt.Fprintf(w, "Saved as %s v%d. Run it with stampede start --scenario %s.\n", a.Scenario.Name, a.Version, a.Scenario.Name)
				return err
			})
		},
	}
	cmd.Flags().StringVar(&scenarioRef, "scenario", "", "save as a new version of this scenario")
	cmd.Flags().StringVarP(&msg, "message", "m", "", "version message")
	cmd.Flags().BoolVar(&allow, "allow-unvalidated", false, "approve even though some journeys are flagged")
	jsonFlag(cmd, &asJSON)
	return cmd
}

func newNarrativeCmd() *cobra.Command {
	var provider string
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "narrative <run>",
		Short: "Have the server's AI provider write a summary of a finished run",
		Long: `Ask the organisation's AI provider for a written summary of a finished
run's report. Every claim cites the report figures it rests on and is
labelled measured or suspected; claims citing unknown figures are dropped.
Only aggregate figures are sent, with error messages redacted. The
narrative is saved into the report, so stampede report and the HTML and
Markdown downloads include it. stampede report --narrative does the same
on this machine with your own key.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, id, err := runClient(cmd, args[0])
			if err != nil {
				return err
			}
			body := map[string]any{}
			if provider != "" {
				p, err := c.FindAIProvider(cmd.Context(), provider)
				if err != nil {
					return err
				}
				body["providerId"] = p.Id
			}
			var n gen.NarrativeResult
			if err := c.Do(cmd.Context(), "POST", "/runs/"+id+"/narrative", body, &n); err != nil {
				return err
			}
			return show(cmd, asJSON, n, func(w io.Writer) error {
				fmt.Fprintf(w, "%s\n\n", n.Narrative.Summary)
				facts := map[string]string{}
				if n.Narrative.Facts != nil {
					for _, f := range *n.Narrative.Facts {
						facts[f.Id] = f.Text
					}
				}
				for _, cl := range n.Narrative.Claims {
					fmt.Fprintf(w, "  - [%s] %s\n", cl.Label, cl.Text)
					for _, ref := range cl.Refs {
						if t, ok := facts[ref]; ok {
							fmt.Fprintf(w, "      %s: %s\n", ref, t)
						}
					}
				}
				_, err := fmt.Fprintf(w, "\n%s, %d tokens. Saved into the report (stampede report %s).\n", or(n.Narrative.Model, "model"), n.Usage.InputTokens+n.Usage.OutputTokens, id[:8])
				return err
			})
		},
	}
	cmd.Flags().StringVar(&provider, "provider", "", "AI provider name or id (default: the only one, or the one named default)")
	jsonFlag(cmd, &asJSON)
	return cmd
}
