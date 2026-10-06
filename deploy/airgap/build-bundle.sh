#!/usr/bin/env bash
# Builds an offline bundle of Stampede for one Linux architecture: binaries,
# container images, the Compose stack, the Helm chart, the product packs and
# the docs, with checksums. Nothing in it is fetched at install time.
#
#   deploy/airgap/build-bundle.sh [version] [amd64|arm64] [output dir]
#
# Needs Go, Docker (with buildx for another architecture) and, for the
# Helm chart archive, helm. Run it from a clone with network access; copy
# the resulting .tar.gz into the air-gapped network.
set -euo pipefail

VERSION=${1:-$(git describe --tags --always 2>/dev/null || echo dev)}
ARCH=${2:-$(go env GOARCH)}
OUT=${3:-dist/airgap}
ROOT=$(cd "$(dirname "$0")/../.." && pwd)
NAME=stampede-airgap-${VERSION}-linux-${ARCH}
STAGE=$(mktemp -d)/$NAME
DB_IMAGE=timescale/timescaledb:2.22.1-pg17

cd "$ROOT"
mkdir -p "$STAGE"/{bin,images,compose,helm,packs,docs}
echo "==> binaries"
LDFLAGS="-s -w -X github.com/Ivan825/Stampede/internal/version.Version=${VERSION}"
for os_arch in "linux/$ARCH" darwin/arm64 darwin/amd64 windows/amd64; do
  os=${os_arch%/*}; arch=${os_arch#*/}; ext=""; [ "$os" = windows ] && ext=.exe
  CGO_ENABLED=0 GOOS=$os GOARCH=$arch go build -trimpath -ldflags "$LDFLAGS" \
    -o "$STAGE/bin/stampede-${os}-${arch}${ext}" ./cmd/stampede
done

echo "==> images"
docker buildx build --platform "linux/$ARCH" --build-arg VERSION="$VERSION" --load \
  -t "ghcr.io/ivan825/stampede:$VERSION" .
docker buildx build --platform "linux/$ARCH" --build-arg VERSION="$VERSION" --load \
  -f deploy/docker/Dockerfile.browser-worker -t "ghcr.io/ivan825/stampede-browser-worker:$VERSION" .
docker pull --platform "linux/$ARCH" "$DB_IMAGE"
docker save "ghcr.io/ivan825/stampede:$VERSION" "ghcr.io/ivan825/stampede-browser-worker:$VERSION" "$DB_IMAGE" \
  | gzip > "$STAGE/images/images.tar.gz"
printf '%s\n' "ghcr.io/ivan825/stampede:$VERSION" "ghcr.io/ivan825/stampede-browser-worker:$VERSION" "$DB_IMAGE" \
  > "$STAGE/images/images.txt"

echo "==> compose, chart, packs, docs"
sed "s/__VERSION__/$VERSION/g" deploy/airgap/docker-compose.yml > "$STAGE/compose/docker-compose.yml"
cp -R deploy/helm/stampede "$STAGE/helm/stampede"
sed "s/__VERSION__/$VERSION/g" deploy/airgap/values-airgap.yaml > "$STAGE/helm/values-airgap.yaml"
if command -v helm >/dev/null; then helm package "$STAGE/helm/stampede" -d "$STAGE/helm" >/dev/null; fi
cp -R packs/. "$STAGE/packs/"
cp -R docs/. "$STAGE/docs/"
cp README.md LICENSE NOTICE "$STAGE/"
cp deploy/airgap/install.sh deploy/airgap/README.md "$STAGE/"
echo "$VERSION" > "$STAGE/VERSION"

(cd "$STAGE" && find . -type f ! -name SHA256SUMS -print0 | sort -z | xargs -0 shasum -a 256 > SHA256SUMS)
mkdir -p "$OUT"
tar -C "$(dirname "$STAGE")" -czf "$OUT/$NAME.tar.gz" "$NAME"
(cd "$OUT" && shasum -a 256 "$NAME.tar.gz" > "$NAME.tar.gz.sha256")
echo "==> $OUT/$NAME.tar.gz"
