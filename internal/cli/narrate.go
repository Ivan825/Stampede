package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/spf13/pflag"

	"github.com/Ivan825/Stampede/internal/ai"
	"github.com/Ivan825/Stampede/internal/ai/provider"
	"github.com/Ivan825/Stampede/internal/report"
)

// aiFlags select a model for a report narrative.
type aiFlags struct {
	narrative            bool
	kind, model, baseURL string
}

func (a *aiFlags) register(fl *pflag.FlagSet) {
	fl.BoolVar(&a.narrative, "narrative", false, "add an AI-written summary; every claim cites the report's figures (needs a provider key)")
	fl.StringVar(&a.kind, "provider", "anthropic", "model provider for --narrative: anthropic, openai, gemini, ollama or openai-compatible")
	fl.StringVar(&a.model, "model", "", "model for --narrative (default claude-sonnet-5-5 for anthropic)")
	fl.StringVar(&a.baseURL, "ai-base-url", "", "provider API base URL for --narrative")
}

// provider returns nil when no narrative was asked for.
func (a *aiFlags) provider() (provider.Provider, error) {
	if !a.narrative {
		return nil, nil
	}
	return newProvider(a.kind, a.model, a.baseURL)
}

// newProvider builds a provider, reading its key from the environment.
func newProvider(name, model, baseURL string) (provider.Provider, error) {
	kind, err := provider.ParseKind(name)
	if err != nil {
		return nil, err
	}
	key := ""
	if env := provider.KeyEnv(kind); env != "" {
		key = os.Getenv(env)
	}
	return provider.New(provider.Config{Kind: kind, Model: model, BaseURL: baseURL, APIKey: key})
}

// narrate adds a narrative to rep. It runs after the load has finished,
// and a failure is reported without changing the run's outcome.
func narrate(ctx context.Context, stderr io.Writer, rep *report.Report, p provider.Provider) {
	if p == nil {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	n, usage, err := ai.Narrate(ctx, rep, ai.NarrateOptions{Provider: p})
	if err != nil {
		fmt.Fprintf(stderr, "stampede: no narrative: %v\n", err)
		return
	}
	rep.Narrative = n
	fmt.Fprintf(stderr, "stampede: narrative by %s/%s, %d tokens\n", p.Name(), p.Model(), usage.Total())
}
