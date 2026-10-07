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
create an API token with `stampede tokens create` (role `runner`) and
use:

```sh
export STAMPEDE_SERVER=https://stampede.example.com STAMPEDE_TOKEN=stp_...
stampede start --project shop --file perf/checkout.yaml --target staging --md summary.md
```

## GitLab CI

The app under test runs as a [service](https://docs.gitlab.com/ci/services/);
its alias is its host name. A service on the job's network has a private
address, so no ownership check applies (see [safety](../safety.md)).
GitLab reads the JUnit file into the merge request's test report.

```yaml
# .gitlab-ci.yml
load-test:
  stage: test
  image: golang:1.27
  services:
    - name: registry.example.com/shop:$CI_COMMIT_SHA
      alias: shop
  script:
    - go install github.com/Ivan825/Stampede/cmd/stampede@latest
    - stampede run perf/checkout.yaml -e TARGET_URL=http://shop:8080
        --shape smoke --junit junit.xml --md summary.md -o report.html
  artifacts:
    when: always
    paths: [report.html, summary.md]
    reports:
      junit: junit.xml
```

Exit code 3 fails the job when a target fails. To keep the job green and
only report, add `allow_failure: { exit_codes: [3] }`.

## Jenkins

A declarative pipeline in a Go container. The Go paths are moved into the
workspace because Jenkins runs the container as its own user, which cannot
write to the image's `/go`.

```groovy
// Jenkinsfile
pipeline {
  agent { docker { image 'golang:1.27' } }
  environment {
    GOPATH  = "${WORKSPACE}/.go"
    GOCACHE = "${WORKSPACE}/.cache/go-build"
    TARGET_URL = 'http://staging.internal:8080'
  }
  stages {
    stage('Install stampede') {
      steps { sh 'go install github.com/Ivan825/Stampede/cmd/stampede@latest' }
    }
    stage('Load test') {
      steps {
        sh '"$GOPATH/bin/stampede" run perf/checkout.yaml -e TARGET_URL="$TARGET_URL" --shape smoke --junit junit.xml --md summary.md -o report.html'
      }
    }
  }
  post {
    always {
      junit testResults: 'junit.xml', allowEmptyResults: true
      archiveArtifacts artifacts: 'report.html,summary.md', allowEmptyArchive: true
    }
  }
}
```

A failed target (exit code 3) fails the `sh` step and the build. To mark
the build unstable instead, wrap the step in
`catchError(buildResult: 'UNSTABLE', stageResult: 'UNSTABLE') { ... }`.

Neither example installs from a release yet: no release is published, so
both build the CLI with `go install`. Once releases exist, the one-line
installer (`curl -fsSL .../install.sh | sh`, see [installation](../install.md))
downloads the binary instead. These two examples are not run in this
repository's CI; the GitHub Action below is.

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
