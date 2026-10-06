# Deploy on Kubernetes with Helm

The chart in [`deploy/helm/stampede`](../../deploy/helm/stampede) runs:

- the **server** (REST API, run manager, web UI) behind a Service with
  `http` on 8080 and the worker gRPC port on 8081;
- a **worker** Deployment whose pods dial the server with the join token;
- optionally a single-node **TimescaleDB** StatefulSet with a persistent
  volume, or a connection to an existing PostgreSQL.

It is tested on kind by [`deploy/kind/smoke.sh`](../../deploy/kind/smoke.sh)
(also run in CI by `.github/workflows/k8s.yml`): install with the bundled
database, `helm test`, then a 10-second distributed run on two in-cluster
workers that must pass.

The chart is not yet published to a chart repository; install it from a
checkout.

## Install

```sh
git clone https://github.com/Ivan825/Stampede && cd Stampede
helm install stampede deploy/helm/stampede -n stampede --create-namespace \
  --set image.tag=<release version>     # until a release exists, see "Local image" below
kubectl -n stampede rollout status deploy/stampede-server
helm test stampede -n stampede
kubectl -n stampede port-forward svc/stampede 8080:8080   # then open http://localhost:8080
```

The first visit to the web UI creates the owner account.

### Local image

No release has been published yet, so build the image yourself and make it
available to the cluster, for example on kind:

```sh
docker build -t ghcr.io/ivan825/stampede:dev .
kind load docker-image ghcr.io/ivan825/stampede:dev
helm install stampede deploy/helm/stampede -n stampede --create-namespace \
  --set image.tag=dev --set image.pullPolicy=Never
```

## Secrets

The chart's Secret (`<release>-stampede`, or `stampede` when the release is
called `stampede`) holds:

| Key | What | Default |
|---|---|---|
| `master-key` | encrypts secrets stored in the database | 32 random bytes, generated |
| `join-token` | what workers present to join | 48 random characters, generated |
| `postgres-password` | bundled TimescaleDB password | 32 random characters, generated |
| `database-url` | only with `database.external.url` | |

Generated values are created on the first install and **reused on every
upgrade** (the chart reads the existing Secret with `lookup`), so
`helm upgrade` never rotates them. The Secret carries
`helm.sh/resource-policy: keep` and survives `helm uninstall`; delete it by
hand once you no longer need the data.

Bring your own instead with `masterKey.existingSecret`,
`joinToken.existingSecret` and `timescaledb.existingSecret` (each with an
`existingSecretKey`), or literal values (`masterKey.value`, ...).

**Back up the master key.** Without it, secrets stored in Stampede cannot be
decrypted:

```sh
kubectl -n stampede get secret stampede -o jsonpath='{.data.master-key}' | base64 -d > master.key
```

Note that `helm template` has no cluster to look up, so it generates new
values every time; use `helm install/upgrade`, or pass existing secrets,
when rendering manifests for GitOps tools.

## Database

Bundled (default): `timescaledb.enabled=true` with an 8Gi volume. It is a
single pod without replication or automatic backups: good for evaluation and
small teams. See [upgrades.md](upgrades.md) for backups.

External PostgreSQL (TimescaleDB recommended; the schema migration only
turns on hypertables and compression when the `timescaledb` extension is
available, so plain PostgreSQL is accepted too):

```sh
kubectl -n stampede create secret generic stampede-db \
  --from-literal=url='postgres://stampede:PASSWORD@db.example.com:5432/stampede?sslmode=require'
helm install stampede deploy/helm/stampede -n stampede \
  --set timescaledb.enabled=false \
  --set database.external.existingSecret=stampede-db --set database.external.existingSecretKey=url
```

## Workers

In-cluster workers dial `<fullname>:8081` with the join token. Values:

| Value | Meaning |
|---|---|
| `workers.replicas` | fixed number of workers (default 2) |
| `workers.region` | region label used to split load by region |
| `workers.labels` | extra `--label KEY=VALUE` |
| `workers.maxVUs` | most virtual users one worker accepts |
| `workers.autoscaling.*` | CPU-based HorizontalPodAutoscaler (needs metrics-server; no KEDA) |

About autoscaling: a run's load is split across the workers connected when
it starts. The HPA therefore keeps capacity in line with how busy workers
have been; it does not reshard a run that is already going. It scales down
one pod every two minutes after a ten-minute window, because removing a
worker mid-run loses its share of that run. For a fixed fleet per run, the
operator's `StampedeRun` can scale a worker Deployment up for exactly one
run ([operator.md](operator.md)); do not combine that with the HPA.

Workers have no probes: they serve no port. A worker that loses the server
reconnects by itself, and stops generating load if it is cut off for 10
seconds during a run.

Workers elsewhere (VMs, other clusters, other clouds) need the worker port
exposed (for example `service.type=LoadBalancer`, or a separate Service)
and the join token; see [terraform.md](terraform.md). Use TLS on the worker
port when it crosses an untrusted network: create a TLS Secret with
`tls.crt`, `tls.key` and `ca.crt` (the certificate must name the service
host), then set `workerTLS.enabled=true` and `workerTLS.secretName`. The
TLS settings render and match the server's `--worker-tls-cert/key` and the
worker's `--ca` flags, but have not been exercised on a cluster yet.

## One server replica

Keep `server.replicas` at 1 (the default). In this version:

- each run is owned by the server replica that started it;
- each worker holds one long-lived gRPC stream to one replica, so a run only
  uses the workers attached to the replica that owns it;
- a starting server marks **every** unfinished run in the database as
  failed, including runs another replica is still driving.

For the same reason the server Deployment uses the `Recreate` strategy: the
old pod stops (finishing its runs' reports within
`server.terminationGracePeriodSeconds`) before the new one starts. An
upgrade therefore has a short UI/API outage; plan upgrades between runs.

## Ingress and TLS for the UI

```yaml
ingress:
  enabled: true
  className: nginx
  hosts:
    - host: stampede.example.com
      paths: [{ path: /, pathType: Prefix }]
  tls:
    - secretName: stampede-tls
      hosts: [stampede.example.com]
server:
  secureCookies: true
  trustedProxies: ["10.0.0.0/8"]   # the ingress controller's pod network
```

The live-run view uses server-sent events; ingress controllers that buffer
responses need buffering off for `/api/v1/runs/*/live` (for ingress-nginx:
`nginx.ingress.kubernetes.io/proxy-buffering: "off"`).

## Security

Every pod runs as a non-root user with a read-only root filesystem, all
capabilities dropped, no privilege escalation and the `RuntimeDefault`
seccomp profile; service account tokens are not mounted. The workloads are
compatible with the `restricted` Pod Security Standard.

## Monitoring

The server exposes Prometheus metrics on `/metrics` (port `http`). With the
Prometheus Operator, set `serviceMonitor.enabled=true` (and
`serviceMonitor.labels` to match your Prometheus selector).

## Values reference

All values are documented in
[`values.yaml`](../../deploy/helm/stampede/values.yaml) and checked by
[`values.schema.json`](../../deploy/helm/stampede/values.schema.json):
unknown keys and invalid enums are rejected at install time.

## Uninstall

```sh
helm uninstall stampede -n stampede
# kept on purpose: the Secret with the master key, and the database volume
kubectl -n stampede delete secret stampede
kubectl -n stampede delete pvc data-stampede-timescaledb-0
```
