# Threat model

Stampede generates traffic on purpose, so the main risks are being used
against someone else, leaking what it holds, and being made to misbehave by
the files it runs. This page lists what is defended today and what is not.

## Assets

- The ability to generate large amounts of traffic.
- Secrets: target credentials and AI provider keys (encrypted at rest),
  session cookies and API tokens (stored only as SHA-256 hashes), the master
  key and the workers' join token.
- Run data: reports, recorded errors and, for AI jobs, redacted traces.

## Actors

| Actor | Can |
|---|---|
| Anonymous network user | reach the HTTP port and, if exposed, the worker port |
| Viewer / runner / editor / admin / owner | what their role allows ([roles](../internal/auth/roles.go)) |
| A scenario author | write YAML that the engine will run |
| A worker | anything the join token allows |
| An AI provider | sees redacted prompts |

## Threats and defences

| Threat | Defence | Status |
|---|---|---|
| Load sent to a site the user does not own | private-only by default for unverified public hosts (low caps), ownership verification by DNS TXT or well-known file, per-request host policy (target host, private hosts, explicit allow list), server and target caps, audit log | built |
| A scenario reading server files and sending them to the target | file feeders confined to `--data-dir`, path traversal rejected | built |
| A scenario reading the server's environment | server runs see only the run's own env and the project's secrets | built |
| A scenario making the server fetch internal URLs (SSRF) | server runs refuse `observe.prometheus.url`; Prometheus is reached only through integrations an admin configured; trace templates only build links | built |
| Notification webhooks aimed at internal services | destinations on loopback, private, link-local (cloud metadata), CGNAT, multicast and reserved addresses refused unless an admin allows private destinations per channel; checked at creation and on every dialled address (no DNS rebinding), environment proxies ignored, redirects not followed | built |
| Webhook URLs and signing secrets leaking | encrypted with the master key, never returned (only scheme and host); webhook bodies signed with HMAC-SHA256 | built |
| Code execution from a scenario | expressions are CEL (no I/O, no loops beyond the language); JavaScript script steps run in goja with no network, file, timer or module access, and each run is interrupted after one second | built |
| A plugin that misbehaves | plugins run as separate processes over gRPC: a crash or panic fails its steps and the process is restarted; configs are validated against the plugin's schema; the target policy applies to the address setting a plugin marks | partial: a plugin is trusted code with the worker's privileges, and connections it makes on its own are not vetted |
| Password guessing | argon2id hashing, per-email and per-address throttling, constant-time comparison, same timing for unknown users | built |
| Address spoofing to dodge throttling | forwarded headers trusted only from configured proxies | built |
| Cross-site request forgery | cookie-authenticated writes need a custom header that browsers cannot send cross-site without a preflight the server never grants; SameSite=Lax cookies | built |
| Script injection in the UI | strict Content-Security-Policy with hashed inline scripts only, no external sources | built |
| Stolen API token | tokens hashed at rest, revocable, optional expiry, can never exceed their owner's current role, cannot mint other tokens | built |
| Secrets leaking into reports, logs or AI prompts | secrets never returned by the API; env values stored redacted on runs; AI traffic redacted (tokens, cookies, auth headers, emails, phone and card numbers) before leaving | built |
| Database dump exposes secrets | AES-256-GCM envelope encryption bound to project and name; master key kept outside the database | built |
| A rogue worker | with `--worker-mtls`: each worker enrolls for its own short-lived certificate by proving it knows the join token without sending it; without it, the token is compared in constant time. A worker only receives runs assigned to it | partial: enrollment is gated by one shared token, and there is no per-worker revocation short of rotating it |
| Eavesdropping or impersonation between server and workers | mutual TLS 1.3 with a CA derived from the master key (default in Compose and Helm); enrollment proofs bound to the TLS session; worker certificates valid only for client authentication; optional CA fingerprint pinning | built |
| Denial of service on the worker port | gRPC pinned past GO-2026-6443; keepalive limits | partial: no rate limiting per peer |
| Runaway run | kill switch (UI, CLI, API), worker dead man's switch after 10 s without the server, breakpoint auto-stop, auto-abort on sustained errors or latency (server floor 90% errors for 30 s by default) | built |
| Supply chain | govulncheck and golangci-lint (gosec) in CI, dependency updates by the maintainers (`make update`), pinned workflow actions, distroless non-root images; signed releases and SBOMs from GoReleaser | built in config; not yet exercised by a published release |

## Known gaps

- Runs are not handed over between server replicas. With `--ha standby`
  a new leader marks the old leader's running runs as failed; with
  `--ha active` the other replicas do so once the owner has been silent
  for 30 seconds. Either way the run's data so far is kept.
- Workers share a join token for enrollment; a leaked token lets an
  attacker enroll a worker and receive scenarios (including the run's
  secrets) for runs assigned to it. Rotate the token if it leaks: enrolled
  certificates expire within 24 hours. Keep the worker port off the public
  internet where you can.
- A worker without `--ca-fingerprint` trusts the CA at its first
  enrollment. Only a holder of the join token can answer that enrollment,
  so an attacker would need the token. Pin the fingerprint to remove even
  that.
- Plugins run with the privileges of the worker or CLI that starts them.
  Install only plugins you trust; `stampede plugin install` builds from
  source you choose, but does not verify signatures.
- AI dry runs send real requests to the target (for example a checkout
  creates a real order); use a test environment.

Report vulnerabilities as described in [SECURITY.md](../SECURITY.md).
