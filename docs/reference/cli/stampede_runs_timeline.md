## stampede runs timeline

Print a run's metrics over time

### Synopsis

Print a run's points: per second by default, or rolled up per 10s or 1m
(requests summed, rps averaged, p50 the mean and p95/p99 the worst
per-second value in each bucket). Rollups outlast the per-second points
when the server keeps metrics for a limited time.

```
stampede runs timeline <run> [flags]
```

### Examples

```
  stampede runs timeline 3f2a91c0 --resolution 10s
```

### Options

```
  -h, --help                help for timeline
      --json                print JSON for scripting
      --resolution string   1s, 10s or 1m (default "1s")
```

### Options inherited from parent commands

```
      --project string   project name, slug or id (default: the default or only project)
```

### SEE ALSO

* [stampede runs](stampede_runs.md)	 - List runs on the server, and show a run's details, events, timeline and workers

