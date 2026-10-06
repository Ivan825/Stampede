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

A packaged GitHub Action (`uses: Ivan825/Stampede/action@v1`) is **planned**.
