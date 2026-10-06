# AI and LLM apps pack

Journeys, stresses and targets for streaming chat APIs. The number users
feel is time to first token, so every streaming step records it: the report
shows `first p50/p95/p99` and events (tokens) per second per streaming step.

| File | What it tests |
|---|---|
| `journeys/chat-mix.yaml` | Everyday mix: chat with a follow-up 60%, long answers 15%, embeddings 15%, model list 10% |
| `journeys/bad-request.yaml` | An unknown model or an empty prompt is refused at once with a 4xx, not a broken stream |
| `stresses/concurrency-ramp.yaml` | Users added in steps (4, 16, 32, 64); first-token time per step shows queueing before generation |
| `stresses/long-context.yaml` | One in ten requests carries a 16,000-character document; do short chats stall behind it? |
| `stresses/long-streams.yaml` | 32 replies of 2,048 tokens streamed at once for 10 minutes; streams must stay open and complete |
| `targets.yaml` | Default targets: chat p95 under 20s, under 1% errors, model list under 300ms |

The journeys follow the OpenAI chat completions API (`POST
/v1/chat/completions` with `stream: true`, `GET /v1/models`, `POST
/v1/embeddings`), which vLLM, Ollama, LiteLLM and most gateways also
speak. Streams stop at the chunk with `"finish_reason":"stop"`, so checks
can read its `usage`. Every journey and stress is run against
[LLMLab](../../examples/packlab/README.md#llmlab) in CI. For your own API,
change the model names, and add an `Authorization` header under
`target.headers` if it needs a key:

```yaml
target:
  baseURL: ${env.TARGET_URL}
  headers: { Authorization: "Bearer ${secret.LLM_API_KEY}" }
```

```sh
go run ./examples/packlab -product llm-apps          # LLMLab on :8092
stampede init --target http://localhost:8092         # detects this pack
stampede run stampede/llm-apps/journeys/chat-mix.yaml -e TARGET_URL=http://localhost:8092
```
