## stampede drift show

Show one scheduled drift check: each journey's dry run and the spec diff

### Synopsis

Show a drift check run by a drift schedule. --json includes each journey's
redacted dry-run traces.

```
stampede drift show <check> [flags]
```

### Options

```
  -h, --help             help for show
      --json             print JSON for scripting
      --project string   project name, slug or id (default: the default or only project)
```

### SEE ALSO

* [stampede drift](stampede_drift.md)	 - Find journeys an API change broke

