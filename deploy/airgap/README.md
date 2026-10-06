# Stampede offline bundle

This archive installs Stampede on a network with no internet access. It
holds everything needed, and nothing is downloaded at install time:

| Path | Contents |
|---|---|
| `bin/` | the `stampede` CLI for Linux, macOS and Windows |
| `images/images.tar.gz` | container images: `stampede` (server and worker), `stampede-browser-worker` (with Chromium) and TimescaleDB |
| `compose/docker-compose.yml` | the stack for one machine; images are never pulled |
| `helm/` | the Helm chart (directory and `.tgz`) and `values-airgap.yaml` |
| `packs/`, `docs/` | product packs and the documentation |
| `SHA256SUMS` | a checksum for every file |

## Install

```sh
tar xzf stampede-airgap-<version>-linux-<arch>.tar.gz && cd stampede-airgap-*
./install.sh                     # verify checksums, load images into Docker
```

**One machine (Docker Compose):**

```sh
export STAMPEDE_JOIN_TOKEN=$(openssl rand -hex 24)
mkdir -p compose/data            # CSV/JSON feeder files for runs go here
docker compose -f compose/docker-compose.yml up -d
# with browser workers:  --profile browser
```

Open <http://localhost:8080>. `STAMPEDE_WORKERS` sets the number of
workers (default 2).

**Kubernetes:** push the images to your registry, then install the chart:

```sh
./install.sh --registry registry.internal:5000
helm install stampede helm/stampede -f helm/values-airgap.yaml \
  --set image.repository=registry.internal:5000/ivan825/stampede \
  --set timescaledb.image.repository=registry.internal:5000/timescale/timescaledb
```

**Plugins** are built from source (`stampede plugin install`), which needs
Go and the source tree. Build them where you made the bundle and copy them
into `STAMPEDE_PLUGIN_DIR` on each worker.

**AI features** are optional. In an air-gapped network, point them at a
model served inside it (Ollama or any OpenAI-compatible server) with
`--provider ollama --base-url http://gpu-box:11434`.

## Build a bundle

From a clone with network access:

```sh
deploy/airgap/build-bundle.sh v1.0.0 amd64      # or arm64
```

It writes `dist/airgap/stampede-airgap-<version>-linux-<arch>.tar.gz` and
its `.sha256`. Building for another architecture needs Docker buildx with
emulation.
