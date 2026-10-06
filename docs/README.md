# Stampede documentation

Stampede describes how your users behave, checks each journey with a single
user, then runs many of them at once and tells you where your product breaks.
Everything here describes what the repository does today; features that are
not built yet are marked **planned**.

## Start

- [Quick start](quickstart.md): ShopLab, a first run, a report, a breakpoint, in about five minutes
- [Installation](install.md): binary, Docker Compose, Kubernetes
- [Your first real target](guides/first-target.md)

## Concepts

- [Scenarios and journeys](concepts/scenarios.md)
- [Open and closed load models](concepts/load-models.md)
- [How Stampede measures](concepts/measurement.md): coordinated omission, histograms, saturation

## Guides

- [Test types](guides/test-types.md): every traffic shape and the targeted stresses
- [Reading a report](guides/reports.md)
- [Comparing releases](guides/comparing.md)
- [CI integration](guides/ci.md)
- [AI journey generation](ai.md)
- [Product packs](guides/packs.md): using and writing them
- [Protocols](protocols.md): HTTP/1.1, HTTP/2, GraphQL, WebSocket, SSE, gRPC

## Operate

- [Docker Compose](deploy/compose.md), [Helm](deploy/helm.md), [operator](deploy/operator.md), [Terraform workers](deploy/terraform.md)
- [Upgrades, backups and restore](deploy/upgrades.md)
- [Safety](safety.md): ownership checks, caps, kill switch
- [Configuration](reference/configuration.md)

## Reference

- [Scenario format](reference/scenario.md) and its [JSON Schema](../schema/scenario.schema.json)
- [CLI](reference/cli/stampede.md) (generated from the binary)
- [REST API](../api/openapi.yaml) (OpenAPI; also served at `/api/v1/openapi.yaml`)

## Project

- [Architecture](architecture.md)
- [Troubleshooting](troubleshooting.md)
- [Contributing](../CONTRIBUTING.md), [Security policy](../SECURITY.md)
