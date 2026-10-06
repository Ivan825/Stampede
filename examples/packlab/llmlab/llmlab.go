// Package llmlab is LLMLab, an OpenAI-compatible chat completions API that
// streams made-up tokens over server-sent events. It is the reference app
// for the llm-apps pack.
//
// Planted bottlenecks (see README.md), each switched off by a fix flag:
//
//   - slots: only four generations run at once; the rest queue before their
//     first token, so time to first token grows with concurrency (fix
//     "batch" allows 256).
//   - prefill: prompt processing holds one global lock, so a long prompt
//     delays the first token of every other request (fix "chunked").
//   - usage: usage accounting re-tokenises the whole reply after every
//     token, so CPU per stream grows with the square of its length (fix
//     "usage").
package llmlab

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/Ivan825/Stampede/examples/packlab/labkit"
)

var routes = []labkit.Route{
	{Method: "GET", Path: "/v1/models", Tag: "models", Summary: "List models"},
	{Method: "POST", Path: "/v1/chat/completions", Tag: "chat", Summary: "Create a chat completion (stream: true for server-sent events)"},
	{Method: "POST", Path: "/v1/completions", Tag: "completions", Summary: "Create a text completion"},
	{Method: "POST", Path: "/v1/embeddings", Tag: "embeddings", Summary: "Embed text"},
	{Method: "GET", Path: "/healthz", Tag: "ops", Summary: "Liveness"},
}

var words = strings.Fields(`the a load test measures how a system behaves when many people use it at
once and a stream of tokens arrives one by one so the first token matters most to how fast the answer
feels while tokens per second decide how long the whole reply takes`)

type server struct {
	cfg        labkit.Config
	slots      chan struct{}
	prefill    sync.Mutex
	tokenDelay time.Duration
	// prefillPerChar is the prompt-processing cost per prompt character.
	prefillPerChar time.Duration
	// work counts tokeniser passes, so tests can see the usage bottleneck.
	work labkit.Counter
	// gen and pre track concurrent generations and prompt processing.
	gen, pre labkit.Gauge
}

// New returns LLMLab's handler.
func New(cfg labkit.Config) http.Handler {
	_, h := newServer(cfg)
	return h
}

func newServer(cfg labkit.Config) (*server, http.Handler) {
	s := &server{cfg: cfg, tokenDelay: 10 * time.Millisecond, prefillPerChar: 20 * time.Microsecond}
	if cfg.Fast {
		s.tokenDelay, s.prefillPerChar = 0, 0
	}
	n := 4
	if cfg.Fixes.On("batch") {
		n = 256
	}
	s.slots = make(chan struct{}, n)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, _ *http.Request) {
		labkit.JSON(w, 200, map[string]any{"name": "LLMLab", "links": map[string]string{"openapi": "/openapi.json"}})
	})
	mux.Handle("GET /openapi.json", labkit.OpenAPI("LLMLab", "OpenAI-compatible chat completions API with planted bottlenecks.", routes))
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { labkit.JSON(w, 200, map[string]string{"status": "ok"}) })
	mux.HandleFunc("GET /v1/models", func(w http.ResponseWriter, _ *http.Request) {
		labkit.JSON(w, 200, map[string]any{"object": "list", "data": []map[string]any{
			{"id": "lab-small", "object": "model", "owned_by": "llmlab"},
			{"id": "lab-large", "object": "model", "owned_by": "llmlab"},
		}})
	})
	mux.HandleFunc("POST /v1/chat/completions", s.chat)
	mux.HandleFunc("POST /v1/completions", s.chat)
	mux.HandleFunc("POST /v1/embeddings", s.embeddings)
	return s, mux
}

type chatRequest struct {
	Model    string `json:"model"`
	Stream   bool   `json:"stream"`
	Prompt   string `json:"prompt"`
	Messages []struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	} `json:"messages"`
	MaxTokens int `json:"max_tokens"`
}

