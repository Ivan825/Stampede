# AI journey generation

Stampede can draft a scenario for you with a language model, then prove
each journey works before you trust it. The feature is **optional** and
**bring-your-own-key**: nothing in Stampede needs a model, and no model is
ever called while load runs. Generation happens once, before load, and a
person approves the result.

```sh
export ANTHROPIC_API_KEY=sk-ant-...
stampede generate \
  --from-openapi examples/shoplab/openapi.yaml \
  --describe "Most shoppers browse and search. Some log in to see their orders. A few buy." \
  --target http://localhost:8090 \
  -o shop.yaml
stampede run shop.yaml
```

## What it does

The pipeline has six stages.

1. **Understand.** Stampede reads your inputs and builds a dependency map,
   showing which call produces a token, id or cookie that another call
   needs (for example `POST /api/login → $.token → GET /api/me`).
   - *Description*: plain language about your users and what they do.
   - *OpenAPI 3.x spec* (YAML or JSON): endpoints, parameters, examples,
     security requirements and response schemas.
   - *GraphQL schema*: SDL, an introspection result, or fetched from the
     target with `--introspect`. Queries and mutations with their
     arguments and return fields go to the model, which writes `graphql:`
     steps; a mutation returning a token (such as `login`) feeds signed-in
     operations, and objects with ids feed operations taking those ids.
   - *HAR file*: a recording from browser devtools or a proxy. Static
     assets and third-party calls are ignored. Dependencies are found by
     seeing which response values later requests reuse.
   - *Browser crawl* (`--crawl URL`): headless Chrome visits the site
     like one visitor, following links on the same host breadth first (at
     most `--crawl-pages`, `--crawl-depth` clicks deep). It records every
     document and API call the pages make (XHR and fetch, with responses
     up to 4 KiB) as a HAR recording, which is then treated exactly like a
     HAR file, and lists the forms it saw for the model. It never submits
     a form, skips links that look destructive (logout, delete, remove,
     unsubscribe), and its requests stay on the target host and allowed
     hosts.
   - *Access log* (common/combined format, or any line with a quoted
     `"METHOD /path"`): each endpoint's share of traffic and the most
     common visits per client, used to set journey weights.
2. **Draft.** The model writes a complete scenario, constrained to the
   [scenario JSON Schema](../schema/scenario.schema.json): weighted
   journeys, think times, branches, and edge journeys such as abandoned
   carts, failed logins, invalid input and retries.
3. **Static check.** The draft must parse and compile, including variable
   flow (every `${var}` is extracted before it is used). Every request
   must use an endpoint that exists in the OpenAPI spec or HAR recording,
   and no request may reach a blocked third-party host (see below).
4. **Dry run.** Each journey runs once with one user against the target.
   A journey with branches runs once per alternative (up to four), so
   every branch is exercised. Every request, response (truncated to 2 KiB,
   redacted), extracted value and check result is recorded. Think times
   are not slept and loops run at most three times.
5. **Repair.** Static problems and dry-run failures go back to the model
   with the redacted evidence, up to three rounds (`--max-repairs`).
   Journeys that still fail are **flagged for a human**.
6. **Approve.** The output is a proposal: the scenario, per-journey
   dry-run traces and a diff against an existing scenario. Nothing is saved
   until a person approves it. The CLI writes the file. The server saves a
   new scenario, or a new version, only through an explicit approve call.

The dry run sends real requests: a checkout journey places a real order on
the target. Point `--target` at a test environment.

## Providers

| `--provider` | Key (CLI environment variable) | Default model | Output constraint |
|---|---|---|---|
| `anthropic` | `ANTHROPIC_API_KEY` | `claude-sonnet-5-5` | The scenario schema is offered as a tool input schema |
| `openai` | `OPENAI_API_KEY` | set `--model` | JSON mode, schema in the prompt |
| `gemini` | `GEMINI_API_KEY` | set `--model` | JSON response type, schema in the prompt |
| `ollama` | none (local) | set `--model` | JSON format mode, schema in the prompt |
| `openai-compatible` | `OPENAI_API_KEY` if set | set `--model` | JSON mode (falls back to plain prompting if the server rejects it) |

Use `--base-url` for a non-default endpoint: a remote Ollama
(`http://gpu-box:11434`) or any OpenAI-compatible server such as vLLM,
LM Studio, llama.cpp or LiteLLM (`http://localhost:8000/v1`). Every reply
is validated and repaired whatever the provider, so a weaker model costs
more repair rounds but cannot produce an invalid file. For Anthropic's
first-party API, Stampede enables server-side refusal fallbacks
(`fallbacks: "default"`) and drops them automatically if an endpoint
rejects them.

