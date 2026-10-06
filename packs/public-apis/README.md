# Public APIs pack

Journeys, stresses and targets for public developer APIs: API keys, rate
limits, idempotent writes and webhooks.

| File | What it tests |
|---|---|
| `journeys/api-mix.yaml` | Everyday mix across 500 keys: page through a list 45%, read a record 25%, create with an idempotency key and retry it (the retry must return the same record) 10%, usage 10%, send an event and wait for its webhook delivery 10% |
| `journeys/auth-errors.yaml` | A missing or unknown key gets a quick 401 (with `WWW-Authenticate`), a missing record a 404 |
| `stresses/rate-limit-burst.yaml` | Ten users share one free-plan key and burst past its limit; every refusal must be a fast 429 with `Retry-After`, and the client backs off for that long |
| `stresses/noisy-neighbour.yaml` | One client floods and ignores its 429s while others behave; the well-behaved journey must keep p95 under 200ms and errors under 0.1% |
| `stresses/webhook-burst.yaml` | Events spike from 10/s to 80/s and each waits for its delivery; a delivery that has not landed within 30 seconds fails |
| `targets.yaml` | Default targets: p95 under 300ms, under 1% errors, webhook journey under 5s |

The journeys follow APILab's API: the key in an `X-API-Key` header,
`/v1/items` with cursor pagination (`next_cursor`), `Idempotency-Key` on
creates (echoed back, with `Idempotent-Replayed: true` on a replay),
`/v1/usage`, `POST /v1/webhooks/test` and `GET /v1/deliveries/{id}`. Every
journey and stress is run against
[APILab](../../examples/packlab/README.md#apilab) in CI, and the test
checks that the burst stress really provokes 429s.

For your own API, change the header name (or use `Authorization: Bearer`),
the paths and the JSONPath extractors, put your test keys in
`data/api-keys.csv` (one column, `key`) and a key on a low plan under
`vars` in the two stresses. Rate-limited APIs are detected by an
`X-RateLimit-Limit` or `RateLimit-Limit` response header.

```sh
go run ./examples/packlab -product public-apis       # APILab on :8096
stampede init --target http://localhost:8096         # detects this pack
stampede run stampede/public-apis/journeys/api-mix.yaml -e TARGET_URL=http://localhost:8096
```
