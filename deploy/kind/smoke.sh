#!/usr/bin/env bash
# Smoke test of the Helm chart on kind:
#   1. create a kind cluster (unless SKIP_CLUSTER=1)
#   2. build the Stampede image (unless SKIP_BUILD=1) and load it into kind
#   3. install the chart with the bundled TimescaleDB and wait for it
#   4. run `helm test`
#   5. through the REST API: create the owner and an API token, check both
#      workers joined, and run a 10-second distributed test that must pass
#
# The API token is saved in the Secret stampede-api-token (key: token) for the
# operator test (deploy/kind/operator-smoke.sh).
#
# Needs: docker, kind, kubectl, helm, curl, jq.
set -euo pipefail

CLUSTER=${CLUSTER:-stampede}
NS=${NS:-stampede}
IMAGE=${IMAGE:-ghcr.io/ivan825/stampede:kind}
ROOT=$(cd "$(dirname "$0")/../.." && pwd)
CHART=$ROOT/deploy/helm/stampede
PORT=${PORT:-18080}

log() { printf '\n==> %s\n' "$*"; }

if [[ ${SKIP_CLUSTER:-0} != 1 ]]; then
  log "creating kind cluster $CLUSTER"
  kind create cluster --name "$CLUSTER" --config "$ROOT/deploy/kind/cluster.yaml" --wait 180s
fi

if [[ ${SKIP_BUILD:-0} != 1 ]]; then
  log "building $IMAGE"
  docker build -t "$IMAGE" "$ROOT"
fi
log "loading $IMAGE into kind"
kind load docker-image "$IMAGE" --name "$CLUSTER"

log "installing the chart"
kubectl create namespace "$NS" --dry-run=client -o yaml | kubectl apply -f -
helm upgrade --install stampede "$CHART" -n "$NS" \
  -f "$CHART/ci/kind-values.yaml" \
  --set image.repository="${IMAGE%:*}" --set image.tag="${IMAGE##*:}" \
  --wait --timeout 8m
kubectl -n "$NS" get pods -o wide

log "helm test"
helm test stampede -n "$NS" --logs

log "port-forwarding the API to localhost:$PORT"
kubectl -n "$NS" port-forward svc/stampede "$PORT":8080 >/tmp/stampede-pf.log 2>&1 &
PF=$!
trap 'kill $PF 2>/dev/null || true' EXIT
for _ in $(seq 1 30); do
  curl -fsS "http://127.0.0.1:$PORT/healthz" >/dev/null 2>&1 && break
  sleep 1
done
API=http://127.0.0.1:$PORT/api/v1
JAR=$(mktemp)

log "creating the owner account and an API token"
if [[ $(curl -fsS "$API/version" | jq -r .setupRequired) == true ]]; then
  curl -fsS -c "$JAR" -H 'X-Stampede-CSRF: 1' -H 'Content-Type: application/json' "$API/setup" \
    -d '{"organisation":"kind","name":"Kind Smoke","email":"smoke@example.com","password":"kind-smoke-password"}' >/dev/null
else
  curl -fsS -c "$JAR" -H 'X-Stampede-CSRF: 1' -H 'Content-Type: application/json' "$API/auth/login" \
    -d '{"email":"smoke@example.com","password":"kind-smoke-password"}' >/dev/null
fi
TOKEN=$(curl -fsS -b "$JAR" -H 'X-Stampede-CSRF: 1' -H 'Content-Type: application/json' "$API/tokens" \
  -d '{"name":"kind-smoke-'"$(date +%s)"'","role":"admin","expiresInDays":1}' | jq -r .secret)
rm -f "$JAR"
kubectl -n "$NS" create secret generic stampede-api-token --from-literal=token="$TOKEN" \
  --dry-run=client -o yaml | kubectl apply -f -
auth=(-H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json')

log "waiting for 2 workers"
for i in $(seq 1 60); do
  n=$(curl -fsS "${auth[@]}" "$API/workers" | jq 'length')
  [[ $n -ge 2 ]] && break
  [[ $i == 60 ]] && { echo "only $n workers joined"; exit 1; }
  sleep 2
done
curl -fsS "${auth[@]}" "$API/workers" | jq -c '.[] | {name, region, labels, status}'

log "running a 10-second distributed test"
PROJECT=$(curl -fsS "${auth[@]}" "$API/projects" -d '{"name":"kind smoke '"$(date +%s)"'"}' | jq -r .id)
TARGET=$(curl -fsS "${auth[@]}" "$API/projects/$PROJECT/targets" \
  -d '{"name":"server","baseURL":"http://stampede:8080"}' | jq -r .id)
SCENARIO=$(jq -Rs '{yaml: .}' "$ROOT/deploy/kind/smoke-scenario.yaml" |
  curl -fsS "${auth[@]}" "$API/projects/$PROJECT/scenarios" -d @- | jq -r .id)
RUN=$(curl -fsS "${auth[@]}" "$API/projects/$PROJECT/runs" \
  -d '{"scenarioId":"'"$SCENARIO"'","targetId":"'"$TARGET"'","workers":2,"note":"kind smoke"}' | jq -r .id)
echo "run $RUN"
for i in $(seq 1 90); do
  run=$(curl -fsS "${auth[@]}" "$API/runs/$RUN")
  status=$(jq -r .status <<<"$run")
  case $status in completed|failed|aborted) break ;; esac
  [[ $i == 90 ]] && { echo "run still $status"; exit 1; }
  sleep 2
done
jq -c '{status, verdict, workers, summary}' <<<"$run"
[[ $status == completed ]] || { echo "run $status: $(jq -r .error <<<"$run")"; exit 1; }
[[ $(jq -r .verdict <<<"$run") == pass ]] || { echo "verdict is not pass"; exit 1; }
[[ $(jq -r .workers <<<"$run") == 2 ]] || { echo "the run did not use both workers"; exit 1; }

log "chart smoke test passed"