Providers use plain HTTPS with no SDKs. Rate limits and server errors
are retried twice with backoff.

## Privacy and redaction

Before anything is sent to a provider:

- **Recorded traffic** (HAR files, access logs and dry-run traces) loses:
  - `Authorization`, `Proxy-Authorization`, `Cookie`, `Set-Cookie` and
    API-key headers
  - any header, JSON field, form field or query parameter whose name
    suggests a secret (password, token, secret, api key, session, card
    number, CVV and similar)
  - JWTs, bearer and basic credentials, and well-known key formats
    (Stripe, OpenAI/Anthropic, AWS, GitHub, Slack, Google, Stampede tokens)
  - long hex or base64 strings
  - email addresses, phone numbers and card numbers (Luhn-checked)
  - client IP addresses from access logs, which never leave the process
- **Documents you wrote on purpose** (the description and the OpenAPI
  spec) lose credentials only. Emails stay, because specs often name the
  test accounts the model needs (ShopLab documents `user0001@shoplab.test`).
- **Secrets** (`STAMPEDE_SECRET_*` in the CLI, project secrets on the
  server) and values extracted from responses into variables with
  secret-like names are redacted by exact value wherever they appear.

Bodies are truncated to 2 KiB in traces, and at most 600 bytes of each
HAR body are shown to the model. The scenario the model wrote is sent back
during repair as written. On the server, job rows store only the kinds and
sizes of the inputs, never their contents. Traces are stored already
redacted.

## Third-party guard

A load test must never charge cards, send SMS or email, or hammer a
CAPTCHA provider, even if the app calls them in production. Generated
scenarios that call these hosts are rejected (a fatal static problem), and
the dry run refuses to send to them. The guard matches the host and all
of its subdomains. The curated list lives in
[`internal/ai/guard.go`](../internal/ai/guard.go):

- **Payment:** Stripe (`api.stripe.com`, `checkout.stripe.com`,
  `js.stripe.com`), PayPal live, Braintree live, Square live, Adyen live,
  Checkout.com, Razorpay, Mollie, Authorize.Net live, Klarna live
- **SMS and voice:** Twilio, Vonage/Nexmo, MessageBird, Plivo, Sinch,
  Telnyx, Textbelt
- **Email:** SendGrid, Mailgun, Postmark, Resend, SparkPost
- **CAPTCHA:** reCAPTCHA (`www.google.com`, `recaptcha.net`), hCaptcha,
  Cloudflare Turnstile, Arkose Labs/FunCaptcha, 2Captcha

