## stampede projects

List and manage projects, their settings and per-project roles

### Synopsis

A project holds targets, scenarios, runs, schedules and secrets. Commands
that work in a project take --project (a name, slug or id); without it
they use the default project set with stampede projects use (or
STAMPEDE_PROJECT), or the only project when there is just one.

### Options

```
  -h, --help   help for projects
```

### SEE ALSO

* [stampede](stampede.md)	 - Self-hosted, distributed load testing
* [stampede projects create](stampede_projects_create.md)	 - Create a project
* [stampede projects delete](stampede_projects_delete.md)	 - Delete a project with everything in it
* [stampede projects list](stampede_projects_list.md)	 - List projects
* [stampede projects roles](stampede_projects_roles.md)	 - Give members a different role in one project
* [stampede projects settings](stampede_projects_settings.md)	 - Show or change a project's caps and dry-run gate
* [stampede projects show](stampede_projects_show.md)	 - Show a project with its targets, scenarios and schedules
* [stampede projects update](stampede_projects_update.md)	 - Rename a project or change its description
* [stampede projects use](stampede_projects_use.md)	 - Set the default project for commands given no --project

