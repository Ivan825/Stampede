## stampede drift repair

Ask the AI generator to repair the journeys a drift check found broken

### Synopsis

Start an AI job that repairs the broken journeys of a drift check, with the
scenario as the starting point and the dry-run evidence (redacted) as the
task. Prints the job id; follow it with stampede ai jobs show <job> and
save the proposal with stampede ai jobs approve <job>. Nothing changes
until then. Needs an AI provider (stampede ai providers set) and the
editor role.

```
stampede drift repair <check> [flags]
```

### Options

```
  -h, --help              help for repair
      --json              print JSON for scripting
      --max-repairs int   repair rounds, 0 to 3 (default 3)
      --project string    project name, slug or id (default: the default or only project)
      --provider string   AI provider name or id (default: the only one, or the one named default)
      --wait              wait for the job to finish and show it
```

### SEE ALSO

* [stampede drift](stampede_drift.md)	 - Find journeys an API change broke

