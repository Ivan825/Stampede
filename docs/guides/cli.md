# Using Stampede from the CLI

Everything you can set up and run on a Stampede server can be done from the
terminal with the `stampede` binary: accounts, projects, targets, secrets,
scenarios, runs, reports, schedules, drift checks, AI generation,
notifications and the audit log. The web UI is there to read live runs and
reports; this guide walks a new user from an empty server to scheduled,
monitored load tests without opening it.

Every command that lists or shows something prints a table or a short
summary, and the same data as JSON with `--json`, so scripts can use
`jq`. The [CLI reference](../reference/cli/stampede.md) lists every flag.

## The console

Run `stampede` on its own to work in one place instead of typing separate
commands. It opens with the Stampede mark, who you are signed in as and on
which server, and what to try first, then waits for input until you leave:

- **Slash commands.** Every command in this guide works as `/<command>`:
  `/projects list`, `/targets create shop --base-url https://shop.example.com`,
  `/compare --a before.json --b after.json`. The console also has its own
  `/run` (with a live throughput and p95 panel), `/init`, `/runs`,
  `/workers`, `/stop`, `/kill` and `/use <project>`; `/stampede <args>` runs
  any command exactly as typed. `/help` lists them.
- **Plain language.** "spike test checkout at 300 rps for 5 minutes" is
  turned into a command and shown for confirmation before it runs.
- **Prompts and secrets.** `/setup`, `/login`, `/password`, `/users`,
  `/secrets`, `/tokens`, `/ai` and `/keygen` take over the terminal while
  they ask for a password or show something secret, then wait for Enter and
  return to the console. Their output is not kept.
- **Sessions.** Each session is saved as you go: what you typed, the output
  and the project you were in. `stampede --continue` (`-c`) reopens the last
  one, `stampede --resume <id>` a particular one, and inside the console
  `/sessions` lists them and `/resume [id]` switches. ↑ and ↓ recall what you
  typed, across sessions. Sessions live in your user config directory
  (`~/.config/stampede/console` on Linux), readable only by you, with the
  values of flags such as `--token` and `--password` hidden; the last 50 are
  kept.
- **Leaving.** `/exit`, or type `exit` or `quit`, or press Ctrl-C twice.
  Ctrl-C once stops the command that is running or stops following a run
  (the run itself continues until `/stop` or `/kill`).

Servers, workers and agents (`stampede server`, `worker`, `agent`) run until
stopped, so start them in a terminal of their own.

## 1. Start a server and create the first account

Start a server ([Docker Compose](../deploy/compose.md) or the other
[installs](../install.md)), then create the organisation and its owner:

```sh
stampede setup --server http://localhost:8080 \
  --organisation Acme --name "Ada Lovelace" --email ada@acme.test
```

