# Stampede web UI

The browser UI for a Stampede server, for analysis and reporting. Everything
you do to a server (set it up, create projects, targets, secrets and
scenarios, start runs, edit schedules, users, tokens and settings) is done
with the `stampede` CLI; the UI shows the results. It talks only to the
public REST API in [`../api/openapi.yaml`](../api/openapi.yaml).

What it shows:

- sign-in and sign-out, and who you are (Settings → Account);
- each project's overview: recent runs, scenarios and targets;
- runs: the list, the live view (charts, worker health, events), the full
  report (charts, errors with examples, workers, knee, recovery,
  breakpoint, the AI summary when one was written) and its downloads
  (HTML, JSON, CSV, JUnit, Markdown, PDF by printing);
- comparisons of runs;
- scenarios: the YAML, read only and syntax highlighted, the journey graph,
  the plan and the version history, and API coverage and drift checks;
- AI generation jobs: each journey's dry-run trace, the proposed scenario
  and its diff (jobs are started and approved with `stampede ai jobs`);
- the library of packs;
- targets with their verification status, workers, schedules and drift
  check results;
- settings, read only: API tokens, users, audit log, integrations,
  notification channels, AI providers (without keys), SSO and limits.

Where an action used to be, the UI shows the CLI command that does it, with
a copy button, for example `stampede start --scenario <name> --target <name>`.
The one exception is safety: **Stop** and **Kill** on a running run and the
**Kill all** switch in the header stay in the UI. When the server has not
been set up, the UI shows a page that says to run `stampede setup`.

![Live run](docs/screenshots/02-live-run.png)

More screenshots (light and dark) are in [`docs/screenshots`](docs/screenshots).

## Stack

React 18, TypeScript (strict), Vite, TanStack Router and Query, Tailwind CSS
with Radix UI primitives, uPlot (live charts), Apache ECharts (report charts),
React Flow (journey graph) and a small built-in YAML highlighter
(`src/features/scenarios/yamlTokens.ts`) for the read-only scenario view.
The API client is generated from the OpenAPI spec with `openapi-typescript`
and called through `openapi-fetch`.

Nothing is loaded from the network at runtime: fonts are system fonts and
every chart library is bundled, so the UI works air-gapped.

## Develop

Requires Node 22+ and pnpm 10.

```sh
pnpm install
pnpm dev          # http://localhost:5173, proxies /api to http://localhost:8080
pnpm dev:mock     # no server needed: MSW serves realistic fixture data
```

Set `STAMPEDE_API=http://host:port` to proxy to another server.

In mock mode you are signed in as the owner of "Acme Retail" with three
projects, a run in progress (streamed over SSE), run history with reports,
workers, users, tokens and an audit log. The Storefront project requires
a passing dry run before load (one of its runs was refused by it), has two
per-project role overrides, and has an hourly drift schedule whose last
check found a broken journey. URL switches:

| URL                      | Shows                                                           |
| ------------------------ | --------------------------------------------------------------- |
| `/?mock=setup`           | the page shown before `stampede setup` has run                  |
| `/login?mock=signed-out` | the sign-in screen (`priya@acme.dev` / `correct-horse-battery`) |
| `/?mock=viewer`          | the UI as a viewer (no Stop, Kill or Kill all)                  |

The mock fixtures are typed with the generated OpenAPI types, so they fail to
compile if they drift from the spec. The same handlers back the tests.

## Scripts

| Script           | Does                                                              |
| ---------------- | ----------------------------------------------------------------- |
| `pnpm generate`  | regenerate `src/api/schema.gen.ts` from `../api/openapi.yaml`     |
| `pnpm typecheck` | `tsc -b`                                                          |
| `pnpm lint`      | ESLint (typescript-eslint strict, react-hooks) and Prettier check |
| `pnpm format`    | Prettier write                                                    |
| `pnpm test`      | Vitest + Testing Library (jsdom)                                  |
| `pnpm test:e2e`  | Playwright end-to-end tests in Chromium (see below)               |
| `pnpm build`     | type-check and build to `dist/`                                   |

Run `pnpm generate` whenever the OpenAPI spec changes, and commit the result.

## The built UI is committed

`dist/` is committed (the repository root ignores `web/dist/`; `web/.gitignore`
re-includes it) and embedded by [`embed.go`](embed.go):

```go
import "github.com/Ivan825/Stampede/web"
// web.Dist is an embed.FS rooted at "dist"
```

Why commit build output: `go install github.com/Ivan825/Stampede/cmd/stampede@latest`
and plain `go build` cannot run pnpm, and the product is a single binary, so
the UI has to be in the module. To keep the repository from growing with every
UI change, vendor code is split into content-hashed chunks (`echarts`,
`flow`, `uplot`, `yaml`, `vendor`, plus Vite's preload helper in `runtime`)
that never import app code, so they only change when dependencies change; an
ordinary UI change rewrites a few small app chunks (tens of KB). The build is
reproducible: building the same sources twice gives byte-identical files.

**After changing anything under `src/`, run `pnpm build` and commit `dist/`
with the change.** A CI check that `dist/` is up to date is a good follow-up.

Build size (Vite 8, minified): about 1.5 MB on disk. The first page needs
about 460 KB (145 KB gzipped): the app entry, the React/Router/Query/Radix
vendor chunk and CSS. ECharts (570 KB) loads with run pages and React Flow
(176 KB) with scenario pages.

## End-to-end tests

`pnpm test:e2e` runs the Playwright tests in [`e2e/`](e2e) in Chromium
(`pnpm exec playwright install chromium` once). They build the UI in mock
mode and serve it with `vite preview`, so no server is needed: sign in,
browse runs, open a live run and a report, compare runs, the library,
scenarios, schedules, the settings pages, and the kill switch.
The mock API lives in the page, so each test loads one URL and then
navigates inside the app; a reload starts from fresh fixtures.

## Journey graph

The graph beside the YAML shows the scenario's journeys and their steps:
a start node fans out to each journey by weight, steps chain downwards,
branches fan out into one column per arm, and loop, while and group blocks
show their body with a back edge for repeats. It is read only; change a
scenario in its YAML file and save it as a new version with `stampede push`.

## Layout

```
src/
  api/        generated schema, typed client (CSRF header, ApiError), query hooks
  app/        shell, kill switch, route errors
  components/ UI primitives, chips, dialogs, toasts, theme
  features/   runs (live view, report, compare), scenarios (YAML view, graph)
  lib/        formatting (mirrors internal/report), roles, SSE hook, CLI commands
  mocks/      MSW handlers, in-memory store and simulated runs
  pages/      one file per route
  test/       test setup, MSW server, render helpers
```

## Conventions

- Latency comes from the API in seconds and is shown like the Go report
  (`ms()` in `src/lib/format.ts` mirrors `report.Ms`). Numbers use tabular
  monospace.
- Status and verdict colours are consistent everywhere: pass green, fail red,
  generator-limited amber.
- Read only: the UI changes nothing on the server except the safety
  controls (Stop, Kill, Kill all), which need the runner role, and signing
  in and out. Admin-only settings (audit log, integrations, notifications,
  AI providers, SSO, limits) are hidden from other roles. The server
  enforces the same rules.
- Where an action belongs to the CLI, the page shows the command in a small
  copyable hint (`src/components/cliHint.tsx`). Every command comes from
  one map, `src/lib/cli.ts`, so the hints match the CLI.
- Errors show the API's `message` and every `details` line.
