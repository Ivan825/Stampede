// Package ai generates Stampede scenarios with a language model, before any
// load is run. It is optional and bring-your-own-key: the model is called
// only here, never during a load test.
//
// The pipeline has six stages:
//
//  1. Understand: build a dependency map and traffic mix from a
//     description, an OpenAPI spec, a HAR recording and/or an access log.
//  2. Draft: the model writes a scenario constrained to the JSON Schema.
//  3. Static check: the scenario must parse and compile (variable flow),
//     use only known endpoints and never call blocked third parties.
//  4. Dry run: each journey runs once with one user against the target,
//     recording every request, response, extracted value and check.
//  5. Repair: problems and redacted evidence go back to the model, up to
//     three rounds; journeys that still fail are flagged for a human.
//  6. Approve: the result is a proposal (YAML, traces, diff). Nothing is
//     saved until a person approves it.
package ai

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/Ivan825/Stampede/internal/ai/provider"
	"github.com/Ivan825/Stampede/internal/safety"
	"github.com/Ivan825/Stampede/internal/scenario"
	"github.com/Ivan825/Stampede/schema"
)

// Pipeline stages, reported through Options.Progress.
const (
	StageUnderstand  = "understand"
	StageDraft       = "draft"
	StageStaticCheck = "static-check"
	StageDryRun      = "dry-run"
	StageRepair      = "repair"
	StageDone        = "done"
)

// Journey statuses.
const (
	JourneyPassed  = "passed"
	JourneyFlagged = "flagged"
	JourneyNotRun  = "not-run"
)

// DefaultMaxRepairs is how many times failures go back to the model.
const DefaultMaxRepairs = 3

// ErrBudget means the token budget ran out before the first draft.
var ErrBudget = errors.New("the AI token budget is exhausted")

// Options configure a generation.
type Options struct {
	Provider provider.Provider
	// Target is the base URL of the system under test. It is required for
	// the dry run.
	Target string
	// AllowHosts are extra hosts requests may reach besides the target and
	// private addresses.
	AllowHosts []string
	// Env and Secrets back ${env.X} and ${secret.X} in the dry run. Secret
	// values are redacted everywhere.
	Env     map[string]string
	Secrets map[string]string
	// DryRun runs each journey once against Target.
	DryRun bool
	// MaxRepairs bounds repair rounds (0 uses DefaultMaxRepairs; negative
	// disables repair).
	MaxRepairs int
	// OmitBaseURL leaves target.baseURL out of the YAML (the server
	// supplies it from the run's target). Otherwise it is set to Target.
	OmitBaseURL bool
	// DataDir and ConfineData resolve file feeders in the dry run.
	DataDir     string
	ConfineData bool
	// TokenBudget stops calling the model once this many tokens were used
	// (0 means no limit).
	TokenBudget int64
	// MaxTokens bounds each reply (default 16000).
	MaxTokens int
	// Transport overrides the dry run's HTTP transport (tests).
	Transport http.RoundTripper
	// Progress receives stage changes. It must not block.
	Progress func(Progress)
}

// Progress reports the pipeline's state.
type Progress struct {
	Stage   string
	Round   int
	Message string
	Usage   provider.Usage
}

// JourneyResult is the outcome for one journey.
type JourneyResult struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	// Attempts counts the dry runs of this journey across repair rounds.
	Attempts int      `json:"attempts"`
	Traces   []Trace  `json:"traces"`
	Problems []string `json:"problems,omitempty"`
}

// Result is a proposal awaiting approval.
type Result struct {
	// YAML is the proposed scenario, with flagged journeys marked by a
	// comment. Empty when the model never produced a usable scenario.
	YAML     string             `json:"yaml"`
	Scenario *scenario.Scenario `json:"-"`
	Journeys []JourneyResult    `json:"journeys"`
	// Problems are static problems in the proposal (fatal ones mean the
	// YAML must not be used).
	Problems []Problem `json:"problems"`
	// Diff compares Inputs.Existing with the proposal.
	Diff          string         `json:"diff,omitempty"`
	Usage         provider.Usage `json:"usage"`
	Rounds        int            `json:"rounds"`
	Provider      string         `json:"provider"`
	Model         string         `json:"model"`
	DryRun        bool           `json:"dryRun"`
	Understanding *Understanding `json:"understanding,omitempty"`
}

