# Deploy with Docker Compose

[`docker-compose.yml`](../../docker-compose.yml) at the repository root runs
the whole stack on one machine:

| Service | What | Port |
|---|---|---|
| `db` | TimescaleDB for runs, metrics and reports | internal |
| `server` | API, run manager and web UI | 8080 |
| `worker-1`, `worker-2` | load generators that dial `server:8081` | none |
| `shoplab` | the demo target app with planted bottlenecks | 8090 |

```sh
git clone https://github.com/Ivan825/Stampede && cd Stampede
docker compose up -d
stampede setup                    # create the organisation and the owner account
open http://localhost:8080        # the web UI: runs, reports and settings, read only
```

Everything else (projects, targets, scenarios, runs) is done with the
`stampede` CLI; the web UI is for analysis.

The image is built from the repository's `Dockerfile` (distroless, nonroot)
and tagged `ghcr.io/ivan825/stampede:${STAMPEDE_VERSION:-local}`. Once
releases are published you can use a released image instead of building:
`STAMPEDE_VERSION=<version> docker compose up -d --no-build`.

## Settings

Set these in the environment or a `.env` file next to `docker-compose.yml`:

| Variable | Default | Change it when |
|---|---|---|
| `STAMPEDE_DB_PASSWORD` | `stampede` | anything other than a laptop |
| `STAMPEDE_JOIN_TOKEN` | `local-compose-join-token` | anything other than a laptop |
| `STAMPEDE_PORT` | `8080` | the port is taken |
| `STAMPEDE_VERSION` | `local` | using a released image |
| `STAMPEDE_OTLP_ENDPOINT` | empty (tracing off) | sending the server's traces to Jaeger or another OTLP collector |
| `STAMPEDE_PROMETHEUS_PORT`, `STAMPEDE_GRAFANA_PORT`, `STAMPEDE_JAEGER_PORT` | `9090`, `3000`, `16686` | a port of the `observability` profile is taken |
| `STAMPEDE_GRAFANA_PASSWORD` | `admin` | anything other than a laptop |

## Optional services

Two [profiles](https://docs.docker.com/compose/how-tos/profiles/) add
services that do not start by default:

| Profile | Services | Port |
|---|---|---|
| `browser` | `browser-worker`: a worker with Chromium, for scenarios with [browser steps](../protocols.md) | none |
| `observability` | `prometheus` scraping the server's `/metrics` | 9090 |
| | `grafana` with Prometheus and Jaeger data sources and the [Stampede server dashboard](../reference/configuration.md#metrics-and-the-grafana-dashboard) | 3000 |
| | `jaeger` receiving the server's OpenTelemetry traces (OTLP on 4317 and 4318) | 16686 |

```sh
docker compose --profile browser up -d --build
STAMPEDE_OTLP_ENDPOINT=http://jaeger:4318 docker compose --profile observability up -d
docker compose --profile browser --profile observability down   # stops the profile services too
```

**Browser worker.** It is built from
[`deploy/docker/Dockerfile.browser-worker`](../../deploy/docker/Dockerfile.browser-worker)
and joins the server like the other workers. The server does not yet
choose workers by protocol: a run uses every idle worker (or as many as
you ask for), and a worker without Chromium refuses a run with browser
steps, which fails the run. To run browser scenarios through the server,
stop the other workers first (`docker compose stop worker-1 worker-2`).
Count on 100–200 MB of memory per browser user.

**Observability.** Grafana opens on the Stampede server dashboard
(anonymous users can view; sign in as `admin` with
`STAMPEDE_GRAFANA_PASSWORD`, default `admin`, to edit). The dashboard's
`job` variable is `stampede-server`. The server sends traces only when
`STAMPEDE_OTLP_ENDPOINT` is set, as above or in `.env`; it is passed to the
server as `OTEL_EXPORTER_OTLP_ENDPOINT` and is empty by default, which
keeps tracing off (see [tracing](../reference/configuration.md#tracing-opentelemetry)).
Jaeger keeps traces in memory, so they are gone when it restarts.
Prometheus keeps 7 days of metrics in the `stampede-prometheus` volume.

These services watch Stampede itself. To chart your target's metrics or
link to its traces in run reports, add your own Prometheus and tracing
system as [integrations](../guides/integrations.md).

CI checks that the Compose file parses with both profiles (`docker
compose config`); the profile services are not started in CI.

## Master key

The server generates its master key on first start into the `stampede-data`
volume (`/data/master.key`, `STAMPEDE_MASTER_KEY_AUTOGEN=true`). Secrets
stored in Stampede are encrypted with it. **Back it up** together with the
database:

```sh
docker compose cp server:/data/master.key ./master.key
```

See [upgrades.md](upgrades.md) for database backups and restores.

## Adding workers on other machines

Workers only need to reach the server's worker port and the system under
test. The Compose file keeps 8081 on the internal network; publish it (add
`"8081:8081"` to the server's `ports`) and run on each machine:

```sh
STAMPEDE_JOIN_TOKEN=... stampede worker --server <server-host>:8081 --mtls \
  --ca-fingerprint sha256:... --region <name>
```

The Compose stack runs the worker port with mutual TLS
(`STAMPEDE_WORKER_MTLS=true`). Each worker enrolls for its own certificate
from a CA the server derives from its master key, and the join token never
crosses the network ([how it works](../architecture.md#worker-security)).
The server logs the CA fingerprint at start (`worker mutual TLS on ...
ca=sha256:...`). Pass it as `--ca-fingerprint` so a worker accepts only
this server, even at its first enrollment.
