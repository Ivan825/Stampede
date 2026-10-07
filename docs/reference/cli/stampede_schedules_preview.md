## stampede schedules preview

Show the next times a cron expression fires

```
stampede schedules preview [flags]
```

### Examples

```
  stampede schedules preview --cron "30 6 * * MON-FRI" --timezone Europe/London --count 5
```

### Options

```
      --count int         how many firings, 1 to 20 (default 3)
      --cron string       cron expression, e.g. "0 2 * * *" or @daily
  -h, --help              help for preview
      --json              print JSON for scripting
      --timezone string   IANA time zone (default UTC)
```

### Options inherited from parent commands

```
      --project string   project name, slug or id (default: the only project)
```

### SEE ALSO

* [stampede schedules](stampede_schedules.md)	 - List and manage scheduled runs on the server