// Fatal reports whether the proposal is unusable.
func (r *Result) Fatal() bool {
	if r.YAML == "" {
		return true
	}
	for _, p := range r.Problems {
		if p.Fatal {
			return true
		}
	}
	return false
}

// Validated reports whether every journey passed (or, without a dry run,
// the static check found nothing).
func (r *Result) Validated() bool {
	if r.Fatal() || len(r.Problems) > 0 {
		return false
	}
	for _, j := range r.Journeys {
		if j.Status == JourneyFlagged {
			return false
		}
		if j.Status == JourneyNotRun && r.DryRun {
			return false
		}
	}
	return true
}

// Flagged lists journeys that need a human.
func (r *Result) Flagged() []string {
	var out []string
	for _, j := range r.Journeys {
		if j.Status == JourneyFlagged {
			out = append(out, j.Name)
		}
	}
	return out
}

// round is the state after one model reply.
type round struct {
	n        int
	reply    string
	doc      *yaml.Node
	sc       *scenario.Scenario
	static   []Problem
	journeys map[string]*JourneyResult
	order    []string
}

func (r *round) fatal() bool {
	if r.sc == nil {
		return true
	}
	for _, p := range r.static {
		if p.Fatal {
			return true
		}
	}
	return false
}

func (r *round) flagged() int {
	n := 0
	for _, j := range r.journeys {
		if j.Status != JourneyPassed {
			n++
		}
	}
	return n
}

// better reports whether r beats o: usable first, then fewer flagged
// journeys and scenario problems; later rounds win ties.
func (r *round) better(o *round) bool {
	if o == nil {
		return true
	}
	if r.fatal() != o.fatal() {
		return !r.fatal()
	}
	if r.flagged() != o.flagged() {
		return r.flagged() < o.flagged()
	}
	return len(r.static) <= len(o.static)
}

