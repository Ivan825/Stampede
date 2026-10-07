# Upgrades, backups and restores

## How upgrades work

- **Migrations run on start.** `stampede server` applies every pending
  database migration before it serves traffic, then listens. There is no
  separate migration step to remember.
- **Check first:** `stampede server --migrate-dry-run` connects with the
  same settings, logs the current schema version and whether migrations are
  pending (`msg="migration dry run" current=1 pending=false`), and exits
  without changing anything. Run it with the **new** version's binary or
  image.
- `stampede server --migrate-only` applies migrations and exits, for when
  you want them done before the new server starts.
- Migrations only go forward. To go back to an older version after a newer
  one has migrated the database, restore a backup taken before the upgrade.
- A stopping server stops its active runs and writes their reports first.
  Upgrade between runs.

Always take a backup (below) before upgrading.

### Docker Compose

```sh
docker compose exec -T db pg_dump -U stampede -Fc stampede > stampede-$(date +%F).dump
docker compose cp server:/data/master.key ./master.key

git pull                                     # or set STAMPEDE_VERSION to a released version
docker compose build server
docker compose run --rm --no-deps server server --migrate-dry-run
docker compose up -d
```

### Helm

```sh
# dry run with the new image, using the release's database settings
kubectl -n stampede apply -f - <<'EOF'
apiVersion: v1
kind: Pod
metadata:
  name: stampede-migrate-check
spec:
  restartPolicy: Never
  securityContext: { runAsNonRoot: true, runAsUser: 65532, seccompProfile: { type: RuntimeDefault } }
  containers:
    - name: check
      image: ghcr.io/ivan825/stampede:NEW_VERSION
      args: [server, --migrate-dry-run]
      env:
        - name: STAMPEDE_DB_PASSWORD
          valueFrom: { secretKeyRef: { name: stampede, key: postgres-password } }
        - name: STAMPEDE_DATABASE_URL
          value: postgres://stampede:$(STAMPEDE_DB_PASSWORD)@stampede-timescaledb:5432/stampede?sslmode=disable
      securityContext: { allowPrivilegeEscalation: false, readOnlyRootFilesystem: true, capabilities: { drop: [ALL] } }
EOF
kubectl -n stampede logs -f stampede-migrate-check
kubectl -n stampede delete pod stampede-migrate-check

helm upgrade stampede deploy/helm/stampede -n stampede --reuse-values --set image.tag=NEW_VERSION
kubectl -n stampede rollout status deploy/stampede-server
helm test stampede -n stampede
```

(With an external database, take `STAMPEDE_DATABASE_URL` from your own
Secret instead.)

The server Deployment uses the `Recreate` strategy, so the old server stops
before the new one starts and the UI is briefly unavailable. The chart keeps
the master key, join token and database password across upgrades; this was
checked on kind by comparing the Secret before and after `helm upgrade`.

### Operator

Re-apply `deploy/operator/config/default` with the new operator image. To
upgrade the Stampede server of a StampedeCluster, change `spec.image`; the
operator rolls the server (Recreate) and workers.

## Backups

Back up two things, together:

1. **The database.** Runs, scenarios, reports and encrypted secrets.
2. **The master key.** Without it the secrets in the database cannot be
   decrypted. It is not in the database.

| Install | Database | Master key |
|---|---|---|
| Compose | `docker compose exec -T db pg_dump -U stampede -Fc stampede > stampede.dump` | `docker compose cp server:/data/master.key ./master.key` |
| Helm, bundled DB | `kubectl -n stampede exec stampede-timescaledb-0 -- pg_dump -U stampede -Fc stampede > stampede.dump` | `kubectl -n stampede get secret stampede -o jsonpath='{.data.master-key}' \| base64 -d > master.key` |
| Helm or operator, external DB | your database's backup tooling, or `pg_dump -Fc "$DATABASE_URL"` | the Secret named by `masterKey.existingSecret` / `<cluster>-stampede-keys` |

### With the CLI

On a machine with the PostgreSQL client tools and network access to the
database, `stampede backup` and `stampede restore` wrap `pg_dump` and
`pg_restore` (they say so and stop when the tools are not on `PATH`):

```sh
export STAMPEDE_DATABASE_URL=postgres://stampede:...@db.internal:5432/stampede
stampede backup stampede-$(date +%F).dump        # pg_dump -Fc --no-owner
```

`pg_dump` must be at the database server's major version or newer (17 for
the bundled TimescaleDB image). The password is passed to the tools in
`PGPASSWORD`, not on their command line. In the Compose stack the database
is not published on the host, so use the `docker compose exec` commands
in the table below instead.

Store the master key separately from the dump (a password manager or secret
store), since together they unlock every stored secret.

`pg_dump` of a TimescaleDB database prints warnings about circular foreign
keys in TimescaleDB's own catalog; they are expected.

## Restore

Restore into a database with the **same TimescaleDB version** (the bundled
image is pinned: `timescale/timescaledb:2.22.1-pg17`), with Stampede
stopped. Example for Compose:

```sh
docker compose stop server worker-1 worker-2
docker compose exec -T db psql -U stampede -d postgres -c 'DROP DATABASE stampede' -c 'CREATE DATABASE stampede'
docker compose exec -T db psql -U stampede -d stampede \
  -c 'CREATE EXTENSION IF NOT EXISTS timescaledb' -c 'SELECT timescaledb_pre_restore()'
docker compose exec -T db pg_restore -U stampede -d stampede --no-owner < stampede.dump
docker compose exec -T db psql -U stampede -d stampede -c 'SELECT timescaledb_post_restore()'
docker compose cp ./master.key server:/data/master.key   # the key that matches this dump
docker compose up -d
```

With the CLI, create an empty database (or drop and recreate the old one),
stop the server and workers, then:

```sh
stampede restore stampede-2026-10-01.dump --database-url postgres://stampede:...@db.internal:5432/stampede
```

`restore` refuses a database that already has tables. When the backup is
of a TimescaleDB database (its table of contents lists the `timescaledb`
extension) it creates the extension and runs `timescaledb_pre_restore()`
before `pg_restore --no-owner` and `timescaledb_post_restore()` after, the
same steps as above. Put the matching master key in place and start the
server. The CLI's backup and restore have a round-trip test against plain
PostgreSQL, which runs only where `pg_dump` is at the test database's
version or newer; the TimescaleDB steps have not been rehearsed yet.

For Helm with the bundled database, the same commands run through
`kubectl -n stampede exec -i stampede-timescaledb-0 -- ...` after scaling
the server and workers to zero
(`kubectl -n stampede scale deploy/stampede-server deploy/stampede-worker --replicas=0`;
disable worker autoscaling first if it is on). Put the matching master key
in the chart's Secret (`kubectl -n stampede patch secret stampede ...`, or
`--set masterKey.value=...` on the next upgrade), then scale back up.

When the server starts on the restored database it applies any migrations
the dump is missing and marks runs that were in progress at backup time as
failed.

These restore steps follow TimescaleDB's documented
`timescaledb_pre_restore()`/`timescaledb_post_restore()` procedure. They
have not yet been rehearsed end to end against a Stampede database in this
repository; rehearse them on a copy before you rely on them.
