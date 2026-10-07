## stampede runs workers

Show the health of the load generators running a run

### Synopsis

Each worker's latest self-monitoring while the run executes: CPU,
scheduling lag, whether it reports itself saturated (and so may be what
limits the measured load) and its last heartbeat. Empty once the run has
ended.

```
stampede runs workers <run> [flags]
```

### Options

```
  -h, --help   help for workers
      --json   print JSON for scripting
```

### Options inherited from parent commands

```
      --project string   project name, slug or id (default: the default or only project)
```

### SEE ALSO

* [stampede runs](stampede_runs.md)	 - List runs on the server, and show a run's details, events, timeline and workers

