## stampede validate

Check scenario files without running them

### Synopsis

Checks scenario files against the scenario format: structure, templates,
expressions, extractors and that variables are defined before use. Plugin
steps are also checked against the schemas of the plugins installed on this
machine; a plugin that is not installed here is reported, and its steps'
settings are checked when a run starts on a worker that has it.

With --dry-run, each valid file's journeys then run once each, with one
user, against the target (set it as for stampede run: -e or --base-url),
and each journey is reported as passing or failing with its first error.
Unique data is read in order rather than used up. The target's safety
rules apply as for a run. Replay scenarios are checked statically only.

```
stampede validate <scenario.yaml>... [flags]
```

### Examples

```
  stampede validate checkout.yaml
  stampede validate journeys/*.yaml --dry-run -e TARGET_URL=http://localhost:8090
```

### Options

```
      --allow-host strings   with --dry-run: extra public hosts requests may reach besides the target
      --base-url string      with --dry-run: override target.baseURL
      --dry-run              also run each journey once with one user against the target
  -e, --env stringArray      with --dry-run: set ${env.KEY} (KEY=VALUE, repeatable)
  -h, --help                 help for validate
      --timeout duration     with --dry-run: time allowed for each file's journeys, think times included (default 2m0s)
```

### SEE ALSO

* [stampede](stampede.md)	 - Self-hosted, distributed load testing

