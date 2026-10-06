package provider

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const testKey = "sk-test-SECRETKEY-1234567890"

var schema = json.RawMessage(`{"$schema":"https://json-schema.org/draft/2020-12/schema","$id":"x","type":"object","properties":{"a":{"type":"integer"}}}`)

// capture records the last request a fake server received.
type capture struct {
	path    string
	headers http.Header
	body    map[string]any
}

func fakeServer(t *testing.T, status int, reply string) (*httptest.Server, *capture) {
	t.Helper()
	c := &capture{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c.path = r.URL.Path
		c.headers = r.Header.Clone()
		b, _ := io.ReadAll(r.Body)
		c.body = map[string]any{}
		_ = json.Unmarshal(b, &c.body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(reply))
	}))
	t.Cleanup(srv.Close)
	return srv, c
}

func newFor(t *testing.T, k Kind, base string) Provider {
	t.Helper()
	p, err := New(Config{Kind: k, Model: "m-1", BaseURL: base, APIKey: testKey, MaxRetries: -1})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func chatReq() *Request {
	return &Request{
		System:     "be helpful",
		Messages:   []Message{{Role: RoleUser, Content: "hi"}, {Role: RoleAssistant, Content: "{}"}, {Role: RoleUser, Content: "again"}},
		JSONSchema: schema, SchemaName: "scenario", MaxTokens: 1234,
	}
}

func TestAnthropicWireFormat(t *testing.T) {
	srv, c := fakeServer(t, 200, `{"model":"m-1","stop_reason":"tool_use","content":[{"type":"text","text":"Here you go"},{"type":"tool_use","name":"submit_scenario","input":{"a":1}}],"usage":{"input_tokens":10,"output_tokens":5,"cache_read_input_tokens":2}}`)
	p := newFor(t, Anthropic, srv.URL)
	resp, err := p.Chat(context.Background(), chatReq())
	if err != nil {
		t.Fatal(err)
	}
	if resp.Text != `{"a":1}` || resp.Usage.InputTokens != 12 || resp.Usage.OutputTokens != 5 {
		t.Errorf("response: %+v", resp)
	}
	if c.path != "/v1/messages" || c.headers.Get("x-api-key") != testKey || c.headers.Get("anthropic-version") == "" {
		t.Errorf("request: %s %v", c.path, c.headers)
	}
	if c.headers.Get("anthropic-beta") != "" || c.body["fallbacks"] != nil {
		t.Error("fallbacks must only be sent to the first-party API")
	}
	if c.body["max_tokens"].(float64) != 1234 || c.body["model"] != "m-1" {
		t.Errorf("body: %v", c.body)
	}
	tools := c.body["tools"].([]any)
	tool := tools[0].(map[string]any)
	is := tool["input_schema"].(map[string]any)
	if tool["name"] != "submit_scenario" || is["$schema"] != nil || is["type"] != "object" {
		t.Errorf("tool: %v", tool)
	}
	if c.body["tool_choice"].(map[string]any)["type"] != "auto" {
		t.Errorf("tool_choice: %v", c.body["tool_choice"])
	}
	msgs := c.body["messages"].([]any)
	if len(msgs) != 3 || msgs[1].(map[string]any)["role"] != "assistant" {
		t.Errorf("messages: %v", msgs)
	}
}

func TestAnthropicTextFallbackAndRefusal(t *testing.T) {
	srv, _ := fakeServer(t, 200, "{\"content\":[{\"type\":\"text\",\"text\":\"```json\\n{\\\"a\\\": 2}\\n```\"}],\"stop_reason\":\"end_turn\",\"usage\":{\"input_tokens\":1,\"output_tokens\":1}}")
	resp, err := newFor(t, Anthropic, srv.URL).Chat(context.Background(), chatReq())
	if err != nil || resp.Text != `{"a": 2}` {
		t.Fatalf("fenced JSON: %v %+v", err, resp)
	}
	srv2, _ := fakeServer(t, 200, `{"content":[],"stop_reason":"refusal","stop_details":{"category":"cyber"},"usage":{"input_tokens":7,"output_tokens":0}}`)
	resp, err = newFor(t, Anthropic, srv2.URL).Chat(context.Background(), chatReq())
	if err == nil || !strings.Contains(err.Error(), "cyber") || resp == nil || resp.Usage.InputTokens != 7 {
		t.Fatalf("refusal: %v %+v", err, resp)
	}
	srv3, _ := fakeServer(t, 200, `{"content":[{"type":"tool_use","name":"submit_scenario","input":{"a":1}}],"stop_reason":"max_tokens","usage":{}}`)
	if _, err := newFor(t, Anthropic, srv3.URL).Chat(context.Background(), chatReq()); !errors.Is(err, ErrTruncated) {
		t.Fatalf("truncated tool call: %v", err)
	}
}

func TestOpenAIWireFormat(t *testing.T) {
	srv, c := fakeServer(t, 200, `{"model":"m-1","choices":[{"message":{"content":"{\"a\":3}"},"finish_reason":"stop"}],"usage":{"prompt_tokens":20,"completion_tokens":8}}`)
	resp, err := newFor(t, OpenAI, srv.URL).Chat(context.Background(), chatReq())
	if err != nil {
		t.Fatal(err)
	}
	if resp.Text != `{"a":3}` || resp.Usage.Total() != 28 {
		t.Errorf("response: %+v", resp)
	}
	if c.path != "/chat/completions" || c.headers.Get("Authorization") != "Bearer "+testKey {
		t.Errorf("request: %s %v", c.path, c.headers)
	}
	if c.body["max_completion_tokens"].(float64) != 1234 || c.body["response_format"].(map[string]any)["type"] != "json_object" {
		t.Errorf("body: %v", c.body)
	}
	msgs := c.body["messages"].([]any)
	sys := msgs[0].(map[string]any)
	if sys["role"] != "system" || !strings.Contains(sys["content"].(string), `"properties"`) {
		t.Errorf("system message should carry the schema: %v", sys)
	}
}

func TestOpenAICompatibleDropsJSONModeWhenRejected(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		calls.Add(1)
		if r.Header.Get("Authorization") != "" {
			t.Error("no key configured, so no Authorization header")
		}
		if _, ok := body["response_format"]; ok {
			w.WriteHeader(400)
			_, _ = w.Write([]byte(`{"error":{"message":"response_format is not supported"}}`))
			return
		}
		if body["max_tokens"] == nil {
			t.Error("compatible servers get max_tokens")
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"sure: {\"a\":4}"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1}}`))
	}))
	defer srv.Close()
	p, err := New(Config{Kind: OpenAICompatible, Model: "local", BaseURL: srv.URL, MaxRetries: -1})
	if err != nil {
		t.Fatal(err)
	}
	resp, err := p.Chat(context.Background(), chatReq())
	if err != nil || resp.Text != `{"a":4}` || calls.Load() != 2 {
		t.Fatalf("%v %+v calls=%d", err, resp, calls.Load())
	}
}

func TestGeminiWireFormat(t *testing.T) {
	srv, c := fakeServer(t, 200, `{"candidates":[{"content":{"role":"model","parts":[{"text":"thinking...","thought":true},{"text":"{\"a\":5}"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":30,"candidatesTokenCount":4,"thoughtsTokenCount":6},"modelVersion":"m-1"}`)
	resp, err := newFor(t, Gemini, srv.URL).Chat(context.Background(), chatReq())
	if err != nil {
		t.Fatal(err)
	}
	if resp.Text != `{"a":5}` || resp.Usage.InputTokens != 30 || resp.Usage.OutputTokens != 10 {
		t.Errorf("response: %+v", resp)
	}
	if c.path != "/models/m-1:generateContent" || c.headers.Get("x-goog-api-key") != testKey {
		t.Errorf("request: %s %v", c.path, c.headers)
	}
	gc := c.body["generationConfig"].(map[string]any)
	if gc["responseMimeType"] != "application/json" || gc["maxOutputTokens"].(float64) != 1234 {
		t.Errorf("generationConfig: %v", gc)
	}
	contents := c.body["contents"].([]any)
	if contents[1].(map[string]any)["role"] != "model" {
		t.Errorf("assistant turns map to role model: %v", contents)
	}
	if c.body["systemInstruction"] == nil {
		t.Error("system instruction missing")
	}
	srv2, _ := fakeServer(t, 200, `{"candidates":[{"content":{"parts":[]},"finishReason":"SAFETY"}],"usageMetadata":{"promptTokenCount":3}}`)
	if _, err := newFor(t, Gemini, srv2.URL).Chat(context.Background(), chatReq()); err == nil || !strings.Contains(err.Error(), "SAFETY") {
		t.Errorf("safety block: %v", err)
	}
}

func TestOllamaWireFormat(t *testing.T) {
	srv, c := fakeServer(t, 200, `{"model":"m-1","message":{"role":"assistant","content":"{\"a\":6}"},"done":true,"done_reason":"stop","prompt_eval_count":40,"eval_count":9}`)
	p, err := New(Config{Kind: Ollama, Model: "llama", BaseURL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	resp, err := p.Chat(context.Background(), chatReq())
	if err != nil {
		t.Fatal(err)
	}
	if resp.Text != `{"a":6}` || resp.Usage.Total() != 49 {
		t.Errorf("response: %+v", resp)
	}
	if c.path != "/api/chat" || c.body["stream"] != false || c.body["format"] != "json" {
		t.Errorf("request: %s %v", c.path, c.body)
	}
	if c.body["options"].(map[string]any)["num_predict"].(float64) != 1234 {
		t.Errorf("options: %v", c.body["options"])
	}
	srv2, _ := fakeServer(t, 200, `{"message":{"content":"{\"a\":"},"done_reason":"length","eval_count":1}`)
	p2, _ := New(Config{Kind: Ollama, Model: "llama", BaseURL: srv2.URL})
	if _, err := p2.Chat(context.Background(), chatReq()); !errors.Is(err, ErrTruncated) {
		t.Errorf("truncation: %v", err)
	}
}

func TestErrorsNeverContainTheKey(t *testing.T) {
	srv, _ := fakeServer(t, 401, `{"error":{"type":"authentication_error","message":"invalid x-api-key `+testKey+`"}}`)
	for _, k := range []Kind{Anthropic, OpenAI, Gemini} {
		_, err := newFor(t, k, srv.URL).Chat(context.Background(), chatReq())
		var ae *APIError
		if !errors.As(err, &ae) || ae.Status != 401 {
			t.Fatalf("%s: %v", k, err)
		}
		if strings.Contains(err.Error(), testKey) {
			t.Errorf("%s: error leaks the key: %v", k, err)
		}
	}
}

func TestRetriesRateLimits(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) < 3 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(429)
			_, _ = w.Write([]byte(`{"error":{"message":"slow down"}}`))
			return
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"{}"},"finish_reason":"stop"}],"usage":{}}`))
	}))
	defer srv.Close()
	p, _ := New(Config{Kind: OpenAI, Model: "m", BaseURL: srv.URL, APIKey: "k"})
	p.(*openai).t.sleep = func(context.Context, time.Duration) error { return nil }
	if _, err := p.Chat(context.Background(), chatReq()); err != nil || calls.Load() != 3 {
		t.Fatalf("%v after %d calls", err, calls.Load())
	}
	// A client error is not retried.
	calls.Store(0)
	srv2, _ := fakeServer(t, 400, `{"error":{"message":"bad"}}`)
	p2, _ := New(Config{Kind: OpenAI, Model: "m", BaseURL: srv2.URL, APIKey: "k"})
	if _, err := p2.Chat(context.Background(), chatReq()); err == nil {
		t.Fatal("expected an error")
	}
}

func TestConfigValidation(t *testing.T) {
	if _, err := New(Config{Kind: Anthropic}); err == nil || !strings.Contains(err.Error(), "ANTHROPIC_API_KEY") {
		t.Errorf("missing key: %v", err)
	}
	if _, err := New(Config{Kind: OpenAI, APIKey: "k"}); err == nil {
		t.Error("openai without a model should fail")
	}
	if _, err := New(Config{Kind: OpenAICompatible, Model: "m"}); err == nil {
		t.Error("openai-compatible without a base URL should fail")
	}
	if p, err := New(Config{Kind: Anthropic, APIKey: "k"}); err != nil || p.Model() != "claude-sonnet-5-5" {
		t.Errorf("default model: %v", err)
	}
	if _, err := ParseKind("bard"); err == nil {
		t.Error("unknown kind accepted")
	}
}

func TestAnthropicFallbacksOnFirstPartyAPI(t *testing.T) {
	var sawBeta atomic.Bool
	rt := roundTripper(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != "api.anthropic.com" {
			t.Errorf("host %s", r.URL.Host)
		}
		sawBeta.Store(r.Header.Get("anthropic-beta") == "server-side-fallback-2026-07-01")
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"content":[{"type":"text","text":"{}"}],"stop_reason":"end_turn","usage":{}}`))}, nil
	})
	p, _ := New(Config{Kind: Anthropic, APIKey: "k", HTTPClient: &http.Client{Transport: rt}})
	if _, err := p.Chat(context.Background(), chatReq()); err != nil || !sawBeta.Load() {
		t.Fatalf("%v beta=%v", err, sawBeta.Load())
	}
}

type roundTripper func(*http.Request) (*http.Response, error)

func (f roundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestFake(t *testing.T) {
	f := &Fake{Replies: []any{"text {\"x\":1} more", errors.New("boom")}, PerCall: Usage{InputTokens: 3, OutputTokens: 2}}
	r, err := f.Chat(context.Background(), chatReq())
	if err != nil || r.Text != `{"x":1}` || r.Usage.Total() != 5 {
		t.Fatalf("%v %+v", err, r)
	}
	if _, err := f.Chat(context.Background(), chatReq()); err == nil {
		t.Fatal("scripted error not returned")
	}
	if len(f.Requests()) != 2 {
		t.Fatal("requests not recorded")
	}
}
