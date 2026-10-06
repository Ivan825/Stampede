package provider

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
)

const openAIDefaultBase = "https://api.openai.com/v1"

// openai implements Chat Completions, for OpenAI itself and for any
// server that speaks the same protocol (vLLM, LM Studio, LiteLLM, llama.cpp
// server, Azure-style gateways). With a schema it turns on JSON mode and
// puts the schema in the prompt; the caller validates and repairs.
type openai struct {
	cfg    Config
	t      *transport
	base   string
	compat bool
	// jsonMode is cleared when a compatible server rejects response_format.
	jsonMode atomic.Bool
}

func newOpenAI(cfg Config, t *transport, compat bool) *openai {
	base := strings.TrimRight(cfg.BaseURL, "/")
	if base == "" {
		base = openAIDefaultBase
	}
	o := &openai{cfg: cfg, t: t, base: base, compat: compat}
	o.jsonMode.Store(true)
	return o
}

func (o *openai) Name() string {
	if o.compat {
		return string(OpenAICompatible)
	}
	return string(OpenAI)
}
func (o *openai) Model() string { return o.cfg.Model }

type openAIMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type openAIRequest struct {
	Model               string            `json:"model"`
	Messages            []openAIMessage   `json:"messages"`
	MaxCompletionTokens int               `json:"max_completion_tokens,omitempty"`
	MaxTokens           int               `json:"max_tokens,omitempty"`
	ResponseFormat      map[string]string `json:"response_format,omitempty"`
}

type openAIResponse struct {
	Model   string `json:"model"`
	Choices []struct {
		Message struct {
			Content string `json:"content"`
			Refusal string `json:"refusal"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int64 `json:"prompt_tokens"`
		CompletionTokens int64 `json:"completion_tokens"`
	} `json:"usage"`
}

// schemaInstruction appends the schema to a system prompt for providers
// that only offer a generic JSON mode.
func schemaInstruction(req *Request) string {
	if len(req.JSONSchema) == 0 {
		return req.System
	}
	return strings.TrimSpace(req.System) + "\n\nReply with exactly one JSON object and nothing else (no markdown, no prose). It must validate against this JSON Schema:\n" + string(req.JSONSchema)
}

func (o *openai) Chat(ctx context.Context, req *Request) (*Response, error) {
	body := openAIRequest{Model: o.cfg.Model}
	if sys := schemaInstruction(req); sys != "" {
		body.Messages = append(body.Messages, openAIMessage{Role: "system", Content: sys})
	}
	for _, m := range req.Messages {
		body.Messages = append(body.Messages, openAIMessage{Role: string(m.Role), Content: m.Content})
	}
	if o.compat {
		body.MaxTokens = defaultMaxTokens(req.MaxTokens)
	} else {
		body.MaxCompletionTokens = defaultMaxTokens(req.MaxTokens)
	}
	headers := map[string]string{}
	if o.cfg.APIKey != "" {
		headers["Authorization"] = "Bearer " + o.cfg.APIKey
	}

	var out openAIResponse
	for {
		useJSON := len(req.JSONSchema) > 0 && o.jsonMode.Load()
		body.ResponseFormat = nil
		if useJSON {
			body.ResponseFormat = map[string]string{"type": "json_object"}
		}
		err := o.t.postJSON(ctx, o.base+"/chat/completions", headers, body, &out)
		if err != nil {
			// Some compatible servers do not implement JSON mode.
			if ae := (*APIError)(nil); errors.As(err, &ae) && o.compat && useJSON && ae.Status == 400 && strings.Contains(strings.ToLower(ae.Message), "response_format") {
				o.jsonMode.Store(false)
				continue
			}
			return nil, err
		}
		break
	}
	resp := &Response{Model: out.Model, Usage: Usage{InputTokens: out.Usage.PromptTokens, OutputTokens: out.Usage.CompletionTokens}}
	if len(out.Choices) == 0 {
		return resp, &APIError{Provider: o.Name(), Message: "the reply has no choices"}
	}
	c := out.Choices[0]
	resp.StopReason, resp.Truncated = c.FinishReason, c.FinishReason == "length"
	if c.Message.Refusal != "" {
		return resp, &APIError{Provider: o.Name(), Message: "the model declined the request: " + c.Message.Refusal}
	}
	resp.Text = c.Message.Content
	return finishJSON(req, resp)
}
