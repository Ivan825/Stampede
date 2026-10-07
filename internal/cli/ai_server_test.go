package cli

import (
	"strings"
	"sync"
	"testing"

	"github.com/Ivan825/Stampede/internal/ai/provider"
	"github.com/Ivan825/Stampede/internal/api/gen"
	"github.com/Ivan825/Stampede/internal/client"
	"github.com/Ivan825/Stampede/internal/server"
)

// fakeAI hands the server scripted models and records the keys it was
// given.
type fakeAI struct {
	mu   sync.Mutex
	next *provider.Fake
	keys []string
}

func (f *fakeAI) set(replies ...any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.next = &provider.Fake{Replies: replies, PerCall: provider.Usage{InputTokens: 100, OutputTokens: 20}}
}

func (f *fakeAI) build(cfg provider.Config) (provider.Provider, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.keys = append(f.keys, cfg.APIKey)
	return f.next, nil
}

func (f *fakeAI) lastKey() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.keys) == 0 {
		return ""
	}
	return f.keys[len(f.keys)-1]
}

func aiServer(t *testing.T) (*client.Client, *fakeAI) {
	t.Helper()
	ai := &fakeAI{}
	c := signedInWith(t, server.Config{AI: server.AIConfig{NewProvider: ai.build, Workers: 1}})
	return c, ai
}

func TestAIProvidersCommands(t *testing.T) {
	aiServer(t)
	if out := mustCLI(t, "ai", "providers", "list"); !strings.Contains(out, "No AI providers") {
		t.Errorf("empty list: %s", out)
	}
	const key = "sk-ant-test-0123456789"
	t.Setenv("MY_AI_KEY", key)
	out := mustCLI(t, "ai", "providers", "set", "default", "--kind", "anthropic", "--api-key-env", "MY_AI_KEY")
	if out != "Added AI provider default: anthropic claude-sonnet-5-5 (key stored encrypted; cap 2000000 tokens a month).\n" {
		t.Errorf("set: %q", out)
	}
	if _, err := runCLI(t, "ai", "providers", "set", "other"); err == nil || !strings.Contains(err.Error(), "--kind is required") {
		t.Errorf("set without a kind: %v", err)
	}
	if _, err := runCLI(t, "ai", "providers", "set", "other", "--kind", "openai", "--model", "gpt-5"); err == nil || !strings.Contains(err.Error(), "apiKey is required") {
		t.Errorf("openai without a key: %v", err)
	}
	// Changing one setting keeps the others and the stored key.
	var p gen.AIProvider
	cliJSON(t, &p, "ai", "providers", "set", "default", "--monthly-token-cap", "5000")
	if p.Kind != gen.AIProviderAnthropic || p.Model != "claude-sonnet-5-5" || !p.HasKey || p.MonthlyTokenCap != 5000 {
		t.Errorf("set --monthly-token-cap: %+v", p)
	}
	if out, err := runCLIIn(t, "sk-from-stdin\n", "ai", "providers", "set", "local", "--kind", "openai-compatible", "--base-url", "http://127.0.0.1:9/v1", "--model", "qwen", "--api-key-stdin"); err != nil || !strings.Contains(out, "Added AI provider local: openai-compatible qwen (key stored encrypted") {
		t.Errorf("set from stdin: %v %q", err, out)
	}

	out = mustCLI(t, "ai", "providers", "list")
	jsonOut := mustCLI(t, "ai", "providers", "list", "--json")
	for _, o := range []string{out, jsonOut} {
		if strings.Contains(o, key) || strings.Contains(o, "sk-from-stdin") {
			t.Errorf("a key was printed:\n%s", o)
		}
	}
	for _, want := range []string{"default", "anthropic", "key stored", "0 / 5000", "local", "http://127.0.0.1:9/v1"} {
		if !strings.Contains(out, want) {
			t.Errorf("list lacks %q:\n%s", want, out)
		}
	}
	if out := mustCLI(t, "ai", "providers", "delete", "local"); out != "Deleted AI provider local.\n" {
		t.Errorf("delete: %q", out)
	}
	if _, err := runCLI(t, "ai", "providers", "delete", "local"); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Errorf("delete again: %v", err)
	}
}

