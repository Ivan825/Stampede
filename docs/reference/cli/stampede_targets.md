## stampede targets

List and manage a project's targets, and prove you own them

### Synopsis

A target is the system a project's runs send load to: a base URL, the
other hosts its requests may reach, and optional caps. Private and
loopback targets need no verification; public ones run under low caps
until you prove you own them (stampede targets verify). Targets are
named by name, base URL or id.

### Options

```
  -h, --help             help for targets
      --project string   project name, slug or id (default: the default or only project)
```

### SEE ALSO

* [stampede](stampede.md)	 - Self-hosted, distributed load testing
* [stampede targets create](stampede_targets_create.md)	 - Add a target to a project
* [stampede targets delete](stampede_targets_delete.md)	 - Delete a target
* [stampede targets list](stampede_targets_list.md)	 - List a project's targets
* [stampede targets show](stampede_targets_show.md)	 - Show a target, and how to verify it when it is public
* [stampede targets update](stampede_targets_update.md)	 - Change a target's name, base URL, allowed hosts or caps
* [stampede targets verify](stampede_targets_verify.md)	 - Prove you own a public target so higher load is allowed

