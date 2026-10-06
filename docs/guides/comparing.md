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

## Tips

- Discard a warm-up run. The first run after a deploy or a restart often
  has slower tails while caches and pools fill.
- Keep the load and the generator setup identical between versions.
- Use `--pause` so one run's effects (queues, caches, GC) settle before the next.
