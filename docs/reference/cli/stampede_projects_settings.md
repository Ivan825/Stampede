## stampede projects settings

Show or change a project's caps and dry-run gate

### Synopsis

A project's caps bound every run in it, together with the server's, the
organisation's and the target's. With the dry-run gate on, every run first
runs each journey once with one user and fails before any load when a
journey fails. Changing them needs the admin role in the project.

### Options

```
  -h, --help             help for settings
      --project string   project name, slug or id (default: the default or only project)
```

### SEE ALSO

* [stampede projects](stampede_projects.md)	 - List and manage projects, their settings and per-project roles
* [stampede projects settings set](stampede_projects_settings_set.md)	 - Change a project's caps or dry-run gate (admin)
* [stampede projects settings show](stampede_projects_settings_show.md)	 - Show a project's caps and dry-run gate

