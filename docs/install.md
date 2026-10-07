# Installation

| Option | Use it for | Guide |
|---|---|---|
| One-line installer | the CLI: `stampede run`, `init`, `compare`, the console | below |
| `stampede up` | the full stack (server, web UI, workers, database) from released images, no clone | below |
| `docker compose up` in a clone | the full stack plus the ShopLab demo | [deploy/compose.md](deploy/compose.md) |
| Release archives, Homebrew, Scoop | the same binary by hand or with a package manager | the GitHub releases page once v1.0 is tagged |
| Offline bundle | networks without internet access | [deploy/airgap.md](deploy/airgap.md) |
| Helm chart | Kubernetes | [deploy/helm.md](deploy/helm.md) |
| Operator | GitOps-style declared runs (alpha) | [deploy/operator.md](deploy/operator.md) |
| Terraform examples | workers in several cloud regions | [deploy/terraform.md](deploy/terraform.md) |

There is one binary. It is the CLI, the server (`stampede server`), a worker
(`stampede worker`) and the terminal console (`stampede` on its own).

## The CLI in one line

```sh
curl -fsSL https://raw.githubusercontent.com/Ivan825/Stampede/main/install.sh | sh        # Linux, macOS
irm https://raw.githubusercontent.com/Ivan825/Stampede/main/install.ps1 | iex             # Windows PowerShell
```

The script finds the latest release, downloads the archive for your OS and
CPU (amd64 or arm64), checks it against the release's SHA-256 checksums
and installs `stampede` into `~/.local/bin` (Windows:
`%LOCALAPPDATA%\stampede\bin`, added to your PATH). Read it first if you
prefer: it is short. Set `STAMPEDE_VERSION=v1.0.0` for a particular
release, `STAMPEDE_INSTALL_DIR` for another folder, or
`STAMPEDE_DOWNLOAD_BASE` to download from an internal mirror of the
release files. With no release published yet, it runs `go install`
instead, which needs Go 1.27 or later.

CI checks both scripts on Linux, macOS and Windows against a release built
from every commit, including that a tampered archive is refused.

## The full stack without a clone

```sh
stampede up      # needs Docker
stampede setup   # create the organisation and owner; then open http://localhost:8080
stampede down    # stop it (add --volumes to delete its data)
```

Outside a clone, `stampede up` writes a Compose file to your config
directory (`~/.config/stampede/stack` on Linux, `~/Library/Application
Support/stampede/stack` on macOS) and starts the released images of the
installed version: the server and web UI, two workers and TimescaleDB. Runs
reach an app on your own machine as `http://host.docker.internal:<port>`.
Inside a clone it uses the repository's `docker-compose.yml`, which also
starts ShopLab.

Everything you do to the server is done with the CLI; the web UI is read
only, for watching runs and reading reports, apart from the Stop, Kill and
Kill all safety controls.

## From source

```sh
git clone https://github.com/Ivan825/Stampede
cd Stampede
make build          # bin/stampede, with the web UI embedded
```

Requires Go 1.27 or later. Building the web UI from source needs Node 22 and
pnpm (`make web`); the repository includes a built copy, so `make build`
works without them.

## Server requirements

- PostgreSQL 15 or later. TimescaleDB is recommended and used by the Compose
  stack and Helm chart: per-second metrics become a compressed hypertable.
- `STAMPEDE_MASTER_KEY` (32 random bytes, base64; `stampede keygen` prints
  one) to store secrets and AI provider keys. Without it, those features are
  disabled.
- See [configuration](reference/configuration.md) for every setting.
