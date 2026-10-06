package provider

import (
	"context"
	"strings"
)

const ollamaDefaultBase = "http://localhost:11434"

// ollama implements a local Ollama server's chat API. No key is needed and
// nothing leaves the machine. With a schema it uses Ollama's JSON format
// mode and puts the schema in the system prompt.
type ollama struct {
	cfg  Config
	t    *transport
	base string
}

func newOllama(cfg Config, t *transport) *ollama {
	base := strings.TrimRight(cfg.BaseURL, "/")
	if base == "" {
		base = ollamaDefaultBase
	}
	return &ollama{cfg: cfg, t: t, base: base}
}

func (o *ollama) Name() string  { return string(Ollama) }
func (o *ollama) Model() string { return o.cfg.Model }

type ollamaRequest struct {
	Model    string          `json:"model"`
	Messages []openAIMessage `json:"messages"`
	Stream   bool            `json:"stream"`
	Format   string          `json:"format,omitempty"`
	Options  map[string]any  `json:"options,omitempty"`
}

type ollamaResponse struct {
	Model   string `json:"model"`
	Message struct {
		Content string `json:"content"`
	} `json:"message"`
	DoneReason      string `json:"done_reason"`
	PromptEvalCount int64  `json:"prompt_eval_count"`
	EvalCount       int64  `json:"eval_count"`
}

func (o *ollama) Chat(ctx context.Context, req *Request) (*Response, error) {
	// The default context window of many local models (2-4k tokens) is too
	// small for an API description plus the schema.
	body := ollamaRequest{Model: o.cfg.Model, Options: map[string]any{"num_predict": defaultMaxTokens(req.MaxTokens), "num_ctx": 32768}}
	if sys := schemaInstruction(req); sys != "" {
		body.Messages = append(body.Messages, openAIMessage{Role: "system", Content: sys})
	}
	for _, m := range req.Messages {
		body.Messages = append(body.Messages, openAIMessage{Role: string(m.Role), Content: m.Content})
	}
	if len(req.JSONSchema) > 0 {
		body.Format = "json"
	}
	var out ollamaResponse
	if err := o.t.postJSON(ctx, o.base+"/api/chat", nil, body, &out); err != nil {
		return nil, err
	}
	resp := &Response{
		Text: out.Message.Content, Model: out.Model, StopReason: out.DoneReason, Truncated: out.DoneReason == "length",
		Usage: Usage{InputTokens: out.PromptEvalCount, OutputTokens: out.EvalCount},
	}
	return finishJSON(req, resp)
}
