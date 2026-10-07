## stampede targets update

Change a target's name, base URL, allowed hosts or caps

### Synopsis

Change a target. Only the flags given change; --allow-host replaces the
list of allowed hosts (--allow-host "" empties it), and a cap of 0 removes
that cap. Changing the base URL's host clears its verification.

```
stampede targets update <target> [flags]
```

### Examples

```
  stampede targets update staging --max-rate 200 --allow-host cdn.example.com --allow-host auth.example.com
```

### Options

```
      --allow-host strings      hosts the target's requests may reach besides its own (repeatable; replaces the list)
      --base-url string         new base URL
  -h, --help                    help for update
      --json                    print JSON for scripting
      --max-duration duration   cap on a run's duration, e.g. 30m (0 removes it)
      --max-rate float          cap on the arrival rate, iterations per second (0 removes it)
      --max-vus int             cap on virtual users (0 removes it)
      --name string             new name
```

### Options inherited from parent commands

```
      --project string   project name, slug or id (default: the default or only project)
```

### SEE ALSO

* [stampede targets](stampede_targets.md)	 - List and manage a project's targets, and prove you own them

