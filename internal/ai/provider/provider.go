// Package provider talks to large language model APIs for Stampede's
// optional AI journey generation. Every provider implements one small
// interface: send a system prompt and a conversation, get text (JSON when a
// schema is given) and token usage back. Keys are supplied by the caller,
// sent only in request headers and never logged or included in errors.
package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"
)

// Role is who wrote a message.
type Role string

// Message roles.
const (
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
)

// Message is one turn of a conversation.
type Message struct {
	Role    Role
	Content string
}

// Request is one chat call.
type Request struct {
	// System is the system prompt.
	System   string
	Messages []Message
	// JSONSchema, when set, asks for exactly one JSON value matching it.
	// Each provider uses the strongest mechanism it supports: a schema-typed
	// tool (Anthropic) or JSON mode (OpenAI, Gemini, Ollama, compatible
	// servers). Callers must still validate the result.
	JSONSchema json.RawMessage
	// SchemaName names the JSON value, for example "scenario".
	SchemaName string
	// MaxTokens bounds the reply (default 16000).
	MaxTokens int
}

// Usage counts tokens billed for a call.
type Usage struct {
	InputTokens  int64 `json:"inputTokens"`
	OutputTokens int64 `json:"outputTokens"`
}

// Total is input plus output tokens.
func (u Usage) Total() int64 { return u.InputTokens + u.OutputTokens }

// Add accumulates o into u.
func (u *Usage) Add(o Usage) {
	u.InputTokens += o.InputTokens
	u.OutputTokens += o.OutputTokens
}

// Response is a completed call.
type Response struct {
	// Text is the reply. With a JSONSchema it is the JSON value.
	Text  string
	Usage Usage
	// Model is the model that answered, as reported by the provider.
	Model string
	// StopReason is the provider's raw stop or finish reason.
	StopReason string
	// Truncated is set when the reply hit the token limit.
	Truncated bool
}

// Provider is a chat model API.
type Provider interface {
	// Name is the provider kind, for example "anthropic".
	Name() string
	// Model is the configured model.
	Model() string
	Chat(ctx context.Context, req *Request) (*Response, error)
}

// Kind identifies a provider implementation.
type Kind string

// Provider kinds.
const (
	Anthropic        Kind = "anthropic"
	OpenAI           Kind = "openai"
	Gemini           Kind = "gemini"
	Ollama           Kind = "ollama"
	OpenAICompatible Kind = "openai-compatible"
)

// Kinds lists every supported provider kind.
func Kinds() []Kind { return []Kind{Anthropic, OpenAI, Gemini, Ollama, OpenAICompatible} }

// ParseKind validates a provider name.
func ParseKind(s string) (Kind, error) {
	for _, k := range Kinds() {
		if string(k) == strings.ToLower(strings.TrimSpace(s)) {
			return k, nil
		}
	}
	names := make([]string, 0, len(Kinds()))
	for _, k := range Kinds() {
		names = append(names, string(k))
	}
	sort.Strings(names)
	return "", fmt.Errorf("unknown AI provider %q (use one of %s)", s, strings.Join(names, ", "))
}

// DefaultModel is the model used when none is configured. Only Anthropic
// has a default; other providers move too quickly to pick one for you.
func DefaultModel(k Kind) string {
	if k == Anthropic {
		return "claude-sonnet-5-5"
	}
	return ""
}

// KeyEnv is the environment variable the CLI reads the key from.
func KeyEnv(k Kind) string {
	switch k {
	case Anthropic:
		return "ANTHROPIC_API_KEY"
	case OpenAI:
		return "OPENAI_API_KEY"
	case Gemini:
		return "GEMINI_API_KEY"
	case OpenAICompatible:
		return "OPENAI_API_KEY"
	}
	return ""
}

// NeedsKey reports whether a provider cannot work without an API key.
func NeedsKey(k Kind) bool { return k == Anthropic || k == OpenAI || k == Gemini }

// Config selects and configures a provider.
type Config struct {
	Kind  Kind
	Model string
	// BaseURL overrides the API endpoint (required for openai-compatible).
	BaseURL string
	APIKey  string
	// HTTPClient defaults to a client with a 5 minute timeout.
	HTTPClient *http.Client
	// MaxRetries for rate limits and server errors (default 2).
	MaxRetries int
}

// New builds a provider.
func New(cfg Config) (Provider, error) {
	if cfg.Model == "" {
		cfg.Model = DefaultModel(cfg.Kind)
	}
	if cfg.Model == "" {
		return nil, fmt.Errorf("%s needs a model name", cfg.Kind)
	}
	if NeedsKey(cfg.Kind) && cfg.APIKey == "" {
		return nil, fmt.Errorf("%s needs an API key (set %s)", cfg.Kind, KeyEnv(cfg.Kind))
	}
	t := newTransport(string(cfg.Kind), cfg)
	switch cfg.Kind {
	case Anthropic:
		return newAnthropic(cfg, t), nil
	case OpenAI:
		return newOpenAI(cfg, t, false), nil
	case OpenAICompatible:
		if cfg.BaseURL == "" {
			return nil, errors.New("openai-compatible needs a base URL, for example http://localhost:8000/v1")
		}
		return newOpenAI(cfg, t, true), nil
	case Gemini:
		return newGemini(cfg, t), nil
	case Ollama:
		return newOllama(cfg, t), nil
	}
	_, err := ParseKind(string(cfg.Kind))
	return nil, err
}

// APIError is a failed provider call. It never contains the API key.
type APIError struct {
	Provider string
	Status   int
	Message  string
}

func (e *APIError) Error() string {
	if e.Status == 0 {
		return fmt.Sprintf("%s: %s", e.Provider, e.Message)
	}
	return fmt.Sprintf("%s: HTTP %d: %s", e.Provider, e.Status, e.Message)
}

// ErrTruncated is returned when a JSON reply was cut off at the token limit.
var ErrTruncated = errors.New("the reply was cut off at the token limit; raise the max tokens setting")

func defaultMaxTokens(n int) int {
	if n <= 0 {
		return 16000
	}
	return n
}

func defaultHTTPClient() *http.Client { return &http.Client{Timeout: 5 * time.Minute} }
