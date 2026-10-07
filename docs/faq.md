# Frequently asked questions

## Do I need a server to run a test?

No. `stampede run scenario.yaml` runs a scenario with an in-process engine
and writes the report; nothing else has to be running. The server
(`stampede server`, or the [Compose stack](deploy/compose.md)) adds saved
scenarios and runs, schedules, distributed workers, roles, the audit log and
a read-only web UI for watching runs and reading reports. Everything you do
to the server is done with the CLI. See [installation](install.md).

## Can I create scenarios or start runs in the web UI?

No. The web UI is for analysis and reporting: runs, live views, reports and
their downloads, comparisons, scenarios, schedules, drift results and
settings, all read only. Where an action would be, it shows the CLI command
to copy, such as `stampede start --scenario <name> --target <name>`,
`stampede push`, `stampede schedules create` or `stampede secrets set`. The
exceptions are the safety controls: **Stop** and **Kill** on a running run
and **Kill all** in the header.

## Is there a released version?

Not yet: v1.0 has not been tagged. Until it is, the one-line installers
build the CLI from source with Go 1.27 or later instead of downloading a
release archive, and the Compose stack builds its images from the
repository.

## What does it cost, and what is the licence?

Stampede is open source under the Apache-2.0 licence and runs on your own
machines. There is no hosted service.

## Can I load test a site I do not own?

Only lightly. Private and loopback addresses need no setup. A public host
runs under low caps (50 iterations per second, 50 users, 10 minutes) until
you prove you own it with a DNS TXT record or a file at
`/.well-known/stampede-verify.txt` (`stampede target verify <url>`).
Requests can reach only the target, private hosts and hosts you allow
explicitly. A scenario file cannot turn these rules off. See
[safety](safety.md).

## How do I stop a run straight away?

`stampede kill <run>` (or `/kill` in the console, or the **Kill** button on
the run's page in the web UI) stops a run's load immediately;
`stampede kill --all` (or the **Kill all** switch in the web UI's header)
stops every active run in the organisation. A worker that loses the server
for 10 seconds stops its load by itself. See [safety](safety.md#stopping).

## Do I need an AI model?

No. Generating journeys with a language model (`stampede generate`) and
report narratives are optional and use your own key or a local Ollama
model. No model is called while load runs. See [AI journey
generation](ai.md).

## How do I start without writing a scenario?

`stampede init --target <url>` probes the target, proposes the matching
[product pack](guides/packs.md), installs its journeys and stresses and
dry-runs each journey once. `stampede generate` drafts a scenario from an
OpenAPI document, a HAR recording, an access log or a description. Or
replay an access log or HAR file at its recorded times
([test types](guides/test-types.md)).

## How accurate are the numbers?

Latency is measured from each request's scheduled send time, so a slow
target cannot hide queueing (coordinated omission). Percentiles come from
merged histograms, never averaged percentiles. CI checks the reported
p50, p95 and p99 against a calibrated server on every push. See [how
Stampede measures](concepts/measurement.md).

## What does the verdict `generator-limited` mean?

A worker generating the load was saturated (CPU, scheduling lag, garbage
collection or file descriptors) while a target failed, so the failure may
be the load generator's rather than your system's. Add workers or give them
more CPU, then run again. See [troubleshooting](troubleshooting.md).

## Which protocols can I test?

HTTP/1.1 and HTTP/2, GraphQL, WebSocket, server-sent events, gRPC, and
pages in headless Chrome, built in; MQTT, Kafka, Redis, SQL and UDP
through first-party [plugins](plugins.md). The [plugin SDK](plugins.md)
adds others. See [protocols](protocols.md).

## How do I use it in CI?

`stampede run` exits 0 when every target passes, 3 when a target fails and
1 on any other error, and writes JUnit XML and a Markdown summary. There is
a GitHub Action, and examples for GitLab CI and Jenkins. See
[CI integration](guides/ci.md).

## How do I tell whether a release got slower?

Run each version several times (`stampede run --repeat 3 --json ...`) and
compare them with `stampede compare`. A change counts only when its
confidence interval excludes zero and it is larger than the noise between
repeats. See [comparing releases](guides/comparing.md).

## How many users can one machine generate?

It depends on the scenario, the protocol and the target. Workers watch
their own CPU, scheduling lag and file descriptors and mark a run they
distorted as generator-limited, so an overloaded machine shows up in the
report rather than as slower numbers. Browser users are far heavier than
HTTP users: about 100–200 MB of memory each. For more load, add workers
([architecture](architecture.md#a-distributed-run)).

## Where are my secrets kept?

On a server, secrets are stored in the database encrypted with the master
key, which is kept outside the database. Back up the key with the database:
without it the secrets cannot be decrypted. See [upgrades, backups and
restore](deploy/upgrades.md). For `stampede run`, secrets come from
`STAMPEDE_SECRET_<NAME>` environment variables.

## Where is the REST API documented?

In the [REST API reference](reference/api.md), generated from
[api/openapi.yaml](../api/openapi.yaml), which the server also serves at
`/api/v1/openapi.yaml`.
