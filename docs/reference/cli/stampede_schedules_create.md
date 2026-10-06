## stampede schedules create

Create a schedule

```
stampede schedules create <name> [flags]
```

### Examples

```
  stampede schedules create nightly --scenario checkout --target staging --cron "0 2 * * *"
  stampede schedules create weekday-soak --scenario browse --target staging \
    --cron "30 6 * * MON-FRI" --timezone Europe/London --shape soak --duration 30m
```

### Options

```
      --cron string       five-field cron expression, e.g. "0 2 * * *", or @hourly, @daily, @weekly
      --disabled          create it disabled
      --duration string   duration override
  -e, --env stringArray   KEY=VALUE for ${env.KEY} (repeatable; stored with the schedule, so use secrets for sensitive values)
  -h, --help              help for create
      --max string        shape max level override
      --note string       description of the schedule
      --rate string       arrival rate override, e.g. 100/s
      --scenario string   scenario name or id on the server
      --shape string      traffic shape override
      --start string      shape start level override
      --target string     target name, base URL or id (default: the only target)
      --timezone string   IANA time zone for the cron expression (default UTC)
      --vus int           virtual users override
      --workers int       number of workers (0 = all)
```

### Options inherited from parent commands

```
      --project string   project name, slug or id (default: the only project)
```

### SEE ALSO

* [stampede schedules](stampede_schedules.md)	 - List and manage scheduled runs on the server

