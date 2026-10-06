## stampede validate

Check scenario files without running them

### Synopsis

Checks scenario files against the scenario format: structure, templates,
expressions, extractors and that variables are defined before use. Plugin
steps are also checked against the schemas of the plugins installed on this
machine; a plugin that is not installed here is reported, and its steps'
settings are checked when a run starts on a worker that has it.

```
stampede validate <scenario.yaml>... [flags]
```

### Options

```
  -h, --help   help for validate
```

### SEE ALSO

* [stampede](stampede.md)	 - Self-hosted, distributed load testing

