# Configuration

## Server (`stampede server`)

| Flag | Environment | Default | Meaning |
|---|---|---|---|
| `--addr` | `STAMPEDE_ADDR` | `:8080` | HTTP listen address (API, UI, `/metrics`, `/healthz`, `/readyz`) |
| `--database-url` | `STAMPEDE_DATABASE_URL` | — | PostgreSQL URL; migrations run on start |
| — | `STAMPEDE_MASTER_KEY` | — | 32 random bytes, base64 (`stampede keygen`); encrypts secrets and AI keys |
| — | `STAMPEDE_MASTER_KEY_FILE` | — | read the key from a file instead |
| — | `STAMPEDE_MASTER_KEY_AUTOGEN` | `false` | with `_FILE`, create the key file on first start (Compose does this) |
| `--worker-addr` | `STAMPEDE_WORKER_ADDR` | `:8081` | gRPC port workers connect to |
| `--join-token` | `STAMPEDE_JOIN_TOKEN` | — | shared secret workers must present; workers are disabled without it |
| `--worker-tls-cert`, `--worker-tls-key` | `STAMPEDE_WORKER_TLS_CERT`, `_KEY` | — | TLS for the worker port |
| `--executor` | `STAMPEDE_EXECUTOR` | `auto` | `auto`: workers when any are idle, else in-process; `workers`; `local` |
| `--data-dir` | `STAMPEDE_DATA_DIR` | — | folder holding CSV/JSON feeder files; file feeders are refused without it |
| `--max-rate`, `--max-vus`, `--max-duration` | — | none | hard caps applied to every run |
| `--abort-errors`, `--abort-for` | `STAMPEDE_ABORT_ERRORS` | `90%`, `30s` | stop any run whose error rate stays at or above this; `0` disables |
| `--trusted-proxy` | `STAMPEDE_TRUSTED_PROXIES` (comma separated) | none | reverse proxies whose `X-Forwarded-For` is believed |
| `--secure-cookies` | `STAMPEDE_SECURE_COOKIES=true` | `false` | mark the session cookie Secure (behind HTTPS) |
| `--log-level`, `--log-format` | `STAMPEDE_LOG_LEVEL`, `STAMPEDE_LOG_FORMAT` | `info`, `json` | logging |
| `--migrate-only`, `--migrate-dry-run` | — | — | apply or report migrations, then exit |

Back up the master key with the database: secrets cannot be decrypted
without it. See [upgrades and backups](../deploy/upgrades.md).

## Worker (`stampede worker`)

| Flag | Meaning |
|---|---|
| `--server host:8081` | the server's worker port (workers connect out, so they work behind NAT) |
| `--token` / `STAMPEDE_JOIN_TOKEN` | the join token |
| `--name`, `--region`, `--label k=v` | identity; regions let a run split load by region |
| `--max-vus` | the most users this worker accepts |
| `--insecure` or `--ca file.pem` | plain gRPC on trusted networks, or TLS with a private CA |

A worker that loses the server for 10 seconds during a run stops its load
on its own.

## CLI

`stampede login` stores the server URL and an API token in the user config
folder (`~/.config/stampede/config.yaml` on Linux,
`~/Library/Application Support/stampede/config.yaml` on macOS).
`STAMPEDE_SERVER` and `STAMPEDE_TOKEN` override it.

`stampede run` passes the process environment to `${env.X}`, and to
`${secret.X}` (also as `STAMPEDE_SECRET_X`). On the server, `${env.X}` comes
only from the run's own environment and `${secret.X}` from the project's
stored secrets; the server's own environment is never exposed to scenarios.
