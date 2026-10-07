package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/Ivan825/Stampede/internal/ai"
	"github.com/Ivan825/Stampede/internal/crawl"
	"github.com/Ivan825/Stampede/internal/safety"
)

// ExitUnvalidated is returned by generate when the proposal was not
// written because a journey failed its dry run.
const ExitUnvalidated = 4

type generateFlags struct {
	openapi, har, accessLog string
	graphql, graphqlPath    string
	introspect              bool
	describe                string
	target                  string
	providerName, model     string
	baseURL                 string
	out                     string
	diffAgainst             string
	tracesOut               string
	noDryRun                bool
	maxRepairs              int
	allowUnvalidated        bool
	allowHosts              []string
	env                     []string
	maxTokens               int
	crawl                   string
	crawlPages, crawlDepth  int
	protos, protoImports    []string
}

func newGenerateCmd() *cobra.Command {
	f := &generateFlags{}
	cmd := &cobra.Command{
		Use:     "generate",
		Aliases: []string{"gen"},
		Short:   "Draft a scenario with an AI model and dry-run every journey (optional, bring your own key)",
		Long: `Generate a scenario from a description, an OpenAPI spec, a GraphQL schema,
.proto files, a HAR recording and/or an access log, using a language model
you choose. The model only writes the scenario; it is never used while load
runs. With --proto, the services' unary and server streaming methods become
grpc steps, dry-run against --target (plaintext gRPC for http://, TLS for
https://).

Stampede builds a dependency map from the inputs, asks the model for a
scenario that fits the scenario schema, checks it statically (it must
compile, use only known endpoints and never call payment, SMS, email or
CAPTCHA services), then runs every journey once with a single user
against --target. Failures go back to the model with redacted evidence,
up to --max-repairs rounds. Secrets, tokens, cookies, emails, phone and
card numbers in recorded traffic are redacted before anything is sent.

The scenario is written only when every journey passed, or with
--allow-unvalidated, in which case journeys that failed are marked with a
FLAGGED comment. Exit code 4 means the proposal was not written.

Keys come from the environment: ANTHROPIC_API_KEY, OPENAI_API_KEY or
GEMINI_API_KEY. Ollama needs none; openai-compatible servers use
OPENAI_API_KEY when it is set.`,
		Example: `  stampede generate --from-openapi examples/shoplab/openapi.yaml \
      --describe "shoppers browse, some log in and buy" \
      --target http://localhost:8090 -o shop.yaml
  stampede generate --from-har session.har --from-log access.log --target http://localhost:8090 \
      --provider ollama --model qwen2.5-coder:14b -o mix.yaml
  stampede generate --from-openapi api.yaml --provider openai --model gpt-5 --no-dry-run -o draft.yaml
  stampede generate --proto protos/orders.proto --describe "clients place and track orders" \
      --target http://localhost:9090 -o orders.yaml`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runGenerate(cmd.Context(), cmd.OutOrStdout(), cmd.ErrOrStderr(), f)
		},
	}
	fl := cmd.Flags()
	fl.StringVar(&f.openapi, "from-openapi", "", "OpenAPI 3.x spec (YAML or JSON)")
	fl.StringVar(&f.graphql, "from-graphql", "", "GraphQL schema: SDL (.graphql) or an introspection result (.json)")
	fl.StringVar(&f.graphqlPath, "graphql-path", "/graphql", "path of the GraphQL API on the target")
	fl.BoolVar(&f.introspect, "introspect", false, "fetch the GraphQL schema from --target by introspection")
	fl.StringVar(&f.har, "from-har", "", "HAR recording of real use (browser devtools or a proxy)")
	fl.StringVar(&f.accessLog, "from-log", "", "web server access log, used to estimate the journey mix")
	fl.StringArrayVar(&f.protos, "proto", nil, ".proto file whose services become grpc steps (repeatable); steps name it in proto: so runs load the same descriptors")
	fl.StringArrayVar(&f.protoImports, "proto-import-path", nil, "directory where imports of the --proto files are found (repeatable); --proto paths are then relative to it")
	fl.StringVar(&f.describe, "describe", "", "plain-language description of your users and what they do")
	fl.StringVar(&f.target, "target", "", "base URL of the system to dry-run against, e.g. http://localhost:8090")
	fl.StringVar(&f.providerName, "provider", "anthropic", "model provider: anthropic, openai, gemini, ollama or openai-compatible")
	fl.StringVar(&f.model, "model", "", "model name (default claude-sonnet-5-5 for anthropic; required for the others)")
	fl.StringVar(&f.baseURL, "base-url", "", "provider API base URL (required for openai-compatible; e.g. http://localhost:11434 for a remote Ollama)")
	fl.StringVarP(&f.out, "out", "o", "", "write the scenario to this file (required)")
	fl.StringVar(&f.diffAgainst, "diff-against", "", "show a diff against this scenario (default: the --out file if it exists)")
	fl.StringVar(&f.tracesOut, "traces", "", "write per-journey dry-run traces as JSON to this file")
	fl.BoolVar(&f.noDryRun, "no-dry-run", false, "skip the dry run and only check the scenario statically")
	fl.IntVar(&f.maxRepairs, "max-repairs", ai.DefaultMaxRepairs, "repair rounds before a failing journey is flagged for a human (0 disables repair)")
	fl.BoolVar(&f.allowUnvalidated, "allow-unvalidated", false, "write the scenario even if some journeys failed, marking them with a comment")
	fl.StringSliceVar(&f.allowHosts, "allow-host", nil, "extra public hosts the dry run may reach besides the target")
	fl.StringArrayVarP(&f.env, "env", "e", nil, "set ${env.KEY} for the dry run (KEY=VALUE, repeatable)")
	fl.IntVar(&f.maxTokens, "max-tokens", 0, "maximum tokens per model reply (default 16000)")
	fl.StringVar(&f.crawl, "crawl", "", "crawl this URL in headless Chrome and use what the pages load (documents and API calls) in place of a HAR file; also the default --target")
	fl.IntVar(&f.crawlPages, "crawl-pages", 30, "most pages to visit with --crawl")
	fl.IntVar(&f.crawlDepth, "crawl-depth", 3, "most clicks away from the --crawl URL")
	return cmd
}

