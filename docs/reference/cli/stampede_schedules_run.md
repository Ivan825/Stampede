## stampede schedules run

Start a schedule's run now, as you

### Synopsis

Start a schedule's run now, without changing when it next fires. Prints
the run id; with --follow, follows it live and exits like stampede start.

```
stampede schedules run <schedule> [flags]
```

### Options

```
  -f, --follow      follow the run live and print its report
  -h, --help        help for run
      --md string   with --follow, write the report as Markdown when done (- for stdout)
```

### Options inherited from parent commands

```
      --project string   project name, slug or id (default: the only project)
```

### SEE ALSO

* [stampede schedules](stampede_schedules.md)	 - List and manage scheduled runs on the server