func (s *server) chat(w http.ResponseWriter, r *http.Request) {
	var req chatRequest
	if !labkit.Decode(w, r, &req) {
		return
	}
	if req.Model == "" {
		req.Model = "lab-small"
	}
	if req.Model != "lab-small" && req.Model != "lab-large" {
		labkit.Error(w, 404, "model_not_found", "unknown model "+req.Model)
		return
	}
	prompt := req.Prompt
	for _, m := range req.Messages {
		prompt += m.Content + "\n"
	}
	if strings.TrimSpace(prompt) == "" {
		labkit.Error(w, 400, "invalid_request", "messages or prompt is required")
		return
	}
	n := req.MaxTokens
	if n <= 0 {
		n = 128
	}
	if n > 4096 {
		n = 4096
	}

	// Wait for a generation slot (bottleneck: only four without "batch").
	select {
	case s.slots <- struct{}{}:
	case <-r.Context().Done():
		return
	}
	defer func() { <-s.slots }()
	defer s.gen.Enter()()

	// Prompt processing (bottleneck: one global lock without "chunked").
	cost := time.Duration(len(prompt)) * s.prefillPerChar
	if !s.cfg.Fixes.On("chunked") {
		s.prefill.Lock()
		done := s.pre.Enter()
		time.Sleep(cost)
		done()
		s.prefill.Unlock()
	} else {
		done := s.pre.Enter()
		time.Sleep(cost)
		done()
	}

	id := labkit.Token("chatcmpl-")
	promptTokens := len(strings.Fields(prompt))
	if !req.Stream {
		var b strings.Builder
		for i := 0; i < n; i++ {
			b.WriteString(words[i%len(words)] + " ")
			time.Sleep(s.tokenDelay)
		}
		labkit.JSON(w, 200, map[string]any{
			"id": id, "object": "chat.completion", "model": req.Model,
			"choices": []map[string]any{{"index": 0, "finish_reason": "stop", "message": map[string]string{"role": "assistant", "content": b.String()}}},
			"usage":   map[string]int{"prompt_tokens": promptTokens, "completion_tokens": n, "total_tokens": promptTokens + n},
		})
		return
	}

	fl, ok := w.(http.Flusher)
	if !ok {
		labkit.Error(w, 500, "no_streaming", "streaming unsupported")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(200)
	send := func(v any) bool {
		b, _ := json.Marshal(v)
		if _, err := fmt.Fprintf(w, "data: %s\n\n", b); err != nil {
			return false
		}
		fl.Flush()
		return r.Context().Err() == nil
	}
	chunk := func(delta map[string]string, finish any) map[string]any {
		return map[string]any{"id": id, "object": "chat.completion.chunk", "model": req.Model,
			"choices": []map[string]any{{"index": 0, "delta": delta, "finish_reason": finish}}}
	}
	if !send(chunk(map[string]string{"role": "assistant"}, nil)) {
		return
	}
	var reply strings.Builder
	usage := 0
	for i := 0; i < n; i++ {
		time.Sleep(s.tokenDelay)
		tok := words[i%len(words)] + " "
		reply.WriteString(tok)
		if s.cfg.Fixes.On("usage") {
			usage++
		} else {
			// Bottleneck: count the reply's tokens from scratch every time.
			usage = len(strings.Fields(reply.String()))
			s.work.Add(usage)
		}
		if !send(chunk(map[string]string{"content": tok}, nil)) {
			return
		}
	}
	last := chunk(map[string]string{}, "stop")
	last["usage"] = map[string]int{"prompt_tokens": promptTokens, "completion_tokens": usage, "total_tokens": promptTokens + usage}
	if !send(last) {
		return
	}
	fmt.Fprint(w, "data: [DONE]\n\n")
	fl.Flush()
}

func (s *server) embeddings(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Model string `json:"model"`
		Input any    `json:"input"`
	}
	if !labkit.Decode(w, r, &req) {
		return
	}
	text, _ := req.Input.(string)
	if text == "" {
		labkit.Error(w, 400, "invalid_request", "input must be a non-empty string")
		return
	}
	vec := make([]float64, 16)
	for i, c := range text {
		vec[i%16] += float64(c%13) / 13
	}
	labkit.JSON(w, 200, map[string]any{"object": "list", "model": "lab-embed",
		"data":  []map[string]any{{"object": "embedding", "index": 0, "embedding": vec}},
		"usage": map[string]int{"prompt_tokens": len(strings.Fields(text)), "total_tokens": len(strings.Fields(text))}})
}
