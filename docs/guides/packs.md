# Product packs

A pack is a folder of journeys, stresses and default targets for one kind of
product. Packs need no engine changes: adding a product type is adding a
folder.

```sh
stampede pack list                      # all 20 product types, shipped or planned
stampede init --target http://localhost:8090   # detect, install, dry-run
stampede pack install ecommerce --dir stampede
stampede pack test ecommerce --target http://localhost:8090
```

## Shipped packs

Seven packs are shipped. A pack is shipped only when its journeys and
stresses run without errors against a reference app in CI; the other 13
product types are listed as planned until each has one.

| Pack | Protocols | What it tests | Reference app |
|---|---|---|---|
| [`ecommerce`](../../packs/ecommerce) | HTTP | Browse, search, cart and checkout; flash-sale spike; last-item contention | [ShopLab](../../examples/shoplab) (PostgreSQL, Redis), port 8090 |
| [`saas`](../../packs/saas) | HTTP, GraphQL | Sign-in, dashboards over GraphQL with persisted queries, creating and editing records, heavy reports; tenant isolation; noisy tenant; Monday-morning report spike | [SaaSLab](../../examples/packlab/README.md#saaslab), port 8091 |
| [`llm-apps`](../../packs/llm-apps) | SSE, HTTP | Streaming chat with time to first token and tokens per second; concurrency ramp; long prompts stalling short ones; long streams held open | [LLMLab](../../examples/packlab/README.md#llmlab), port 8092 |
| [`chat`](../../packs/chat) | WebSocket, HTTP | Conversations with fan-out to every room member, idle connections, history; a crowded room; 2,000 open connections; reconnect storm | [ChatLab](../../examples/packlab/README.md#chatlab), port 8093 |
| [`ticketing`](../../packs/ticketing) | HTTP, WebSocket | Seat maps, holds and orders; a WebSocket waiting room; on-sale rush; seat-lock contention with an oversell check | [TicketLab](../../examples/packlab/README.md#ticketlab), port 8094 |
| [`identity`](../../packs/identity) | HTTP | OAuth 2.0 / OpenID Connect password, refresh and client-credentials grants, introspection, revocation; login storm; refresh waves | [AuthLab](../../examples/packlab/README.md#authlab), port 8095 |
| [`public-apis`](../../packs/public-apis) | HTTP | API keys, cursor pagination, idempotent creates, webhook deliveries; rate-limit burst (429 with Retry-After); noisy neighbour; webhook burst | [APILab](../../examples/packlab/README.md#apilab), port 8096 |

Each pack's README lists its files and what each one checks. The PackLab
apps are small in-memory Go servers with planted bottlenecks, each with a
fix behind a flag, so a pack can be tried end to end in a minute:

```sh
go run ./examples/packlab -product ticketing        # TicketLab on :8094
stampede init --target http://localhost:8094        # proposes the ticketing pack, installs it, dry-runs it
stampede run stampede/ticketing/stresses/on-sale-rush.yaml -e TARGET_URL=http://localhost:8094 -o before.html
go run ./examples/packlab -product ticketing -fix all   # restart with the bottlenecks fixed, run again, compare
```

How the packs are tested: `examples/packlab/packs_test.go` starts every
PackLab app in-process, checks that `stampede init`'s probe picks the right
pack, runs `stampede pack test` on it, and runs every journey and stress
under load for a couple of seconds, failing on any failed request or
iteration. The CI `packlab` job repeats the detection and the dry run
against the `packlab` binary with the packs exactly as shipped, one job per
pack; the `packs` job does the same for e-commerce against ShopLab.

## Layout

```
packs/ecommerce/
├── pack.yaml          # name, title, status, protocols, detection hints, reference app
├── journeys/          # everyday journeys (full scenarios)
├── stresses/          # targeted stresses
├── targets.yaml       # suggested targets
├── data/              # feeder files, referenced as ../data/... from scenarios
└── README.md
```

## Detection

`stampede init` fetches the target's home page, response headers, an
OpenAPI document from common paths (`/openapi.yaml`, `/openapi.json`,
`/swagger.json`, `/v3/api-docs`, ...) and an OpenID Connect discovery
document (`/.well-known/openid-configuration`, whose endpoints count as
paths). Each pack's `detect` block scores matches:

```yaml
detect:
  paths: ["/api/products", "/api/cart", "/api/checkout"]   # 3 points each
  openapiTags: [products, cart, checkout]                   # 2 points each
  htmlMeta: ['og:type" content="product']                   # 3 points each
  headers: { X-Powered-By: shopify, X-RateLimit-Limit: "" } # 2 points each; "" only needs the header
```

The best-scoring shipped pack is proposed and confirmed before anything is
installed.

## Writing a pack

1. Copy a shipped pack and edit `pack.yaml`.
2. Write journeys as normal scenarios using `${env.TARGET_URL}`. Keep each
   file's journeys under two minutes for one user, think times included:
   that is the dry run's budget per file.
3. Add a reference app and make `stampede pack test` pass against it. For
   an in-memory app, add a package under `examples/packlab` and register
   it in `examples/packlab/main.go`; `packs_test.go` then tests it.
4. Add the directory to the embed list in `packs/embed.go`, set its status to
   `shipped` in `packs/catalog.yaml`, and add it to the CI `packlab` matrix
   (or a job of its own).
