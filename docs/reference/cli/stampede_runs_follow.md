## stampede runs follow

Follow a run live, then print its report

### Synopsis

Follow a running run's live progress, as stampede start does, then print
its report. Ctrl-C stops following; the run continues. Exits like
stampede start: 0 pass, 3 targets failed. For a run that has already
finished, prints the report straight away.

```
stampede runs follow <run> [flags]
```

### Options

```
  -h, --help        help for follow
      --md string   write the report as Markdown when done (- for stdout)
```

### Options inherited from parent commands

```
      --project string   project name, slug or id (default: the default or only project)
```

### SEE ALSO

* [stampede runs](stampede_runs.md)	 - List runs on the server, and show a run's details, events, timeline and workers