// Generate runs the pipeline. It returns a result even with an error when
// some work was done, so token usage can always be recorded.
func Generate(ctx context.Context, in Inputs, opts Options) (*Result, error) {
	if opts.Provider == nil {
		return nil, errors.New("no AI provider configured")
	}
	maxRepairs := opts.MaxRepairs
	switch {
	case maxRepairs == 0:
		maxRepairs = DefaultMaxRepairs
	case maxRepairs < 0:
		maxRepairs = 0
	}
	res := &Result{Provider: opts.Provider.Name(), Model: opts.Provider.Model(), DryRun: opts.DryRun, Problems: []Problem{}, Journeys: []JourneyResult{}}
	report := func(stage string, n int, msg string) {
		if opts.Progress != nil {
			opts.Progress(Progress{Stage: stage, Round: n, Message: msg, Usage: res.Usage})
		}
	}

	var target *url.URL
	if opts.Target != "" {
		u, err := url.Parse(opts.Target)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return nil, fmt.Errorf("target must be an absolute http(s) URL, got %q", opts.Target)
		}
		target = u
	}
	if opts.DryRun && target == nil {
		return nil, errors.New("the dry run needs a target URL")
	}
	if target != nil {
		if cat := ThirdPartyCategory(target.Hostname()); cat != "" {
			return nil, fmt.Errorf("the target %s is a %s provider; Stampede will not generate load for it", target.Hostname(), cat)
		}
	}

	red := NewRedactor()
	for _, v := range opts.Secrets {
		red.AddSecret(v)
	}

	report(StageUnderstand, 0, "reading inputs")
	und, err := Understand(in, red)
	if err != nil {
		return nil, err
	}
	res.Understanding = und
	report(StageUnderstand, 0, fmt.Sprintf("%d endpoints, %d dependencies", len(und.Endpoints), len(und.Dependencies)))

	existing := strings.TrimSpace(string(in.Existing))
	first := provider.Message{Role: provider.RoleUser, Content: draftPrompt(und, red.Secrets(existing))}
	msgs := []provider.Message{first}
	sys := systemPrompt()
	targetHost := ""
	if target != nil {
		targetHost = target.Hostname()
	}

	attempts := map[string]int{}
	var best *round
	for n := 0; ; n++ {
		stage := StageDraft
		if n > 0 {
			stage = StageRepair
		}
		if opts.TokenBudget > 0 && res.Usage.Total() >= opts.TokenBudget {
			if best == nil {
				return res, ErrBudget
			}
			res.Problems = append(res.Problems, Problem{Message: "the token budget ran out after " + plural(n-1, "repair round")})
			break
		}
		report(stage, n, fmt.Sprintf("asking %s/%s", opts.Provider.Name(), opts.Provider.Model()))
		resp, err := opts.Provider.Chat(ctx, &provider.Request{
			System: sys, Messages: msgs, JSONSchema: schema.Scenario, SchemaName: "scenario", MaxTokens: opts.MaxTokens,
		})
		if resp != nil {
			res.Usage.Add(resp.Usage)
		}
		cur := &round{n: n, journeys: map[string]*JourneyResult{}}
		switch {
		case err == nil:
			cur.reply = resp.Text
			cur.parse(target)
		case errors.Is(err, provider.ErrNoJSON) && resp != nil:
			cur.reply = resp.Text
			cur.static = []Problem{{Message: err.Error(), Fatal: true}}
		default:
			if best != nil && ctx.Err() == nil {
				// Keep what we have; the proposal is still useful.
				res.Problems = append(res.Problems, Problem{Message: "the model failed during repair: " + err.Error()})
				best.finish(res, opts, existing, attempts)
				return res, nil
			}
			return res, err
		}
		res.Rounds = n

		if cur.sc != nil {
			cur.static = append(cur.static, staticCheck(cur.sc, und, targetHost, opts.AllowHosts)...)
		}
		report(StageStaticCheck, n, fmt.Sprintf("%d problems", len(cur.static)))

		if !cur.fatal() {
			cur.order = make([]string, 0, len(cur.sc.Journeys))
			for _, j := range cur.sc.Journeys {
				cur.order = append(cur.order, j.Name)
				cur.journeys[j.Name] = &JourneyResult{Name: j.Name, Status: JourneyNotRun, Traces: []Trace{}}
			}
			for _, p := range cur.static {
				if jr := cur.journeys[p.Journey]; jr != nil {
					jr.Status = JourneyFlagged
					jr.Problems = append(jr.Problems, p.Message)
				}
			}
			if opts.DryRun {
				if err := cur.dryRun(ctx, opts, red, attempts); err != nil {
					return res, err
				}
				passed := 0
				for _, jr := range cur.journeys {
					if jr.Status == JourneyPassed {
						passed++
					}
				}
				report(StageDryRun, n, fmt.Sprintf("%d of %d journeys passed", passed, len(cur.journeys)))
			}
		}
		if cur.better(best) {
			best = cur
		}

		failures := map[string][]Trace{}
		var passedNames []string
		for _, name := range cur.order {
			jr := cur.journeys[name]
			switch {
			case jr.Status == JourneyPassed:
				passedNames = append(passedNames, name)
			case len(jr.Traces) > 0:
				failures[name] = jr.Traces
			}
		}
		if len(cur.static) == 0 && len(failures) == 0 {
			break
		}
		if n >= maxRepairs {
			break
		}
		if err := ctx.Err(); err != nil {
			return res, err
		}
		reply := cur.reply
		if reply == "" {
			reply = "{}"
		}
		msgs = []provider.Message{
			first,
			{Role: provider.RoleAssistant, Content: reply},
			{Role: provider.RoleUser, Content: repairPrompt(cur.static, failures, passedNames)},
		}
	}
	best.finish(res, opts, existing, attempts)
	report(StageDone, res.Rounds, fmt.Sprintf("%d journeys, %d flagged", len(res.Journeys), len(res.Flagged())))
	return res, nil
}

// parse decodes the model's reply. The base URL is set so validation and
// the dry run resolve relative paths.
func (r *round) parse(target *url.URL) {
	doc, sc, err := parseDraft(r.reply)
	if err != nil {
		r.static = []Problem{{Message: "the scenario could not be read: " + err.Error(), Fatal: true}}
		return
	}
	r.doc = doc
	r.sc = sc
	if target != nil {
		sc.Target.BaseURL = strings.TrimRight(target.String(), "/")
	} else if sc.Target.BaseURL == "" {
		sc.Target.BaseURL = "${env.TARGET_URL}"
	}
}