**Test-mode hosts are not blocked:** the PayPal, Braintree, Square,
Adyen, Checkout.com, Authorize.Net and Klarna sandboxes. Like any host
other than the target, they still have to be listed with `--allow-host`
(or a target's `allowHosts` on the server) before a request may reach them.

Separately from the guard, a request may only reach the target, private
addresses or explicitly allowed hosts. This is the same host policy that
`stampede run` uses.

## Cost control

Every job records the input and output tokens it used. On the server,
each provider has a `monthlyTokenCap` (default 2,000,000). A new job is
refused with `429 ai_token_cap` once the organisation's total AI token
use for the calendar month (UTC) reaches the chosen provider's cap. A
running job also stops calling the model when it would go over the cap,
and keeps the best proposal so far. The CLI prints the tokens used after
every run.

A typical job uses one draft call plus one call per repair round. Each
call sends the system prompt and schema (about 6,000 tokens) plus the
input digest. An OpenAPI spec is condensed to at most 60 KB of text;
ShopLab's 860-line spec becomes about 13 KB.

## CLI

```
stampede generate
  --from-openapi spec.yaml      OpenAPI 3.x spec
  --from-graphql schema.graphql GraphQL schema (SDL or introspection JSON)
  --introspect                  fetch the GraphQL schema from --target
  --graphql-path /graphql       where the GraphQL API is served
  --from-har session.har        HAR recording
  --from-log access.log         access log (journey mix)
  --proto file.proto            .proto file whose services become grpc steps (repeatable)
  --proto-import-path DIR       where imports of the --proto files are found (repeatable)
  --crawl URL                   crawl the site in headless Chrome instead of a HAR (default --target)
  --crawl-pages 30              most pages to visit
  --crawl-depth 3               most clicks away from the crawl URL
  --describe "..."              plain-language description
  --target URL                  system to dry-run against (required unless --no-dry-run)
  --provider NAME               anthropic (default), openai, gemini, ollama, openai-compatible
  --model NAME                  model (default claude-sonnet-5-5 for anthropic)
  --base-url URL                provider endpoint (required for openai-compatible)
  -o, --out FILE                where to write the scenario (required)
  --no-dry-run                  static check only
  --max-repairs N               repair rounds, default 3 (0 disables repair)
  --allow-unvalidated           write even if journeys failed; they are marked "FLAGGED FOR REVIEW"
  --allow-host HOST             extra public hosts the dry run may reach
  -e, --env KEY=VALUE           ${env.KEY} for the dry run (TARGET_URL is set from --target)
  --diff-against FILE           show a diff against this file (default: --out if it exists)
  --traces FILE                 write per-journey dry-run traces as JSON
  --max-tokens N                maximum tokens per reply (default 16000)
```

You need at least one input. The command prints each stage,
a per-journey dry-run summary (with the failing step's trace for flagged
journeys) and the tokens used. It writes the scenario only when every
journey passed, or with `--allow-unvalidated`. Otherwise it exits with
code 4 and writes nothing. With `--no-dry-run`, passing the static check
is enough. The written file sets `target.baseURL` to `--target`, and its
header comment records the provider, the model and the dry-run result.

### gRPC from .proto files

With `--proto`, the generator compiles the files and lists each service's
unary and server streaming methods, with an example request message, for
the model, which writes `grpc:` steps for them. Client and bidirectional
streaming methods are listed as not callable. The static check flags grpc
steps that call a method the files do not define. The dry run calls each
grpc step against `--target` (`http://` for plaintext gRPC, `https://` for
TLS) using the compiled descriptors, so the target needs no reflection
service, and checks and extracts from the response as JSON as a run does.
The written steps name the files in `proto:` (and `importPaths:` with
`--proto-import-path`), so runs load the same descriptors.

```sh
stampede generate --proto protos/orders.proto --proto-import-path protos \
  --describe "clients place an order and poll its status" \
  --target http://localhost:9090 -o orders.yaml
```

## On a server

Everything that changes something is done with the CLI; the web UI shows the
results.

- An admin adds a provider (kind, model, base URL, API key and monthly token
  cap) with `stampede ai providers set`. The key is never shown again;
  leave it out when replacing a provider to keep the stored one. The web
  UI lists the providers under **Settings → AI providers**, with their
  token use but never their keys.
- Editors start a job with `stampede ai jobs create`, giving a description
  and any of an OpenAPI spec, a HAR recording or an access log. The
  server's limits apply (20,000 characters of description, 5 MiB of
  OpenAPI, 20 MiB each of HAR and access log). A dry run needs a target.
  A job can also diff against an existing scenario, pick the provider when
  there are several, and set the number of repair rounds.
- Editors approve the proposal as a new scenario or as a new version of a
  scenario with `stampede ai jobs approve`. A job that needs review must be
  approved explicitly as unvalidated, because its flagged journeys did not
  pass the dry run.
- The web UI's **AI jobs** page in a project lists the jobs; opening one
  shows its progress, each journey's dry-run trace, the proposed scenario
  and its diff against the existing one, and the `stampede ai jobs approve`
  command for that job. Approving is not done in the UI.

## Server API

All endpoints are under `/api/v1` with the `ai` tag. See
[`api/openapi.yaml`](../api/openapi.yaml) for the full schemas.