func TestAIJobsAndNarrative(t *testing.T) {
	c, ai := aiServer(t)
	shop(t, c, target(t).URL)
	t.Setenv("MY_AI_KEY", "sk-test-key-0123456789")
	mustCLI(t, "ai", "providers", "set", "default", "--kind", "anthropic", "--api-key-env", "MY_AI_KEY")

	if _, err := runCLI(t, "ai", "jobs", "create"); err == nil || !strings.Contains(err.Error(), "give at least one input") {
		t.Errorf("create without inputs: %v", err)
	}
	ai.set(goodScenario)
	out := mustCLI(t, "ai", "jobs", "create", "--describe", "people browse items", "--target", "local", "--scenario", "items", "--wait")
	for _, want := range []string{"AI job ", ": succeeded", "anthropic/fake-model", "✓ browse: passed", "stampede ai jobs approve"} {
		if !strings.Contains(out, want) {
			t.Errorf("create --wait lacks %q:\n%s", want, out)
		}
	}
	if ai.lastKey() != "sk-test-key-0123456789" {
		t.Errorf("the provider got key %q", ai.lastKey())
	}
	var jobs []gen.AIJobSummary
	cliJSON(t, &jobs, "ai", "jobs", "list")
	if len(jobs) != 1 || jobs[0].Status != gen.AIJobSucceeded {
		t.Fatalf("list --json: %+v", jobs)
	}
	id := jobs[0].Id.String()
	if out := mustCLI(t, "ai", "jobs", "list"); !strings.Contains(out, id[:8]) || !strings.Contains(out, "succeeded") {
		t.Errorf("list:\n%s", out)
	}
	if out := mustCLI(t, "ai", "jobs", "show", id[:8], "--yaml"); !strings.Contains(out, "name: browse") || !strings.Contains(out, "/api/items/${itemId}") {
		t.Errorf("show --yaml:\n%s", out)
	}
	if out := mustCLI(t, "ai", "jobs", "show", id[:8], "--diff"); !strings.Contains(out, "+") {
		t.Errorf("show --diff:\n%s", out)
	}
	if out := mustCLI(t, "ai", "jobs", "approve", id[:8], "-m", "from AI"); out != "Saved as items v2. Run it with stampede start --scenario items.\n" {
		t.Errorf("approve: %q", out)
	}
	if out := mustCLI(t, "ai", "jobs", "show", id); !strings.Contains(out, "Approved: saved as version 2") {
		t.Errorf("show after approval:\n%s", out)
	}
	if _, err := runCLI(t, "ai", "jobs", "approve", id); err == nil {
		t.Error("a job was approved twice")
	}

	// A job started without waiting prints its id.
	ai.set(goodScenario)
	out = mustCLI(t, "ai", "jobs", "create", "--describe", "browse", "--no-dry-run")
	jid, rest, _ := strings.Cut(out, "\n")
	if len(jid) != 36 || !strings.Contains(rest, "stampede ai jobs show "+jid[:8]+" --wait") {
		t.Errorf("create: %q", out)
	}
	var j gen.AIJob
	cliJSON(t, &j, "ai", "jobs", "show", jid, "--wait")
	if j.DryRun || j.Status != gen.AIJobSucceeded {
		t.Errorf("show --wait --json: %+v", j)
	}

	// narrative <run> asks the provider for a summary of a finished run.
	run := strings.TrimSpace(mustCLI(t, "start", "--scenario", "items", "--duration", "2s", "--detach"))
	waitRun(t, run)
	ai.set(`{"summary":"The run served every request.","claims":[{"text":"Every request succeeded.","label":"measured","refs":["overall.requests"]}]}`)
	out = mustCLI(t, "narrative", run[:8])
	for _, want := range []string{"The run served every request.", "[measured] Every request succeeded.", "overall.requests:", "Saved into the report"} {
		if !strings.Contains(out, want) {
			t.Errorf("narrative lacks %q:\n%s", want, out)
		}
	}
	if out := mustCLI(t, "report", run[:8]); !strings.Contains(out, "The run served every request.") {
		t.Errorf("the report lacks the narrative:\n%s", out)
	}
}
