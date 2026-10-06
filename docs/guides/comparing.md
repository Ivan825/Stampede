# Comparing releases

A single run is noisy. `stampede compare` uses repeated runs of each version.

```sh
stampede run checkout.yaml --repeat 3 --pause 30s --json v1.json   # v1-1.json, v1-2.json, v1-3.json
# deploy the new version
stampede run checkout.yaml --repeat 3 --pause 30s --json v2.json
stampede compare --label-a v1 --label-b v2 \
  --a v1-1.json,v1-2.json,v1-3.json --b v2-1.json,v2-2.json,v2-3.json --md comment.md
```

For p50, p95, p99, error rate, throughput and (for breakpoint runs) the
highest load that held, it reports both means, the relative change, a
bootstrap 95% confidence interval for the change, and the **noise floor**:
the largest spread between repeats of the same version (at least 2%).

A metric is a **regression** or **improvement** only when the interval
excludes zero *and* the change is bigger than the noise floor; for latency
it must also exceed 1 ms, the engine's measured accuracy. With fewer than
two runs per side, changes are **inconclusive**.

Runs with different scenarios, load plans, durations or worker counts, or
any generator-limited run, are flagged **not comparable**.

`compare` exits with 4 when there is a regression, for CI.

The JSON output (`--json`) also compares each request step's p95 and error
rate under `steps`. Step results do not change the verdict: with many steps,
some differ by chance. Use them to find which step a change comes from. A
change from zero (an error rate that rises from 0%) is written as `null` in
JSON and `+∞` in text.

## In the web UI

On a project's **Runs** page, tick the runs to compare and choose
**Compare…**. Only runs that finished with a report (completed or aborted)
can be selected, and selections are kept while you page through older runs.
The dialog puts each run in A (the baseline) or B:

- If the runs are of exactly two scenario versions, the older version is A
  and the sides are named after the versions (`v3`, `v4`).
- Otherwise the older half of the runs is A.

Change any run's side and the names, then choose **Compare**. The comparison
page shows the verdict (regression, improvement, no significant change,
inconclusive, or not comparable), a row per metric with both means, the
change, the 95% interval and the noise floor, the same figures for every
step, and warnings such as different worker counts. Hover a mean to see each
run's value. **Copy Markdown** copies the same Markdown that
`stampede compare --md` writes. The page address lists the runs, so it can be
shared with anyone in the organisation.

## Server API

`POST /api/v1/compare` (tag `runs`, any role) runs the same comparison on
runs stored on the server:

```sh
curl -s -X POST https://stampede.example.com/api/v1/compare \
  -H "Authorization: Bearer $STAMPEDE_TOKEN" -H 'Content-Type: application/json' \
  -d '{"a": ["<run id>", "<run id>", "<run id>"], "b": ["<run id>", "<run id>", "<run id>"],
       "labelA": "v1", "labelB": "v2"}'
```

Each side takes 1 to 20 run ids. Every run must belong to your organisation
and have finished with a report; otherwise the request fails with 422 and
lists each problem (not found, not finished, failed, listed twice). The
response has the fields of `stampede compare --json` (`a`, `b`,
`comparable`, `problems`, `metrics`, `steps`, `verdict`, `confidence`) plus
`markdown`, the Markdown summary. Labels default to `A` and `B`. See
[`api/openapi.yaml`](../../api/openapi.yaml) for the full schema.

## Tips

- Discard a warm-up run. The first run after a deploy or a restart often
  has slower tails while caches and pools fill.
- Keep the load and the generator setup identical between versions.
- Use `--pause` so one run's effects (queues, caches, GC) settle before the next.
