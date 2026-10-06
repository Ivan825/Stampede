#!/usr/bin/env bash
# Installs Stampede from this offline bundle.
#
#   ./install.sh                       verify checksums and load the images into Docker
#   ./install.sh --registry reg:5000   also push the images to a private registry (for Kubernetes)
#   ./install.sh --verify-only         only check the bundle's checksums
#
# Then start the stack with `docker compose -f compose/docker-compose.yml up -d`,
# or install the Helm chart with helm/values-airgap.yaml (see README.md).
set -euo pipefail
cd "$(dirname "$0")"

REGISTRY=""
VERIFY_ONLY=false
while [ $# -gt 0 ]; do
  case "$1" in
    --registry) REGISTRY=${2:?--registry needs host[:port]}; shift 2 ;;
    --verify-only) VERIFY_ONLY=true; shift ;;
    -h|--help) sed -n '2,10p' "$0"; exit 0 ;;
    *) echo "unknown option $1" >&2; exit 2 ;;
  esac
done

echo "==> checking SHA256SUMS"
if command -v sha256sum >/dev/null; then sha256sum --quiet -c SHA256SUMS; else shasum -a 256 -q -c SHA256SUMS; fi
$VERIFY_ONLY && { echo "bundle verified"; exit 0; }

echo "==> loading images"
gunzip -c images/images.tar.gz | docker load

if [ -n "$REGISTRY" ]; then
  echo "==> pushing to $REGISTRY"
  while read -r img; do
    [ -z "$img" ] && continue
    path=${img#*/}           # drop the source registry host
    case "$img" in */*/*) ;; *) path=$img ;; esac
    docker tag "$img" "$REGISTRY/$path"
    docker push "$REGISTRY/$path"
  done < images/images.txt
fi

arch=$(uname -m); case "$arch" in x86_64) arch=amd64 ;; aarch64|arm64) arch=arm64 ;; esac
cat <<MSG

Stampede $(cat VERSION) is loaded.

  Docker Compose:  docker compose -f compose/docker-compose.yml up -d     (then http://localhost:8080)
  Kubernetes:      helm install stampede helm/stampede -f helm/values-airgap.yaml \
                     --set image.repository=${REGISTRY:-<your-registry>}/ivan825/stampede \
                     --set timescaledb.image.repository=${REGISTRY:-<your-registry>}/timescale/timescaledb
  CLI:             bin/stampede-linux-$arch (and macOS/Windows builds in bin/)
MSG
