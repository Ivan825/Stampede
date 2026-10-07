## stampede ai jobs show

Show a job's status, journeys and problems, or its proposal

### Synopsis

Show a job. --wait follows it until it finishes; --yaml prints only the
proposed scenario and --diff only its diff against the scenario it was
compared with. --json includes the dry-run traces.

```
stampede ai jobs show <job> [flags]
```

### Options

```
      --diff   print only the diff against the scenario it was compared with
  -h, --help   help for show
      --json   print JSON for scripting
      --wait   wait for the job to finish
      --yaml   print only the proposed scenario
```

### Options inherited from parent commands

```
      --project string   project name, slug or id (default: the default or only project)
```

### SEE ALSO

* [stampede ai jobs](stampede_ai_jobs.md)	 - Start, follow and approve AI journey generation jobs