func readOptional(path string) ([]byte, error) {
	if path == "" {
		return nil, nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(b) > 50<<20 {
		return nil, fmt.Errorf("%s is larger than 50 MiB", path)
	}
	return b, nil
}

func runGenerate(ctx context.Context, stdout, stderr io.Writer, f *generateFlags) error {
	if f.out == "" {
		return errors.New("--out (-o) is required: where to write the scenario")
	}
	if f.crawl != "" && f.target == "" {
		if u, err := url.Parse(f.crawl); err == nil {
			f.target = u.Scheme + "://" + u.Host
		}
	}
	if !f.noDryRun && f.target == "" {
		return errors.New("--target is required for the dry run (or pass --no-dry-run)")
	}
	p, err := newProvider(f.providerName, f.model, f.baseURL)
	if err != nil {
		return err
	}

	var in ai.Inputs
	in.Description = f.describe
	if in.OpenAPI, err = readOptional(f.openapi); err != nil {
		return err
	}
	if in.HAR, err = readOptional(f.har); err != nil {
		return err
	}
	if f.crawl != "" {
		if len(in.HAR) > 0 {
			return errors.New("--crawl records its own HAR; use either --crawl or --from-har")
		}
		cu, err := url.Parse(f.crawl)
		if err != nil || cu.Host == "" {
			return fmt.Errorf("--crawl %q is not an absolute URL", f.crawl)
		}
		policy := safety.NewHostPolicy(cu.Hostname(), f.allowHosts)
		fmt.Fprintf(stderr, "stampede: crawling %s (at most %d pages, %d clicks deep; links only, no forms submitted)\n", f.crawl, f.crawlPages, f.crawlDepth)
		res, err := crawl.Crawl(ctx, crawl.Options{
			Start: f.crawl, MaxPages: f.crawlPages, MaxDepth: f.crawlDepth, Allow: policy.Allow,
			Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		})
		if err != nil {
			return fmt.Errorf("crawl: %w", err)
		}
		fmt.Fprintf(stderr, "stampede: crawled %d pages, recorded %d requests, found %d forms\n", len(res.Pages), res.Requests, len(res.Forms))
		in.HAR = res.HAR
		if sum := res.Summary(); sum != "" {
			in.Description = strings.TrimSpace(in.Description + "\n\n" + sum)
		}
	}
	if in.GraphQL, err = readOptional(f.graphql); err != nil {
		return err
	}
	in.GraphQLPath = f.graphqlPath
	if f.introspect {
		if f.target == "" {
			return errors.New("--introspect needs --target")
		}
		if in.GraphQL, err = introspect(ctx, strings.TrimRight(f.target, "/")+f.graphqlPath); err != nil {
			return err
		}
	}
	if in.AccessLog, err = readOptional(f.accessLog); err != nil {
		return err
	}
	if err := readProtos(&in, f.protos, f.protoImports); err != nil {
		return err
	}
	diffPath := f.diffAgainst
	if diffPath == "" {
		if _, err := os.Stat(f.out); err == nil {
			diffPath = f.out
		}
	}
	if in.Existing, err = readOptional(diffPath); err != nil {
		return err
	}

	env := map[string]string{}
	for _, kv := range f.env {
		k, v, ok := strings.Cut(kv, "=")
		if !ok {
			return fmt.Errorf("--env %q: use KEY=VALUE", kv)
		}
		env[k] = v
	}
	if f.target != "" {
		env["TARGET_URL"] = f.target
	}
	secrets := map[string]string{}
	for _, kv := range os.Environ() {
		k, v, _ := strings.Cut(kv, "=")
		if name, ok := strings.CutPrefix(k, "STAMPEDE_SECRET_"); ok {
			secrets[name] = v
		}
	}
	maxRepairs := f.maxRepairs
	if maxRepairs == 0 {
		maxRepairs = -1 // the pipeline reads 0 as "default"
	}

	fmt.Fprintf(stderr, "stampede: generating with %s/%s (the model sees redacted inputs only)\n", p.Name(), p.Model())
	res, err := ai.Generate(ctx, in, ai.Options{
		Provider: p, Target: f.target, AllowHosts: f.allowHosts, Env: env, Secrets: secrets,
		DryRun: !f.noDryRun, MaxRepairs: maxRepairs, MaxTokens: f.maxTokens,
		DataDir:  filepath.Dir(f.out),
		Progress: generateProgress(stderr),
	})
	if res != nil {
		fmt.Fprintf(stderr, "stampede: tokens used: %d in, %d out\n", res.Usage.InputTokens, res.Usage.OutputTokens)
	}
	if err != nil {
		return err
	}
	printGenerateSummary(stdout, res)

	if f.tracesOut != "" {
		b, _ := json.MarshalIndent(map[string]any{"journeys": res.Journeys, "problems": res.Problems}, "", "  ")
		if err := os.WriteFile(f.tracesOut, append(b, '\n'), 0o600); err != nil {
			return err
		}
	}
	if res.Diff != "" {
		fmt.Fprintf(stdout, "\nChanges against %s:\n%s", diffPath, res.Diff)
	}

	switch {
	case res.Fatal():
		return &exitError{code: ExitUnvalidated, msg: "no usable scenario was produced; nothing was written"}
	case !res.Validated() && !f.allowUnvalidated:
		return &exitError{code: ExitUnvalidated, msg: fmt.Sprintf("not written: %d journeys did not pass (%s); fix the inputs, raise --max-repairs, or pass --allow-unvalidated to write it with those journeys marked",
			len(res.Flagged()), strings.Join(res.Flagged(), ", "))}
	}
	if err := os.WriteFile(f.out, []byte(res.YAML), 0o644); err != nil { //nolint:gosec // a scenario file is not secret
		return err
	}
	fmt.Fprintf(stdout, "\nWrote %s", f.out)
	if n := len(res.Flagged()); n > 0 {
		fmt.Fprintf(stdout, " with %d flagged journeys (search for FLAGGED)", n)
	}
	fmt.Fprintf(stdout, ". Run it with: stampede run %s\n", f.out)
	return nil
}

func generateProgress(w io.Writer) func(ai.Progress) {
	return func(p ai.Progress) {
		label := p.Stage
		if p.Stage == ai.StageRepair {
			label = fmt.Sprintf("repair %d", p.Round)
		}
		fmt.Fprintf(w, "  %-14s %s\n", label, p.Message)
	}
}

func printGenerateSummary(w io.Writer, res *ai.Result) {
	fmt.Fprintf(w, "\nDry-run summary (%d repair round(s)):\n", res.Rounds)
	if !res.DryRun {
		fmt.Fprintln(w, "  (dry run skipped; static check only)")
	}
	for _, j := range res.Journeys {
		mark := "✓"
		switch j.Status {
		case ai.JourneyFlagged:
			mark = "✗"
		case ai.JourneyNotRun:
			mark = "·"
		}
		steps := 0
		for _, t := range j.Traces {
			steps += len(t.Steps)
		}
		fmt.Fprintf(w, "  %s %-24s %-8s %d passes, %d steps\n", mark, j.Name, j.Status, len(j.Traces), steps)
		if j.Status == ai.JourneyFlagged {
			for _, p := range j.Problems {
				fmt.Fprintf(w, "      %s\n", p)
			}
			for _, t := range j.Traces {
				if !t.OK {
					for _, line := range strings.Split(strings.TrimRight(ai.RenderTrace(t), "\n"), "\n") {
						fmt.Fprintf(w, "      | %s\n", line)
					}
					break
				}
			}
		}
	}
	for _, p := range res.Problems {
		if p.Journey != "" && !p.Fatal {
			continue // listed under the journey
		}
		sev := "problem"
		if p.Fatal {
			sev = "error"
		}
		fmt.Fprintf(w, "  %s: %s\n", sev, p)
	}
}

// introspect fetches a GraphQL schema with the standard introspection query.
func introspect(ctx context.Context, url string) ([]byte, error) {
	body, _ := json.Marshal(map[string]string{"query": ai.IntrospectionQuery})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return nil, fmt.Errorf("introspection: %w", err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("introspection at %s returned HTTP %d (is introspection disabled? pass the schema with --from-graphql)", url, resp.StatusCode)
	}
	return b, nil
}

// readProtos reads --proto files into the generator's inputs, named as
// imports and the scenario's proto: lists will name them: relative to the
// first --proto-import-path that holds them, or as given.
func readProtos(in *ai.Inputs, paths, importPaths []string) error {
	if len(paths) == 0 {
		if len(importPaths) > 0 {
			return errors.New("--proto-import-path needs --proto")
		}
		return nil
	}
	in.Proto = map[string][]byte{}
	for _, p := range paths {
		name := filepath.ToSlash(filepath.Clean(p))
		full := p
		for _, dir := range importPaths {
			if rel, err := filepath.Rel(dir, p); err == nil && !strings.HasPrefix(rel, "..") {
				name = filepath.ToSlash(rel)
				break
			}
			if _, err := os.Stat(filepath.Join(dir, p)); err == nil {
				full = filepath.Join(dir, p)
				break
			}
		}
		b, err := readOptional(full)
		if err != nil {
			return fmt.Errorf("--proto: %w", err)
		}
		in.Proto[name] = b
		in.ProtoPaths = append(in.ProtoPaths, name)
	}
	in.ProtoImportPaths = importPaths
	return nil
}
