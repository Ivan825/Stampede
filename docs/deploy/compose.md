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
open http://localhost:8080        # the first visit creates the owner account
```

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
crosses the network ([how it works](../concepts/distributed.md#worker-security)).
The server logs the CA fingerprint at start (`worker mutual TLS on ...
ca=sha256:...`). Pass it as `--ca-fingerprint` so a worker accepts only
this server, even at its first enrollment.
