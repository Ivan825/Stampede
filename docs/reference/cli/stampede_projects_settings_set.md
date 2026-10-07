## stampede projects settings set

Change a project's caps or dry-run gate (admin)

```
stampede projects settings set [flags]
```

### Examples

```
  stampede projects settings set --project shop --max-rate 200 --max-duration 1h --require-dry-run
```

### Options

```
  -h, --help                    help for set
      --json                    print JSON for scripting
      --max-duration duration   cap on a run's duration, e.g. 30m (0 removes it)
      --max-rate float          cap on the arrival rate, iterations per second (0 removes it)
      --max-vus int             cap on virtual users (0 removes it)
      --require-dry-run         dry-run every journey before each run's load (--require-dry-run=false turns it off)
```

### Options inherited from parent commands

```
      --project string   project name, slug or id (default: the default or only project)
```

### SEE ALSO

* [stampede projects settings](stampede_projects_settings.md)	 - Show or change a project's caps and dry-run gate

