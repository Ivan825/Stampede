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
- **AI-generated scenarios** are rejected when they call known payment, SMS,
  email or CAPTCHA services (see [AI generation](ai.md)).

## Caps

Every run is checked before it starts against, in order: the server's hard
caps (`--max-rate`, `--max-vus`, `--max-duration`), the low caps for
unverified public targets, and the target's own caps. The request is refused
with the reason if any is exceeded.

## Stopping

- **Stop** ends a run gracefully: no new iterations, in-flight ones may
  finish within the graceful-stop period (30 s by default).
- **Kill** stops all load immediately. In tests the kill reaches every
  worker in under a millisecond on a local network.
- **Kill all** (the always-visible button in the web UI, `stampede kill --all`,
  or `POST /api/v1/runs/kill-all`) kills every active run in the organisation.
- **Dead man's switch:** a worker that cannot reach the server for 10
  seconds stops its load by itself.
- **Breakpoint runs** stop at the first load level that misses a target.
- **Auto-abort:** a scenario's `load.abort` (for example
  `{errors: 50%, p95: 5s, for: 10s}`) stops the run once errors or p95 stay
  at or above the limit for that long. The server also applies a floor to
  every run, by default errors at or above 90% for 30 seconds
  (`--abort-errors`, `--abort-for`; `0` disables it), so a target that has
  fallen over is not hammered for the rest of a test.

## Record

Every run start, stop and kill, target and secret change, token and user
change is in the audit log (Settings → Audit log, admins), with who did it.

**Planned:** connection-flood and slow-client tests, with typed
confirmation before they run.
