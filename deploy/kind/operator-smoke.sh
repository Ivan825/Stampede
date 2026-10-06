#!/usr/bin/env bash
# Smoke test of the operator on kind. Run deploy/kind/smoke.sh first: this
# reuses its cluster, Stampede image and bundled TimescaleDB.
#   1. build the operator image (unless SKIP_BUILD=1), load it, deploy it
#   2. create a database for a second Stampede and apply a StampedeCluster
#   3. create the owner and an API token on that server
#   4. apply a StampedeRun that scales the workers from 2 to 3, runs a
#      10-second test on all three, and must end Completed with verdict pass
#      and the workers back at 2
#
# Needs: docker, kind, kubectl, curl, jq.
set -euo pipefail

CLUSTER=${CLUSTER:-stampede}
NS=${NS:-stampede-op}
CHART_NS=${CHART_NS:-stampede}
IMAGE=${IMAGE:-ghcr.io/ivan825/stampede:kind}
OPERATOR_IMAGE=ghcr.io/ivan825/stampede-operator:kind
ROOT=$(cd "$(dirname "$0")/../.." && pwd)
PORT=${PORT:-18081}

log() { printf '\n==> %s\n' "$*"; }
dump() {
  kubectl -n stampede-system logs deploy/stampede-operator-controller-manager --tail=60 || true
  kubectl -n "$NS" get stampedeclusters,stampederuns,deploy,pods -o wide || true
  kubectl -n "$NS" get stampederun health-smoke -o yaml || true
}
trap 'echo "operator smoke test failed"; dump' ERR

if [[ ${SKIP_BUILD:-0} != 1 ]]; then
  log "building $OPERATOR_IMAGE"
  docker build -t "$OPERATOR_IMAGE" "$ROOT/deploy/operator"
fi
log "loading $OPERATOR_IMAGE into kind"
kind load docker-image "$OPERATOR_IMAGE" --name "$CLUSTER"

log "deploying the operator"
kubectl apply -k "$ROOT/deploy/operator/config/kind"
kubectl -n stampede-system rollout status deploy/stampede-operator-controller-manager --timeout=180s

log "creating a database for the StampedeCluster"
PGPASS=$(kubectl -n "$CHART_NS" get secret stampede -o jsonpath='{.data.postgres-password}' | base64 -d)
kubectl -n "$CHART_NS" exec stampede-timescaledb-0 -- \
  psql -U stampede -d stampede -tAc "SELECT 1 FROM pg_database WHERE datname='stampede_operator'" | grep -q 1 ||
  kubectl -n "$CHART_NS" exec stampede-timescaledb-0 -- psql -U stampede -d stampede -c 'CREATE DATABASE stampede_operator'
kubectl create namespace "$NS" --dry-run=client -o yaml | kubectl apply -f -
kubectl -n "$NS" create secret generic lab-db \
  --from-literal=url="postgres://stampede:$PGPASS@stampede-timescaledb.$CHART_NS.svc:5432/stampede_operator?sslmode=disable" \
  --dry-run=client -o yaml | kubectl apply -f -

log "applying a StampedeCluster"
kubectl -n "$NS" apply -f - <<EOF
apiVersion: stampede.dev/v1alpha1
kind: StampedeCluster
metadata:
  name: lab
spec:
  image: $IMAGE
  imagePullPolicy: Never
  database:
    urlSecretRef: { name: lab-db, key: url }
  server:
    resources:
      requests: { cpu: 50m, memory: 64Mi }
      limits: { memory: 512Mi }
  workers:
    replicas: 2
    region: kind
    labels: { managed-by: operator }
    resources:
      requests: { cpu: 50m, memory: 32Mi }
      limits: { memory: 256Mi }
EOF
kubectl -n "$NS" wait stampedecluster/lab --for=jsonpath='{.status.phase}'=Ready --timeout=300s
kubectl -n "$NS" rollout status deploy/lab-worker --timeout=180s
kubectl -n "$NS" get stampedecluster lab

