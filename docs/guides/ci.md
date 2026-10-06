# CI integration

`stampede run` needs no server, so it fits any CI system.

```yaml
# .github/workflows/perf.yml
name: performance
on: [pull_request]
jobs:
  smoke:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with: { go-version: "1.27" }
      - run: go install github.com/Ivan825/Stampede/cmd/stampede@latest
      - name: start the app under test
        run: docker compose -f deploy/test.yml up -d --wait
      - name: load test
        run: |
          stampede run perf/checkout.yaml -e TARGET_URL=http://localhost:8080 \
            --shape smoke --junit junit.xml --md summary.md -o report.html
      - if: always()
        run: cat summary.md >> "$GITHUB_STEP_SUMMARY"
      - if: always()
        uses: actions/upload-artifact@v4
        with: { name: load-report, path: "report.html\njunit.xml" }
```

| Exit code | Meaning |
|---|---|
| 0 | every target passed (or none set) |
| 3 | a target failed |
| 4 | `stampede compare` found a regression |
| 1 | any other error |

`--junit` writes one test case per target, which most CI systems display
natively. `--md` writes a summary suitable for a pull request comment.

To run against a shared Stampede server instead (for distributed load),
create an API token in the web UI (Settings → API tokens, role `runner`)
and use:

```sh
export STAMPEDE_SERVER=https://stampede.example.com STAMPEDE_TOKEN=stp_...
stampede start --project shop --file perf/checkout.yaml --target staging --md summary.md
```

## GitHub Action

The repository ships a composite action in [`action/`](../../action/action.yml).
It installs `stampede`, runs a scenario, appends the Markdown summary to the
job summary, uploads the HTML, JSON, JUnit and Markdown reports as an
artifact, sets outputs and fails the step when a target fails (exit code 3).

```yaml
# .github/workflows/perf.yml
name: performance
on: [pull_request]
jobs:
  smoke:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - name: start the app under test
        run: docker compose -f deploy/test.yml up -d --wait
      - id: load
        uses: Ivan825/Stampede/action@main   # @v1 once a v1 tag is published
        with:
          scenario: perf/checkout.yaml
          target-url: http://localhost:8080
          shape: smoke
      - run: echo "p95 was ${{ steps.load.outputs.p95 }}s (${{ steps.load.outputs.verdict }})"
```

`uses: Ivan825/Stampede/action@v1` will work once a `v1` tag is published;
no release exists yet, so use `@main` (or pin a commit SHA) until then.

| Input | Default | Meaning |
|---|---|---|
| `scenario` | (required) | path to the scenario file |
| `target-url` | | overrides `target.baseURL` (`--base-url`) |
| `shape`, `rate`, `vus`, `duration` | | the matching `stampede run` overrides |
| `extra-args` | | more `stampede run` arguments, split on spaces, e.g. `-e TOKEN=abc` |
| `version` | `latest` | `latest`; a release tag such as `v1.2.3` (downloads the release archive for the runner's OS and architecture and checks it against the release checksums); any other git ref such as `main` or a SHA (`go install ...@ref`, with `actions/setup-go`); or `local` to build the Stampede repository checked out in the workspace. While no release is published, `latest` builds the latest commit. |
| `output-dir` | `stampede-report` | where the reports are written |
| `artifact-name` | `stampede-report` | name of the uploaded artifact; empty skips the upload |
| `fail-on-target-failure` | `true` | fail the step when a target fails |

| Output | Meaning |
|---|---|
| `verdict` | `pass`, `fail`, `generator-limited` or `no-targets` |
| `p95` | overall p95 latency in seconds |
| `error-rate` | share of failed requests, 0 to 1 |
| `exit-code` | exit code of `stampede run` |
| `report-dir` | directory holding `report.html`, `report.json`, `junit.xml`, `summary.md` |

The action is exercised in this repository's CI (the `action` job) with
`version: local` against `bench/echoserver`: a passing run must set its
outputs and a failing target must fail the step. Downloading a release
binary has not been exercised yet because no release has been published.
