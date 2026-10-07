## stampede scenarios

List, show and delete the scenarios saved on the server

### Synopsis

Scenarios are saved on the server with every version kept. stampede push
saves local files as new versions; these commands read them back.
Scenarios are named by name or id (or a unique start of the id).

### Options

```
  -h, --help             help for scenarios
      --project string   project name, slug or id (default: the default or only project)
```

### SEE ALSO

* [stampede](stampede.md)	 - Self-hosted, distributed load testing
* [stampede scenarios delete](stampede_scenarios_delete.md)	 - Delete a scenario with all its versions
* [stampede scenarios list](stampede_scenarios_list.md)	 - List a project's scenarios with their latest version
* [stampede scenarios show](stampede_scenarios_show.md)	 - Show a scenario, or print one version's YAML
* [stampede scenarios validate](stampede_scenarios_validate.md)	 - Check scenario files the way the server will
* [stampede scenarios versions](stampede_scenarios_versions.md)	 - List a scenario's versions, newest first