The password (at least ten characters) is asked without echo, or read from
`STAMPEDE_PASSWORD`; it is never a flag, so it stays out of your shell
history. `setup` only works while the server has no accounts. It signs you
in and stores an API token for the CLI in `~/.config/stampede/config.yaml`
(the OS's user config directory), readable only by you.

```sh
stampede whoami            # ada@acme.test (Ada Lovelace), owner of Acme on http://localhost:8080
stampede version --server  # this binary's version and the server's
```

On another machine, sign in with `stampede login --server URL --email
you@example.com`. `stampede password` changes your password; `stampede
logout` revokes the CLI's token on the server and removes it locally.

## 2. Invite your team

```sh
STAMPEDE_NEW_PASSWORD='first password for pat' \
  stampede users create pat@acme.test --name "Pat Lee" --role editor
stampede users list
stampede users update pat@acme.test --role admin
stampede users delete pat@acme.test
```

Roles are `owner`, `admin`, `editor`, `runner` and `viewer`. The new
member's first password comes from `STAMPEDE_NEW_PASSWORD` or a prompt;
they change it with `stampede password`.

For CI and scripts, create an API token. Tokens cannot create tokens, so
this asks for your password; the secret is printed once on stdout and
never again:

```sh
stampede tokens create github-actions --role runner --expires-in-days 90
stampede tokens list          # names, prefixes and last use, never secrets
stampede tokens delete github-actions
```

In CI set `STAMPEDE_SERVER` and `STAMPEDE_TOKEN` instead of signing in.

## 3. Create a project and a target

```sh
stampede projects create Shop --description "Storefront API" --use
```

`--use` makes Shop the default project, so later commands need no
`--project` (`stampede projects use <name>` changes it, `STAMPEDE_PROJECT`
overrides it for one shell). With a single project, it is used without
either.

A target is the system you load: a base URL, other hosts its requests may
reach, and optional caps.

```sh
stampede targets create staging --base-url https://staging.example.com \
  --allow-host cdn.example.com --max-rate 500
stampede targets list
```

Private and loopback addresses need no verification. A public target runs
under low caps until you prove you own it: `stampede targets show staging`
prints a token to publish as a DNS TXT record or at
`/.well-known/stampede-verify.txt`, then

```sh
stampede targets verify staging
```

checks it. (`stampede target verify <url>` still works too; for a URL that
is not a target on the server it checks this machine's token, which
`stampede run` uses.) `stampede targets update` changes the URL, allowed
hosts or caps; `stampede targets delete` removes a target.

Values such as passwords and API keys that scenarios use as
`${secret.NAME}` are project secrets. They are encrypted on the server
and never printed:

```sh
stampede secrets set SHOP_PASSWORD                   # asks for the value
stampede secrets set API_KEY --from-env STAGING_KEY  # or from a variable
vault read -field=key secret/shop | stampede secrets set API_KEY   # or stdin
stampede secrets list
```

## 4. Save a scenario

Write a scenario file (see the [scenario format](../reference/scenario.md),
`stampede init` or `stampede generate`), check it the way the server will,
and save it:

```sh
stampede scenarios validate checkout.yaml
stampede push checkout.yaml -m "first version"
```

Every push of a file with the same `metadata.name` adds a version.

```sh
stampede scenarios list
stampede scenarios show checkout                    # summary of the latest version
stampede scenarios versions checkout
stampede scenarios show checkout --version 1 > checkout-v1.yaml
```

## 5. Run it

```sh
stampede start --scenario checkout --target staging --duration 5m
```

`start` follows the run live, prints its report when it ends and exits
like `stampede run`: 0 when every target passed, 3 when one failed.
`--detach` prints the run id and returns; `--file checkout.yaml` pushes a
local file first. Load overrides (`--shape`, `--rate`, `--vus`,
`--duration`), `--region` splits and `-e KEY=VALUE` work as for local runs.

Runs are named by id or a unique start of it:

```sh
stampede runs                      # recent runs of the project
stampede runs follow 3f2a91c0      # attach to a running run
stampede runs show 3f2a91c0        # status, load, summary, who started it
stampede runs events 3f2a91c0      # workers joining, safety stops, dry runs
stampede runs timeline 3f2a91c0 --resolution 10s
stampede runs workers 3f2a91c0     # live CPU, scheduling lag, saturation
stampede stop 3f2a91c0             # graceful
stampede kill 3f2a91c0             # immediate
stampede kill --all                # every active run in the organisation
stampede workers                   # connected load generators
```

## 6. Read and compare reports

```sh
stampede report 3f2a91c0                  # summary in the terminal
stampede report 3f2a91c0 -o report.html   # also --pdf, --md, --junit, --csv, --json
stampede compare --a 3f2a91c0,51c0aa2e,9e77d1f0 --b 7be01d44,0c3e5b12,c41f9a7d
```

See [reading a report](reports.md) and [comparing releases](comparing.md).
With an AI provider on the server (step 9), `stampede narrative 3f2a91c0`
adds a written summary to the report in which every claim cites its
figures.

## 7. Schedule it

```sh
stampede schedules preview --cron "0 2 * * MON-FRI" --timezone Europe/London
stampede schedules create nightly --scenario checkout --target staging \
  --cron "0 2 * * MON-FRI" --timezone Europe/London --duration 10m
stampede schedules list
stampede schedules show nightly
stampede schedules update nightly --cron "0 3 * * *" --shape soak -e STAGE=nightly
stampede schedules run nightly --follow     # fire it now
stampede schedules disable nightly
```

`update` changes only the flags you give; overrides and environment
variables are merged into the current ones (`--clear-overrides`,
`--unset-env` and `--clear-env` remove them). See [scheduled
runs](schedules.md).

## 8. Catch API drift

A drift schedule dry-runs each journey once (no load) and, with a spec URL,
compares the scenario with the API on every check:

```sh
stampede schedules create api-drift --kind drift --scenario checkout \
  --target staging --cron "0 6 * * *" --spec-url https://staging.example.com/openapi.json
stampede drift results              # every check, newest first
stampede drift show 7c1e0a2b        # each journey's dry run and the spec diff
stampede drift repair 7c1e0a2b      # an AI job that proposes a fix (step 9)
```

The same checks run on demand against a saved scenario, on the server:

```sh
stampede coverage --scenario checkout --spec-url https://staging.example.com/openapi.json
stampede drift --scenario checkout --from-openapi openapi.yaml \
  --previous-openapi openapi-v1.yaml --target staging
```

`stampede coverage <file>` and `stampede drift <file>` keep working on
local files; see [coverage and drift](coverage.md).

## 9. Generate journeys with AI

AI generation is optional and uses your own key. An admin adds a provider;
the key is read from a variable, stdin or a hidden prompt, stored
encrypted and never shown:

```sh
stampede ai providers set default --kind anthropic --api-key-env ANTHROPIC_API_KEY
stampede ai providers list
```

Then start a job. It drafts a scenario, dry-runs every journey against the
target and repairs failures; nothing is saved until you approve it.

```sh
stampede ai jobs create --describe "people browse items, add one to the cart and check out" \
  --from-openapi openapi.yaml --target staging --wait
stampede ai jobs show 5d1e9c2a --yaml      # the proposal (--diff against --scenario)
stampede ai jobs approve 5d1e9c2a -m "generated checkout"
```

`--from-har`, `--from-log` and `--proto` add recorded traffic and gRPC
services; `--scenario checkout` compares the proposal with an existing
scenario so approving saves a new version. See [AI journey
generation](../ai.md).

## 10. Notifications and integrations

Admins add channels that hear when runs finish, miss a target or are
killed, and when drift checks find broken journeys:

```sh
stampede notify channels create team --kind slack --url-env SLACK_WEBHOOK \
  --event run.target_failed --event run.killed --event drift.detected
stampede notify channels test team
stampede notify deliveries team
```

A generic `--kind webhook` channel signs each body; its secret is printed
once when the channel is created. Prometheus, trace-link and agent
integrations that scenarios refer to by name:

```sh
stampede integrations create prom --kind prometheus --url http://prometheus:9090
stampede integrations create jaeger --kind traces --url "https://jaeger.example.com/trace/{traceId}"
stampede integrations list
```

See [integrations](integrations.md).

## 11. Limits, roles and the audit log

```sh
stampede caps set --max-rate 2000 --max-duration 2h          # organisation caps
stampede projects settings set --max-vus 500 --require-dry-run
stampede projects roles set pat@acme.test viewer               # one project only
stampede settings limits      # every cap a run meets, and each target's effective caps
stampede settings sso         # the single sign-on configuration
stampede audit --since 24h    # who did what; --action run. --json
```

## Scripting

- `--json` prints the API's response; human output goes to stdout and
  progress to stderr.
- Exit codes: 0 success, 1 error, 3 a run missed a target, 4 a comparison
  found a regression or a drift check found drift.
- `STAMPEDE_SERVER`, `STAMPEDE_TOKEN` and `STAMPEDE_PROJECT` override the
  config file; `STAMPEDE_PASSWORD` and `STAMPEDE_NEW_PASSWORD` answer
  password prompts.

```sh
run=$(stampede start --scenario checkout --target staging --detach)
stampede runs follow "$run" --md summary.md
stampede runs show "$run" --json | jq '.summary.p95'
```

## Where each part of the web UI lives

| Web UI | Command |
|---|---|
| First-run setup, sign-in, password | `stampede setup`, `login`, `password`, `logout`, `whoami` |
| Users, API tokens | `stampede users`, `stampede tokens` |
| Projects, project settings and roles, organisation caps | `stampede projects`, `projects settings`, `projects roles`, `stampede caps` |
| Targets and verification, secrets | `stampede targets`, `stampede secrets` |
| Scenario editor and versions | `stampede push`, `stampede scenarios` |
| Runs, live view, kill switch | `stampede start`, `runs`, `runs follow`, `stop`, `kill` |
| Reports and comparisons | `stampede report`, `stampede compare`, `stampede narrative` |
| Schedules and drift | `stampede schedules`, `stampede drift results/show/repair` |
| Coverage and drift of a saved scenario | `stampede coverage --scenario`, `stampede drift --scenario` |
| AI providers and generation | `stampede ai providers`, `stampede ai jobs` |
| Integrations and notifications | `stampede integrations`, `stampede notify` |
| Audit log, SSO and limits settings | `stampede audit`, `stampede settings` |
| Workers | `stampede workers`, `stampede runs workers` |
| Pack library | `stampede pack list` and `stampede init` (local) |
