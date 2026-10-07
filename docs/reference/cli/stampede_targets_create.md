## stampede targets create

Add a target to a project

```
stampede targets create <name> [flags]
```

### Examples

```
  stampede targets create staging --base-url https://staging.example.com --allow-host cdn.example.com
  stampede targets create local --base-url http://localhost:8090 --max-rate 500
```

### Options

```
      --allow-host strings      another host the target's requests may reach (repeatable)
      --base-url string         the target's base URL (required)
  -h, --help                    help for create
      --json                    print JSON for scripting
      --max-duration duration   cap on a run's duration, e.g. 30m (0 removes it)
      --max-rate float          cap on the arrival rate, iterations per second (0 removes it)
      --max-vus int             cap on virtual users (0 removes it)
```

### Options inherited from parent commands

```
      --project string   project name, slug or id (default: the default or only project)
```

### SEE ALSO

* [stampede targets](stampede_targets.md)	 - List and manage a project's targets, and prove you own them

