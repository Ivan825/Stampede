# Kubernetes operator

The operator in [`deploy/operator`](../../deploy/operator) (its own Go
module, built with controller-runtime) adds two resources in the
`stampede.dev/v1alpha1` API:

- **StampedeCluster** deploys a Stampede server and workers, a simplified
  form of the [Helm chart](helm.md).
- **StampedeRun** declares one load test. The operator creates it through
  the Stampede REST API, can scale a worker Deployment up for the run and
  back down afterwards, and writes the result to the object's status.

Status: **alpha**. What is listed below is implemented and covered by the
envtest suite (`make -C deploy/operator test`: controllers running against a
real kube-apiserver and etcd, with a fake Stampede API). The kind test
(`deploy/kind/operator-smoke.sh`, run by `.github/workflows/k8s.yml`) drives
a real server and workers; see "Verification" at the end for what has and
has not been run.

## Install

No release has been published yet, so build the image and deploy it with
kustomize (CRDs, RBAC and the manager in `stampede-system`):

```sh
docker build -t ghcr.io/ivan825/stampede-operator:dev deploy/operator
# make the image available to your cluster (push it, or `kind load docker-image`)
cd deploy/operator/config/manager && kustomize edit set image ghcr.io/ivan825/stampede-operator:dev && cd -
kubectl apply -k deploy/operator/config/default
```

After a release, `ghcr.io/ivan825/stampede-operator:<version>` is published
alongside the Stampede image and `kubectl apply -k
deploy/operator/config/default` works as is (it uses the `latest` tag).

The manager runs nonroot with a read-only root filesystem in a namespace
labelled for the `restricted` Pod Security Standard, with leader election
so a second replica can stand by.

## StampedeCluster

```yaml
apiVersion: stampede.dev/v1alpha1
kind: StampedeCluster
metadata:
  name: lab
spec:
  image: ghcr.io/ivan825/stampede:0.1.0
  database:
    urlSecretRef: { name: lab-db, key: url }   # postgres://... (required)
  workers:
    replicas: 2
    region: kubernetes
    labels: { pool: default }
```

The operator creates:

| Object | Name | Notes |
|---|---|---|
| Secret | `<name>-stampede-keys` | generated `master-key` (32 random bytes, base64) and `join-token`, created once and never changed; **not** owned by the StampedeCluster, so deleting the cluster keeps the key the database depends on |
| Deployment | `<name>-server` | one replica, `Recreate` strategy, probes on `/healthz` and `/readyz` |
| Service | `<name>` | `http` 8080, `workers` 8081 |
| Deployment | `<name>-worker` | `stampede worker --server <name>:8081 --mtls --region ... --label ...` (the server runs with `--worker-mtls`) |

Use your own key or token with `masterKeySecretRef` / `joinTokenSecretRef`.
Everything is nonroot, read-only root filesystem, all capabilities dropped.

Status: `phase` (`Pending` until the server is available, then `Ready`),
`url` (in-cluster address of the UI and API), `workerAddress`,
`workerDeployment`, `readyWorkers`, `keysSecret` and a `Ready` condition.

Not in StampedeCluster (use the Helm chart for these): a bundled database,
Ingress, TLS on the worker port, worker autoscaling, PodDisruptionBudgets,
ServiceMonitor, scheduling settings (node selectors, tolerations,
affinity) and extra environment variables.

## StampedeRun

```yaml
apiVersion: stampede.dev/v1alpha1
kind: StampedeRun
metadata:
  name: release-42-smoke
spec:
  server: { clusterRef: lab }            # or { url: http://stampede.stampede.svc:8080 }
  apiTokenSecretRef: { name: stampede-api-token, key: token }
  project: checkout                       # ID, slug or name; created if missing
  target: { url: http://shop.shop.svc:8090 }
  scenario:
    name: checkout-smoke                  # a stored scenario (optionally version: 3) ...
    # inline: |                           # ... or a whole scenario document
    #   apiVersion: stampede.dev/v1
    #   kind: Scenario
    #   ...
  overrides: { mode: rate, rate: 50/s, duration: 2m }
  env: { BUILD: "42" }
  workers: 3                              # workers that share the run (0 = all connected)
  scaleWorkers: { replicas: 3, waitTimeoutSeconds: 300 }
  note: release 42
```

What the controller does:

1. Reads the API token from the Secret (create one under Settings > API
   tokens). Role **runner** is enough when the project, target and
   scenario already exist; **editor** is needed when the operator creates
   any of them.
2. Resolves the project (by ID, slug or name, creating it when missing),
   the target (the project target with this base URL, registered when
   missing) and the scenario (stored by name or ID; an inline document is
   saved under its `metadata.name`, as a new version only when it
   changed).
3. With `scaleWorkers`, records the worker Deployment's replica count in an
   annotation, scales it to `replicas` and waits until enough workers are
   connected to the server (`workers`, or `replicas` when `workers` is 0),
   failing after `waitTimeoutSeconds`. The Deployment defaults to the
   StampedeCluster's worker Deployment; with `server.url` name it with
   `scaleWorkers.deployment` (same namespace). While a run holds the
   Deployment, other StampedeRuns that want it wait, and a StampedeCluster
   leaves its replica count alone.
4. Creates the run. Its note carries a `[k8s:<ns>/<name> <uid>]` marker, so
   a retry after a lost status update finds the run instead of starting a
   second one.
5. Polls the run every 5 seconds. When it ends, records `verdict`,
   `summary`, `completedAt`, and scales the workers back.

Status fields: `phase` (`Pending`, `Scaling`, `Running`, `Completed`,
`Failed`), `runId`, `serverStatus`, `verdict` (`pass`, `fail`,
`generator-limited`, `no-targets`), `reportURL` (the run's page in the web
UI), `summary` (requests, error rate, RPS, p95, p99), `message`, the
resolved IDs, and a `Ready` condition once the run is over.

`Completed` means the run finished; whether it met its targets is the
`verdict`. In a pipeline:

```sh
kubectl wait stampederun/release-42-smoke --for=jsonpath='{.status.phase}'=Completed --timeout=30m
test "$(kubectl get stampederun/release-42-smoke -o jsonpath='{.status.verdict}')" = pass
```

The spec is immutable (enforced by the CRD): to run again, create a new
StampedeRun. Deleting a StampedeRun whose run is still going stops the run
and restores the workers before the object goes away (finalizer
`stampede.dev/run-cleanup`). `kubectl get stampederuns` shows phase and
verdict; `-o wide` adds the run ID and report URL.

Limitations in this version:

- Do not point `scaleWorkers` at a Deployment an HPA manages; they would
  fight over the replica count.
- The operator must reach the server URL over HTTP from its own pod.
- Progress is polled, not streamed; live metrics are in the web UI.
- A StampedeCluster runs one server replica. A StampedeRun can point at a
  Helm install with `server.ha: active`: status and stop go through the
  database, so any replica answers, but the kind test covers one replica
  only.

## Verification

- `make -C deploy/operator test TESTFLAGS=-race`: 5 envtest tests (cluster
  objects and stable keys, scale-up/run/restore, failures, deletion stops
  the run, CRD validation) and 3 API client tests, all passing locally on
  Kubernetes 1.37 envtest binaries.
- `golangci-lint` with the repository config: no issues.
- `deploy/kind/operator-smoke.sh` (StampedeCluster on kind, then a
  StampedeRun that scales workers from 2 to 3 and runs a real 10-second
  distributed test) passes in the `kubernetes` workflow on every change to
  the chart or the operator.
