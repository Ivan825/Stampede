# Installation

| Option | Use it for | Guide |
|---|---|---|
| `go install github.com/Ivan825/Stampede/cmd/stampede@latest` | the CLI: `stampede run`, `init`, `compare` | below |
| Release archives and images | the same binary without Go | the GitHub releases page once v1.0 is tagged |
| `docker compose up` | everything on one machine | [deploy/compose.md](deploy/compose.md) |
| Helm chart | Kubernetes | [deploy/helm.md](deploy/helm.md) |
| Operator | GitOps-style declared runs (alpha) | [deploy/operator.md](deploy/operator.md) |
| Terraform examples | workers in several cloud regions | [deploy/terraform.md](deploy/terraform.md) |

There is one binary. It is the CLI, the server (`stampede server`), a worker
(`stampede worker`) and the terminal console (`stampede` on its own).

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
