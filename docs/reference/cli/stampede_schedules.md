## stampede schedules

List and manage scheduled runs on the server

### Synopsis

Schedules start a run of a saved scenario against a target on a cron
expression, such as "0 2 * * *" for 02:00 every day. Times are UTC unless
--timezone names an IANA zone. Runs start as the schedule's owner: whoever
created or last changed it. Creating, changing and deleting schedules
needs the editor role; starting one by hand needs runner.

### Options

```
  -h, --help             help for schedules
      --project string   project name, slug or id (default: the only project)
```

### SEE ALSO

* [stampede](stampede.md)	 - Self-hosted, distributed load testing
* [stampede schedules create](stampede_schedules_create.md)	 - Create a schedule
* [stampede schedules delete](stampede_schedules_delete.md)	 - Delete a schedule (the runs it started are kept)
* [stampede schedules disable](stampede_schedules_disable.md)	 - Stop a schedule firing until it is enabled again
* [stampede schedules enable](stampede_schedules_enable.md)	 - Let a schedule fire again (you become its owner)
* [stampede schedules list](stampede_schedules_list.md)	 - List a project's schedules
* [stampede schedules run](stampede_schedules_run.md)	 - Start a schedule's run now, as you

