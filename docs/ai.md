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
   - *HAR file*: a recording from browser devtools or a proxy. Static
     assets and third-party calls are ignored. Dependencies are found by
     seeing which response values later requests reuse.
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
  --from-har session.har        HAR recording
  --from-log access.log         access log (journey mix)
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

You need at least one of the four inputs. The command prints each stage,
a per-journey dry-run summary (with the failing step's trace for flagged
journeys) and the tokens used. It writes the scenario only when every
journey passed, or with `--allow-unvalidated`. Otherwise it exits with
code 4 and writes nothing. With `--no-dry-run`, passing the static check
is enough. The written file sets `target.baseURL` to `--target`, and its
header comment records the provider, the model and the dry-run result.

## Server API

All endpoints are under `/api/v1` with the `ai` tag. See
[`api/openapi.yaml`](../api/openapi.yaml) for the full schemas.

| Method and path | Role | What it does |
|---|---|---|
| `GET /ai/providers` | viewer | Lists providers with `hasKey`, `monthlyTokenCap` and `usedTokensThisMonth`. Keys are never returned. |
| `POST /ai/providers` | admin | Creates (201) or replaces (200) a provider by `name` (default `"default"`): `{kind, model?, baseURL?, apiKey?, monthlyTokenCap?}`. The key is sealed with the server's master key, using the same envelope encryption as project secrets. Omit `apiKey` to keep the stored one. |
| `DELETE /ai/providers/{providerId}` | admin | Deletes a provider. Past jobs stay readable. |
| `POST /projects/{projectId}/ai/jobs` | editor | Starts a job (202): `{description?, openapi?, har?, accessLog?, targetId?, scenarioId?, providerId?, dryRun = true, maxRepairs = 3}`. `targetId` is required for the dry run. `scenarioId` is the scenario to diff against. |
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

## Limitations

- GraphQL introspection and browser crawling are **planned**, not built.
- A dry run proves one user can complete each journey. It does not prove
  the journey is realistic, or that the weights match production. Review
  the proposal.
- Dependency detection is heuristic: field names such as `token` or
  `productId`, matching resource names in OpenAPI, and value reuse in
  HAR recordings.
- The dry run supports HTTP steps. Other step kinds are recorded as not
  executed.
