# Safety

Some load tests look exactly like an attack. These rules are built in and a
scenario file cannot turn them off.

## Who can be targeted

- **Private and loopback addresses** need no setup.
- **Public hosts** run under low caps (50 iterations/s, 50 users, 10
  minutes) until their ownership is verified, by a DNS TXT record
  `stampede-verify=<token>` on the host or on `_stampede.<host>`, or the
  token served at `/.well-known/stampede-verify.txt`.
- **Requests can only reach** the target host, private hosts and hosts you
  allow explicitly (`--allow-host`, or a target's allowed hosts). A scenario
  cannot send load to someone else's site; blocked requests appear in the
  report as `blocked by safety`. Plugin steps go through the same check for
  the address setting the plugin marks (a broker, a DSN); see
  [plugins](plugins.md#in-a-scenario).
- **Third-party services.** Scenarios must not send load to payment, SMS,
  email or CAPTCHA providers (the list in [AI generation](ai.md#third-party-guard)).
  AI-generated scenarios that call one are rejected; `stampede validate`
  warns about every step that does; and a server refuses to start a run
  whose scenario calls one (`403`, code `third_party`, one detail per
  step) unless that host is one of the target's allowed hosts. Hosts written
  as templates (`https://${env.PAY_HOST}/...`) cannot be checked before the
  run; the host policy above still applies to them.

## Caps

Every run is checked before it starts against, in order: the server's hard
caps (`--max-rate`, `--max-vus`, `--max-duration`), the organisation's caps,
the project's caps, the low caps for unverified public targets, and the
target's own caps. A run must fit within all of them; the request is refused
(`403`) with the level and the reason if any is exceeded.

Organisation caps are set by admins with `stampede caps set` (or
`PUT /api/v1/organisation/caps`); project caps with
`stampede projects settings set` (or
`PUT /api/v1/projects/{projectId}/settings`), by organisation admins or
members with the admin role in that project. Each takes `maxRate`
(iterations per second), `maxVUs` and `maxDurationSeconds`; an empty object
removes them. Changes are audited. The web UI shows them read only:
**Settings → Limits** lists the organisation's caps, every project's caps
and each target's effective caps (the tightest of all the levels that
apply to it), and **Project settings** in the project's navigation shows
the project's caps.

Bandwidth and open-connection caps are not implemented.

## Dry run before load

A project can require a passing dry run before any load
(`requireDryRun` in `PUT /api/v1/projects/{projectId}/settings`). Every
run in the project then starts by running each journey once with one user
against the target, the same dry run as `stampede validate --dry-run` and
AI generation, under the same host policy and third-party guard. The run
stays `starting` meanwhile. If a journey fails, the run ends `failed` with
the failing journeys and their first error, and no load is sent. The
outcome is recorded as run events (`dryrun.started`, one `dryrun.journey`
per journey, then `dryrun.passed` or `dryrun.failed`), listed by
`GET /api/v1/runs/{runId}/events`. Stopping or killing a run during its dry
run ends it `aborted` before any load. The dry run is bounded to 2 minutes.
Turn the gate on or off with `stampede projects settings set`. In the web
UI, **Project settings** shows whether it is on, and the run page shows the
dry run's result for each journey, with the problem of each one that
failed, and the run's events.

## Stopping

- **Stop** (`stampede stop <run>`, or the **Stop** button on a running
  run's page) ends a run gracefully: no new iterations, in-flight ones may
  finish within the graceful-stop period (30 s by default).
- **Kill** (`stampede kill <run>`, or the **Kill** button on a running
  run's page) stops all load immediately. In tests the kill reaches every
  worker in under a millisecond on a local network.
- **Kill all** (`stampede kill --all`, the **Kill all** switch in the web
  UI's header whenever a run is active, or `POST /api/v1/runs/kill-all`)
  kills every active run in the organisation.

These safety controls are the only actions in the web UI, which is
otherwise read only; they need the runner role.
- **Dead man's switch:** a worker that cannot reach the server for 10
  seconds stops its load by itself.
- **Breakpoint runs** stop at the first load level that misses a target.
- **Auto-abort:** a scenario's `load.abort` (for example
  `{errors: 50%, p95: 5s, for: 10s}`) stops the run once errors or p95 stay
  at or above the limit for that long. The server also applies a floor to
  every run, by default errors at or above 90% for 30 seconds
  (`--abort-errors`, `--abort-for`; `0` disables it), so a target that has
  fallen over is not hammered for the rest of a test.

## Roles

Members have one organisation role: viewer (read), runner (also start,
stop and kill runs), editor (also scenarios, targets, secrets and
schedules), admin (also users, integrations, caps and settings) or owner.
An admin can give a member a different role in one project with
`stampede projects roles set` or
`PUT /api/v1/projects/{projectId}/roles/{userId}` (`{"role": "editor"}`),
higher or lower than their organisation role, and remove it with `DELETE`;
`GET /api/v1/projects/{projectId}/roles` lists the overrides. Every check
on a project's targets, scenarios, runs, schedules, AI jobs and drift
results uses the role in that project; `GET /projects` reports it as
`role`. Owners keep the owner role everywhere and nobody can be made owner
of one project. An API token never grants more than the role it was
created with. Organisation admins can always manage a project's roles and
settings, even when an override lowers them there; deleting a project and
the organisation-wide kill switch need the organisation role. In the web
UI, **Project settings** lists every member with their organisation role
and any override, read only.

## Record

Every run start, stop and kill, target and secret change, token and user
change is in the audit log (Settings → Audit log, admins), with who did it.

**Planned:** connection-flood and slow-client tests, with typed
confirmation before they run.
