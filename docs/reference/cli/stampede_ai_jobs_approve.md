## stampede ai jobs approve

Save a finished job's proposal as a scenario or a new version

### Synopsis

Save the proposal of a finished job: as a new version of --scenario
(default: the scenario the job was compared with), otherwise as a new
scenario. Jobs with flagged journeys need --allow-unvalidated. Needs the
editor role and is audited.

```
stampede ai jobs approve <job> [flags]
```

### Options

```
      --allow-unvalidated   approve even though some journeys are flagged
  -h, --help                help for approve
      --json                print JSON for scripting
  -m, --message string      version message
      --scenario string     save as a new version of this scenario
```

### Options inherited from parent commands

```
      --project string   project name, slug or id (default: the default or only project)
```

### SEE ALSO

* [stampede ai jobs](stampede_ai_jobs.md)	 - Start, follow and approve AI journey generation jobs

