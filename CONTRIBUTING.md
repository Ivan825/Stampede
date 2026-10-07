# Contributing to Stampede

Thanks for helping. Stampede aims to be a load tester whose numbers people
can trust, so correctness and clear behaviour come before features.

## Ground rules

- **Numbers first.** Any change to the engine, metrics or reports must keep
  `go run ./bench/accuracy -quick` passing and should add a test that would
  catch a regression.
- **Honest docs.** The README and website list only what works in the
  repository. New, unfinished work is labelled *planned*.
- **Small commits** using [Conventional Commits](https://www.conventionalcommits.org):
  `feat(engine): ...`, `fix(report): ...`, `docs: ...`, `test: ...`.
  The changelog and version bumps are generated from them.

## Development

```sh
make build      # bin/stampede
make test       # unit and integration tests
make race       # tests with the race detector
make lint       # golangci-lint
```

Layout:

| Path | What lives there |
|---|---|
| `cmd/stampede` | the single binary |
| `internal/scenario` | scenario types, parser, validation, CEL templating, load plans |
| `internal/engine` | virtual users, executors, feeders |
| `internal/protocol/httpx` | HTTP driver and phase timing |
| `internal/metrics` | histograms, snapshots, collector |
| `internal/report` | verdicts, targets, exports |
| `internal/runner` | in-process runs for `stampede run` |
| `internal/safety` | target classification, ownership checks, caps |
| `bench` | calibrated echo server and accuracy benchmark |

## Pull requests

1. Open an issue first for anything larger than a bug fix.
2. Keep each pull request focused; include tests.
3. CI must be green: gofmt, vet, golangci-lint, tests on Linux and macOS,
   cross-compilation, quick accuracy run and govulncheck.

## Dependencies

Dependencies are updated by hand, not by a bot: `make update` updates the Go
modules in every module of the repository and runs govulncheck; bump
GitHub Actions versions in `.github/workflows` and `action/action.yml` at
the same time. CI then checks everything as usual.

By contributing you agree that your contributions are licensed under the
Apache License 2.0.
