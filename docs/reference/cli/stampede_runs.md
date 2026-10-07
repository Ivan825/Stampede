## stampede runs

List runs on the server, and show a run's details, events, timeline and workers

### Synopsis

With no subcommand, list a project's recent runs (as stampede runs list).
Runs are named by id or by a unique start of it, as printed by the list.
stampede report <run> prints or downloads a finished run's report.

```
stampede runs [flags]
```

### Options

```
  -h, --help              help for runs
      --json              print JSON for scripting
      --limit int         how many runs (at most 200) (default 20)
      --project string    project name, slug or id (default: the default or only project)
      --scenario string   only runs of this scenario
```

### SEE ALSO

* [stampede](stampede.md)	 - Self-hosted, distributed load testing
* [stampede runs events](stampede_runs_events.md)	 - List a run's events: workers, safety stops, dry runs
* [stampede runs follow](stampede_runs_follow.md)	 - Follow a run live, then print its report
* [stampede runs list](stampede_runs_list.md)	 - List a project's recent runs, newest first
* [stampede runs show](stampede_runs_show.md)	 - Show a run's status, load, summary and outcome
* [stampede runs timeline](stampede_runs_timeline.md)	 - Print a run's metrics over time
* [stampede runs workers](stampede_runs_workers.md)	 - Show the health of the load generators running a run

