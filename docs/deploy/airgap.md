# Air-gapped install

For a network with no internet access, Stampede ships as one offline
bundle per Linux architecture. It holds the CLI for Linux, macOS and
Windows, the container images (server and worker, the browser worker with
Chromium, and TimescaleDB), a Compose stack that never pulls, the Helm
chart with values for a private registry, the product packs, the docs, and
a checksum for every file.

```sh
# where there is network access
deploy/airgap/build-bundle.sh v1.0.0 amd64          # or arm64

# inside the air-gapped network
tar xzf stampede-airgap-v1.0.0-linux-amd64.tar.gz && cd stampede-airgap-*
./install.sh                                         # verify checksums, load images
export STAMPEDE_JOIN_TOKEN=$(openssl rand -hex 24)
docker compose -f compose/docker-compose.yml up -d   # http://localhost:8080
```

For Kubernetes, `./install.sh --registry registry.internal:5000` also
pushes the images to your registry, and `helm/values-airgap.yaml` points
the chart at it. The bundle's
[README](../../deploy/airgap/README.md) has every step, including plugins
and using a model served inside the network for the optional AI features.

Releases attach a bundle for amd64 and arm64. A nightly CI job builds a
bundle, deletes the images it contains, installs from the bundle alone and
checks that the server is ready and both workers connect.