log "creating the owner account and an API token on the lab server"
kubectl -n "$NS" port-forward svc/lab "$PORT":8080 >/tmp/stampede-op-pf.log 2>&1 &
PF=$!
trap 'kill $PF 2>/dev/null || true' EXIT
for _ in $(seq 1 30); do
  curl -fsS "http://127.0.0.1:$PORT/healthz" >/dev/null 2>&1 && break
  sleep 1
done
API=http://127.0.0.1:$PORT/api/v1
JAR=$(mktemp)
if [[ $(curl -fsS "$API/version" | jq -r .setupRequired) == true ]]; then
  curl -fsS -c "$JAR" -H 'X-Stampede-CSRF: 1' -H 'Content-Type: application/json' "$API/setup" \
    -d '{"organisation":"operator","name":"Operator Smoke","email":"op@example.com","password":"operator-smoke-password"}' >/dev/null
else
  curl -fsS -c "$JAR" -H 'X-Stampede-CSRF: 1' -H 'Content-Type: application/json' "$API/auth/login" \
    -d '{"email":"op@example.com","password":"operator-smoke-password"}' >/dev/null
fi
TOKEN=$(curl -fsS -b "$JAR" -H 'X-Stampede-CSRF: 1' -H 'Content-Type: application/json' "$API/tokens" \
  -d '{"name":"operator-'"$(date +%s)"'","role":"editor","expiresInDays":1}' | jq -r .secret)
rm -f "$JAR"
kubectl -n "$NS" create secret generic stampede-api-token --from-literal=token="$TOKEN" \
  --dry-run=client -o yaml | kubectl apply -f -

log "applying a StampedeRun that scales the workers to 3"
kubectl -n "$NS" delete stampederun health-smoke --ignore-not-found --wait=true
kubectl -n "$NS" apply -f - <<'EOF'
apiVersion: stampede.dev/v1alpha1
kind: StampedeRun
metadata:
  name: health-smoke
spec:
  server: { clusterRef: lab }
  apiTokenSecretRef: { name: stampede-api-token, key: token }
  project: kubernetes
  target: { url: "http://lab:8080" }
  scenario:
    inline: |
      apiVersion: stampede.dev/v1
      kind: Scenario
      metadata:
        name: operator-smoke
      journeys:
        - name: health
          steps:
            - get: /healthz
              check: { status: 200 }
      load:
        mode: rate
        rate: 20/s
        duration: 10s
      targets:
        - errors < 1%
  workers: 3
  scaleWorkers: { replicas: 3, waitTimeoutSeconds: 180 }
  note: kind operator smoke
EOF
for i in $(seq 1 120); do
  phase=$(kubectl -n "$NS" get stampederun health-smoke -o jsonpath='{.status.phase}')
  [[ $phase == Completed || $phase == Failed ]] && break
  [[ $((i % 10)) == 0 ]] && echo "phase=$phase $(kubectl -n "$NS" get stampederun health-smoke -o jsonpath='{.status.message}')"
  sleep 2
done
kubectl -n "$NS" get stampederun health-smoke -o wide
kubectl -n "$NS" get stampederun health-smoke -o jsonpath='{.status}' | jq .
[[ $phase == Completed ]] || { echo "StampedeRun phase $phase"; false; }
[[ $(kubectl -n "$NS" get stampederun health-smoke -o jsonpath='{.status.verdict}') == pass ]] || { echo "verdict is not pass"; false; }
RUN=$(kubectl -n "$NS" get stampederun health-smoke -o jsonpath='{.status.runId}')
[[ $(curl -fsS -H "Authorization: Bearer $TOKEN" "$API/runs/$RUN" | jq -r .workers) == 3 ]] || { echo "the run did not use 3 workers"; false; }
for _ in $(seq 1 30); do
  [[ $(kubectl -n "$NS" get deploy lab-worker -o jsonpath='{.spec.replicas}') == 2 ]] && break
  sleep 1
done
[[ $(kubectl -n "$NS" get deploy lab-worker -o jsonpath='{.spec.replicas}') == 2 ]] || { echo "workers were not scaled back to 2"; false; }
kubectl -n "$NS" get events --field-selector involvedObject.name=health-smoke --sort-by=.lastTimestamp | tail -6 || true

log "operator smoke test passed"
