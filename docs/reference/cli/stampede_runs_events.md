## stampede runs events

List a run's events: workers, safety stops, dry runs

### Synopsis

Events recorded for a run, oldest first: workers joining or being lost,
safety stops, breakpoint confirmations and, when the project requires
one, the dry run before load.

```
stampede runs events <run> [flags]
```

### Options

```
  -h, --help   help for events
      --json   print JSON for scripting
```

### Options inherited from parent commands

```
      --project string   project name, slug or id (default: the default or only project)
```

### SEE ALSO

* [stampede runs](stampede_runs.md)	 - List runs on the server, and show a run's details, events, timeline and workers

