package provider

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
)

const anthropicDefaultBase = "https://api.anthropic.com"

// anthropic implements the Messages API. With a schema it offers a tool
// whose input_schema is the schema and asks the model to call it, so the
// reply is a JSON object shaped by the schema. tool_choice stays "auto"
// because current models reject forced tool choice; a text reply holding
// JSON is accepted as a fallback.
type anthropic struct {
	cfg  Config
	t    *transport
	base string
	// fallbacks enables server-side refusal fallbacks on the first-party
	// API; it is switched off for the rest of the process if rejected.
	fallbacks atomic.Bool
}

func newAnthropic(cfg Config, t *transport) *anthropic {
	base := strings.TrimRight(cfg.BaseURL, "/")
	if base == "" {
		base = anthropicDefaultBase
	}
	a := &anthropic{cfg: cfg, t: t, base: base}
	a.fallbacks.Store(base == anthropicDefaultBase && supportsFallbacks(cfg.Model))
	return a
}

func supportsFallbacks(model string) bool {
	for _, p := range []string{"claude-sonnet-5-5", "claude-opus-5", "claude-fable-5"} {
		if strings.HasPrefix(model, p) {
			return true
		}
	}
	return false
}

func (a *anthropic) Name() string  { return string(Anthropic) }
func (a *anthropic) Model() string { return a.cfg.Model }

type anthropicMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type anthropicTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema"`
}

type anthropicRequest struct {
	Model      string             `json:"model"`
	MaxTokens  int                `json:"max_tokens"`
	System     string             `json:"system,omitempty"`
	Messages   []anthropicMessage `json:"messages"`
	Tools      []anthropicTool    `json:"tools,omitempty"`
	ToolChoice map[string]string  `json:"tool_choice,omitempty"`
	Fallbacks  string             `json:"fallbacks,omitempty"`
}

type anthropicResponse struct {
	Model   string `json:"model"`
	Content []struct {
		Type  string          `json:"type"`
		Text  string          `json:"text"`
		Name  string          `json:"name"`
		Input json.RawMessage `json:"input"`
	} `json:"content"`
	StopReason  string `json:"stop_reason"`
	StopDetails *struct {
		Category    string `json:"category"`
		Explanation string `json:"explanation"`
	} `json:"stop_details"`
	Usage struct {
		InputTokens              int64 `json:"input_tokens"`
		OutputTokens             int64 `json:"output_tokens"`
		CacheCreationInputTokens int64 `json:"cache_creation_input_tokens"`
		CacheReadInputTokens     int64 `json:"cache_read_input_tokens"`
	} `json:"usage"`
}

func toolName(req *Request) string {
	n := req.SchemaName
	if n == "" {
		n = "result"
	}
	return "submit_" + n
}

// toolSchema drops keys that are not part of an input schema.
func toolSchema(s json.RawMessage) json.RawMessage {
	var m map[string]json.RawMessage
	if json.Unmarshal(s, &m) != nil {
		return s
	}
	delete(m, "$schema")
	delete(m, "$id")
	b, err := json.Marshal(m)
	if err != nil {
		return s
	}
	return b
}

func (a *anthropic) Chat(ctx context.Context, req *Request) (*Response, error) {
	body := anthropicRequest{Model: a.cfg.Model, MaxTokens: defaultMaxTokens(req.MaxTokens), System: req.System}
	for _, m := range req.Messages {
		body.Messages = append(body.Messages, anthropicMessage{Role: string(m.Role), Content: m.Content})
	}
	tool := ""
	if len(req.JSONSchema) > 0 {
		tool = toolName(req)
		body.Tools = []anthropicTool{{
			Name:        tool,
			Description: "Submit the complete " + strings.TrimPrefix(tool, "submit_") + ". Always call this tool exactly once with the full result as its input.",
			InputSchema: toolSchema(req.JSONSchema),
		}}
		body.ToolChoice = map[string]string{"type": "auto"}
		body.System = strings.TrimSpace(body.System + "\n\nReply by calling the " + tool + " tool exactly once with the complete JSON object as its input.")
	}
	headers := map[string]string{"x-api-key": a.cfg.APIKey, "anthropic-version": "2023-06-01"}

	var out anthropicResponse
	for {
		h := headers
		useFallbacks := a.fallbacks.Load()
		if useFallbacks {
			body.Fallbacks = "default"
			h = map[string]string{"x-api-key": a.cfg.APIKey, "anthropic-version": "2023-06-01", "anthropic-beta": "server-side-fallback-2026-07-01"}
		} else {
			body.Fallbacks = ""
		}
		err := a.t.postJSON(ctx, a.base+"/v1/messages", h, body, &out)
		if err != nil {
			// A deployment without the fallback beta: retry once without it.
			if ae := (*APIError)(nil); errors.As(err, &ae) && useFallbacks && ae.Status == 400 && strings.Contains(strings.ToLower(ae.Message), "fallback") {
				a.fallbacks.Store(false)
				continue
			}
			return nil, err
		}
		break
	}

	resp := &Response{
		Model: out.Model, StopReason: out.StopReason, Truncated: out.StopReason == "max_tokens",
		Usage: Usage{
			InputTokens:  out.Usage.InputTokens + out.Usage.CacheCreationInputTokens + out.Usage.CacheReadInputTokens,
			OutputTokens: out.Usage.OutputTokens,
		},
	}
	if out.StopReason == "refusal" {
		msg := "the model declined the request"
		if d := out.StopDetails; d != nil && (d.Category != "" || d.Explanation != "") {
			msg += " (" + strings.TrimSpace(d.Category+" "+d.Explanation) + ")"
		}
		return resp, &APIError{Provider: a.Name(), Message: msg}
	}
	var text strings.Builder
	for _, c := range out.Content {
		switch c.Type {
		case "tool_use":
			if c.Name == tool && len(c.Input) > 0 {
				if resp.Truncated {
					return resp, ErrTruncated
				}
				resp.Text = string(c.Input)
				return resp, nil
			}
		case "text":
			text.WriteString(c.Text)
		}
	}
	resp.Text = text.String()
	return finishJSON(req, resp)
}
