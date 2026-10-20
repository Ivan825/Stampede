# Contributing to Stampede

Thanks for helping. Stampede aims to be a load tester whose numbers people
can trust, so correctness and clear behaviour come before features.

## Start here

Issues labelled [good first
issue](https://github.com/Ivan825/Stampede/labels/good%20first%20issue)
are scoped for a first pull request. If none fits, these are always open:

- **Run the quick start on your machine.** Follow
  [docs/quickstart.md](docs/quickstart.md) exactly as written. Done when
  every command worked, or you have fixed the step that did not (or opened
  an issue with your OS, the command and its output).
- **Add a journey to a pack.** Pick a pack in `packs/`, add a journey that
  real users of that kind of product take, and extend its reference app in
  `examples/packlab` if the journey needs an endpoint it lacks. Done when
  its test passes, for example
  `go test ./examples/packlab -run TestPacksAgainstReferenceApps/ticketing`
  (the e-commerce pack is tested against `examples/shoplab` instead).
- **Add a regression fixture.** Find a scenario mistake that gives an
  unclear error (a typo'd field, a variable used before it is extracted)
  and add it to the parser's tests in `internal/scenario` with the error
  you would expect. Done when the test fails before your fix to the
  message and passes after.

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
