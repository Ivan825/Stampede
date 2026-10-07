## stampede schedules update

Change a schedule; only the flags given change

### Synopsis

Change any of a schedule's settings. Only the flags given change: load
overrides are merged into the current ones (--clear-overrides drops them
first), --env adds or replaces variables (--unset-env and --clear-env
remove them). The schedule is checked in full again, as when it was
created, and you become its owner, so later runs start as you.

```
stampede schedules update <schedule> [flags]
```

### Examples

```
  stampede schedules update nightly --cron "0 3 * * *" --timezone Europe/Berlin
  stampede schedules update nightly --scenario checkout --duration 10m --env STAGE=ci
  stampede schedules update api-drift --spec-url https://staging.example.com/openapi.json
```

### Options

```
      --clear-env               remove every environment variable (before adding any given)
      --clear-overrides         drop the current overrides (before applying any given)
      --cron string             five-field cron expression, e.g. "0 2 * * *", or @hourly, @daily, @weekly
      --duration string         duration override
      --enabled                 enable (--enabled) or disable (--enabled=false) the schedule (default true)
  -e, --env stringArray         add or replace KEY=VALUE for ${env.KEY} (repeatable)
  -h, --help                    help for update
      --json                    print JSON for scripting
      --max string              shape max level override
      --name string             rename the schedule
      --note string             description of the schedule
      --rate string             arrival rate override, e.g. 100/s
      --region stringArray      split the load by worker region, REGION=PERCENT (repeatable, adds up to 100%), e.g. --region mumbai=50% --region frankfurt=50%; replaces load.regions (replaces the regions)
      --scenario string         run this scenario (name or id) instead
      --shape string            traffic shape override
      --spec-url string         drift schedules: OpenAPI document compared with the scenario on each check ("" removes it)
      --start string            shape start level override
      --target string           run against this target (name, base URL or id) instead
      --timezone string         IANA time zone for the cron expression
      --unset-env stringArray   remove an environment variable (repeatable)
      --vus int                 virtual users override
      --workers int             number of workers (0 = all)
```

### Options inherited from parent commands

```
      --project string   project name, slug or id (default: the only project)
```

### SEE ALSO

* [stampede schedules](stampede_schedules.md)	 - List and manage scheduled runs on the server

