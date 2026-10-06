package provider

import (
	"context"
	"net/url"
	"strings"
)

const geminiDefaultBase = "https://generativelanguage.googleapis.com/v1beta"

// gemini implements the Gemini generateContent API. With a schema it sets
// the JSON response MIME type and puts the schema in the system
// instruction (Gemini's own response schema supports only a subset of
// JSON Schema, which the scenario schema exceeds).
type gemini struct {
	cfg  Config
	t    *transport
	base string
}

func newGemini(cfg Config, t *transport) *gemini {
	base := strings.TrimRight(cfg.BaseURL, "/")
	if base == "" {
		base = geminiDefaultBase
	}
	return &gemini{cfg: cfg, t: t, base: base}
}

func (g *gemini) Name() string  { return string(Gemini) }
func (g *gemini) Model() string { return g.cfg.Model }

type geminiPart struct {
	Text    string `json:"text"`
	Thought bool   `json:"thought,omitempty"`
}

type geminiContent struct {
	Role  string       `json:"role,omitempty"`
	Parts []geminiPart `json:"parts"`
}

type geminiRequest struct {
	SystemInstruction *geminiContent  `json:"systemInstruction,omitempty"`
	Contents          []geminiContent `json:"contents"`
	GenerationConfig  map[string]any  `json:"generationConfig"`
}

type geminiResponse struct {
	Candidates []struct {
		Content      geminiContent `json:"content"`
		FinishReason string        `json:"finishReason"`
	} `json:"candidates"`
	PromptFeedback *struct {
		BlockReason string `json:"blockReason"`
	} `json:"promptFeedback"`
	UsageMetadata struct {
		PromptTokenCount     int64 `json:"promptTokenCount"`
		CandidatesTokenCount int64 `json:"candidatesTokenCount"`
		ThoughtsTokenCount   int64 `json:"thoughtsTokenCount"`
	} `json:"usageMetadata"`
	ModelVersion string `json:"modelVersion"`
}

func (g *gemini) Chat(ctx context.Context, req *Request) (*Response, error) {
	body := geminiRequest{GenerationConfig: map[string]any{"maxOutputTokens": defaultMaxTokens(req.MaxTokens)}}
	if sys := schemaInstruction(req); sys != "" {
		body.SystemInstruction = &geminiContent{Parts: []geminiPart{{Text: sys}}}
	}
	for _, m := range req.Messages {
		role := "user"
		if m.Role == RoleAssistant {
			role = "model"
		}
		body.Contents = append(body.Contents, geminiContent{Role: role, Parts: []geminiPart{{Text: m.Content}}})
	}
	if len(req.JSONSchema) > 0 {
		body.GenerationConfig["responseMimeType"] = "application/json"
	}
	var out geminiResponse
	u := g.base + "/models/" + url.PathEscape(g.cfg.Model) + ":generateContent"
	if err := g.t.postJSON(ctx, u, map[string]string{"x-goog-api-key": g.cfg.APIKey}, body, &out); err != nil {
		return nil, err
	}
	resp := &Response{Model: out.ModelVersion, Usage: Usage{
		InputTokens:  out.UsageMetadata.PromptTokenCount,
		OutputTokens: out.UsageMetadata.CandidatesTokenCount + out.UsageMetadata.ThoughtsTokenCount,
	}}
	if out.PromptFeedback != nil && out.PromptFeedback.BlockReason != "" {
		return resp, &APIError{Provider: g.Name(), Message: "the prompt was blocked (" + out.PromptFeedback.BlockReason + ")"}
	}
	if len(out.Candidates) == 0 {
		return resp, &APIError{Provider: g.Name(), Message: "the reply has no candidates"}
	}
	c := out.Candidates[0]
	resp.StopReason, resp.Truncated = c.FinishReason, c.FinishReason == "MAX_TOKENS"
	switch c.FinishReason {
	case "SAFETY", "RECITATION", "PROHIBITED_CONTENT", "BLOCKLIST", "SPII":
		return resp, &APIError{Provider: g.Name(), Message: "the reply was blocked (" + c.FinishReason + ")"}
	}
	var b strings.Builder
	for _, p := range c.Content.Parts {
		if !p.Thought {
			b.WriteString(p.Text)
		}
	}
	resp.Text = b.String()
	return finishJSON(req, resp)
}