func (r *round) dryRun(ctx context.Context, opts Options, red *Redactor, attempts map[string]int) error {
	prog, err := scenario.Compile(r.sc)
	if err != nil {
		r.static = append(r.static, Problem{Message: err.Error(), Fatal: true})
		return nil
	}
	target, _ := url.Parse(opts.Target)
	policy := safety.NewHostPolicy(target.Hostname(), opts.AllowHosts)
	runner := &DryRunner{
		Program: prog, BaseURL: strings.TrimRight(opts.Target, "/"), Env: opts.Env, Secrets: opts.Secrets,
		DataDir: opts.DataDir, ConfineData: opts.ConfineData, Redactor: red, Transport: opts.Transport,
		Allow: func(u *url.URL) string {
			if cat := ThirdPartyCategory(u.Hostname()); cat != "" {
				return fmt.Sprintf("%s is a blocked %s provider", u.Hostname(), cat)
			}
			if !policy.Allow(u) {
				return fmt.Sprintf("%s is not the target or an allowed host", u.Hostname())
			}
			return ""
		},
	}
	for _, cj := range prog.Journeys {
		if err := ctx.Err(); err != nil {
			return err
		}
		jr := r.journeys[cj.Name]
		jr.Traces = runner.RunJourney(ctx, cj)
		attempts[cj.Name]++
		ok := true
		for _, t := range jr.Traces {
			if !t.OK {
				ok = false
				msg := fmt.Sprintf("pass %d failed", t.Pass)
				if len(t.Branches) > 0 {
					msg += " (" + strings.Join(t.Branches, ", ") + ")"
				}
				if t.Error != "" {
					msg += ": " + t.Error
				}
				jr.Problems = append(jr.Problems, msg)
			}
		}
		if jr.Status == JourneyNotRun {
			jr.Status = JourneyPassed
		}
		if !ok {
			jr.Status = JourneyFlagged
		}
	}
	return nil
}

// finish renders the chosen round into the result.
func (r *round) finish(res *Result, opts Options, existing string, attempts map[string]int) {
	res.Problems = append(r.static, res.Problems...)
	if r.sc == nil || r.doc == nil {
		return
	}
	res.Scenario = r.sc
	flagged := map[string]string{}
	for _, name := range r.order {
		jr := r.journeys[name]
		jr.Attempts = attempts[name]
		if jr.Status == JourneyFlagged {
			why := strings.Join(jr.Problems, "\n")
			if jr.Attempts > 0 {
				why = "It still failed after " + plural(res.Rounds, "repair round") + ":\n" + why
			}
			flagged[name] = Truncate(why, 600)
		}
		res.Journeys = append(res.Journeys, *jr)
	}
	base := ""
	if !opts.OmitBaseURL {
		base = strings.TrimRight(opts.Target, "/")
		if base == "" {
			base = "${env.TARGET_URL}"
		}
	}
	setBaseURL(r.doc, base)
	annotate(r.doc, header(res, opts), flagged)
	out, err := encodeYAML(r.doc)
	if err != nil {
		res.Problems = append(res.Problems, Problem{Message: "could not render YAML: " + err.Error(), Fatal: true})
		return
	}
	res.YAML = string(out)
	if existing != "" {
		res.Diff = UnifiedDiff(existing+"\n", res.YAML, "current", "proposed")
	}
}

func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return fmt.Sprintf("%d %ss", n, word)
}

func header(res *Result, opts Options) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Generated by stampede generate with %s/%s.\n", res.Provider, res.Model)
	if opts.DryRun {
		passed := 0
		for _, j := range res.Journeys {
			if j.Status == JourneyPassed {
				passed++
			}
		}
		fmt.Fprintf(&b, "Dry run (one user, each journey once): %d of %d journeys passed.\n", passed, len(res.Journeys))
	} else {
		b.WriteString("Not dry-run: journeys were checked statically only.\n")
	}
	b.WriteString("Review it before running it at scale.")
	return b.String()
}
