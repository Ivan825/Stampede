# Serverless pack

Journeys, stresses and targets for apps that run as functions: cold
starts, warm instances, idle reclaiming and concurrency limits.

| File | What it tests |
|---|---|
| `journeys/app-mix.yaml` | Everyday traffic across five functions: follow a short link (a redirect, checked, not followed) 55%, shorten a link and try it 15%, link stats 15%, a link-preview image 10%, hello 5% |
| `stresses/burst-after-idle.yaml` | A trickle of 1/s, a burst to 150/s, two quiet minutes (long enough for idle instances to be reclaimed) and a second burst; warm requests under 50ms at the p50, cold starts in the p99 under 500ms |
| `stresses/concurrency-limit.yaml` | 20 users hammer a function reserved at five instances: each refusal must be a 429 with Retry-After and the client backs off; redirects (another function) must not be throttled |
| `targets.yaml` | Default targets: p95 under 300ms, under 1% errors, redirect p50 under 50ms and p99 under 500ms |

**Cold starts are in the tail.** A cold start happens when a request
finds no warm instance, so most requests are warm and the cold ones land
in the slowest few percent: hold the p50 for warm speed and the p99 for
cold starts. A burst after a quiet period is when they cluster. EdgeLab
marks each response with `X-Cold-Start` and `Server-Timing: init;dur=...`;
on AWS Lambda look for `Init Duration` in the logs, on other platforms
for their own cold-start metric.

The journeys follow EdgeLab: functions at `/r/{code}` (a 301 to the
long URL; links `go1000` to `go1999` exist), `POST /api/links`,
`/api/links/{code}/stats`, `/api/preview?url=` (reserved concurrency 5)
and `/api/hello`, and the platform's view at `/_platform/functions`
(instances, cold starts and throttles per function). Over a limit the
answer is 429 with `Retry-After: 1`. Every journey and stress is run
against [EdgeLab](../../examples/packlab/README.md#edgelab) in CI, and the
test checks that the concurrency stress really provokes 429s.

For your own app, change the paths to your functions' routes and pick
the function with the lowest concurrency limit for the concurrency
stress. Function platforms are detected by their response headers
(`Function-Execution-Id`, `X-Amzn-RequestId`, `X-Vercel-Id`,
`X-Nf-Request-Id`). Mind the bill: on pay-per-invocation platforms a load
test is charged like real traffic.

```sh
go run ./examples/packlab -product serverless        # EdgeLab on :8105
stampede init --target http://localhost:8105         # detects this pack
stampede run stampede/serverless/stresses/burst-after-idle.yaml -e TARGET_URL=http://localhost:8105
```