| Method and path | Role | What it does |
|---|---|---|
| `GET /ai/providers` | viewer | Lists providers with `hasKey`, `monthlyTokenCap` and `usedTokensThisMonth`. Keys are never returned. |
| `POST /ai/providers` | admin | Creates (201) or replaces (200) a provider by `name` (default `"default"`): `{kind, model?, baseURL?, apiKey?, monthlyTokenCap?}`. The key is sealed with the server's master key, using the same envelope encryption as project secrets. Omit `apiKey` to keep the stored one. |
| `DELETE /ai/providers/{providerId}` | admin | Deletes a provider. Past jobs stay readable. |
| `POST /projects/{projectId}/ai/jobs` | editor | Starts a job (202): `{description?, openapi?, har?, accessLog?, proto?, targetId?, scenarioId?, providerId?, dryRun = true, maxRepairs = 3}`. `proto` maps file names to `.proto` sources (5 MiB in all); the dry run uses them, and the proposed grpc steps leave `proto:` out, so runs rely on the target's reflection service. `targetId` is required for the dry run. `scenarioId` is the scenario to diff against. |
| `POST /drift-results/{driftId}/repair` | editor | Starts a job (202) that repairs the journeys a [scheduled drift check](guides/schedules.md#drift-checks) found broken, with the scenario as the starting point. Approve it like any job. |
| `GET /projects/{projectId}/ai/jobs` | viewer | Job summaries, newest first. |
| `GET /ai/jobs/{jobId}` | viewer | `status` (`queued`, `running`, `succeeded`, `needs_review`, `failed`), `stage`, `round`, `usage`, `yaml`, `journeys` (status, attempts, problems and redacted `traces`), `problems`, `diff` and `error`. |
| `POST /ai/jobs/{jobId}/approve` | editor | `{scenarioId?, message?, allowUnvalidated?}` saves the proposal as a new version of `scenarioId` (default: the job's diff scenario), or otherwise as a new scenario. Jobs in `needs_review` need `allowUnvalidated: true`. A job can be approved once. Approval is audited. |

Jobs run in a bounded worker pool (2 at a time, up to 20 queued). Each job
has a 30-minute limit. Jobs live in the server process: if the server
restarts, unfinished jobs are marked `failed` with an "interrupted" error.
On the server, the dry run uses the target's base URL, its `allowHosts`
and the project's secrets, and reads file feeders only from `--data-dir`.
Proposals leave `target.baseURL` out, because the run's target supplies it.

Provider changes, job creation and approvals are written to the audit
log, without keys or input contents. Only admins can set a provider's
`baseURL`, because the server sends prompts (redacted inputs) to that
address.

## Report narratives

After a run finishes, a model can write the summary at the top of the
report. Every claim in it cites the report figures it rests on and is
labelled **measured** (the figures state it) or **suspected** (a likely
cause the figures suggest, such as rising connect time pointing at
connection pool exhaustion).

```sh
stampede run checkout.yaml --narrative -o report.html
stampede report saved.json --narrative --json with-summary.json -o report.html
stampede run checkout.yaml --narrative --provider ollama --model qwen3:14b
```

| Flag | Meaning |
|---|---|
| `--narrative` | write a summary after the run |
| `--provider` | `anthropic` (default), `openai`, `gemini`, `ollama`, `openai-compatible` |
| `--model` | model name (default `claude-sonnet-5-5` for anthropic) |
| `--ai-base-url` | provider endpoint, as `--base-url` for `generate` |

How claims are checked:

- The model sees only the report's aggregate figures as a list of facts
  with ids (`overall.latency`, `target.0`, `breakpoint`, `knee`,
  `step.checkout/pay`, `error.0`, ...). No request or response data is sent.
  Error messages and notes are redacted first.
- A claim citing an id that does not exist is dropped.
- A measured claim must cite at least one fact, and every number in it
  must appear in the facts it cites (compared by value, so 840ms matches
  840.0ms). Otherwise it is dropped.
- Every number in the summary must appear in some fact.
- If anything was dropped, the model gets one chance to correct its reply.
  What survives is kept, and nothing unchecked is ever shown.

The narrative runs after the load has finished, so it never affects the
measurement. If the model fails, the run's report and exit code are
unchanged and a warning is printed. The HTML report shows the summary
with each citation's figure on hover. The text and Markdown reports
list the claims with their fact ids, and the JSON report stores it under
`narrative`.

On a server, `stampede narrative <run>` writes the narrative of a finished
run with the organisation's AI provider (`POST
/api/v1/runs/{runId}/narrative`); it is saved into the report, and the run's
page in the web UI shows it as the AI summary.

## Limitations

- A crawl sees only what the pages load by following links. Journeys
  behind a login or a form need a description or a HAR recording of that
  part, and an app that builds its links in JavaScript without `<a href>`
  elements is only partly crawled.
- A narrative's checks prove that its figures come from the report, not
  that its suspected causes are right. Suspected claims are leads to
  investigate.
- A dry run proves one user can complete each journey. It does not prove
  the journey is realistic, or that the weights match production. Review
  the proposal.
- Dependency detection is heuristic: field names such as `token` or
  `productId`, matching resource names in OpenAPI, and value reuse in
  HAR recordings.
- The dry run supports HTTP steps. Other step kinds are recorded as not
  executed.
