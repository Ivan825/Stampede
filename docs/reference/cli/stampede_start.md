## stampede start

Start a run on a Stampede server and follow it live

### Synopsis

Start a run on the server you signed in to with stampede login. Pass a
scenario already saved on the server (--scenario) or a local file (--file),
which is saved as a new version first. Exits like stampede run: 0 pass,
3 targets failed.

```
stampede start [flags]
```

### Examples

```
  stampede start --project shop --file checkout.yaml --target staging --shape spike
  stampede start --scenario shoplab-mix --target shoplab --duration 2m --detach
  stampede start --scenario checkout --target staging --region mumbai=50% --region frankfurt=30% --region virginia=20%
```

### Options

```
  -d, --detach               print the run id and return without following
      --duration string      duration override
  -e, --env stringArray      KEY=VALUE for ${env.KEY} (repeatable)
  -f, --file string          local scenario file to save as a new version and run
  -h, --help                 help for start
      --max string           shape max level override
      --md string            write the report as Markdown when done (- for stdout)
      --note string          note recorded with the run
      --project string       project name, slug or id (default: the only project)
      --rate string          arrival rate override, e.g. 100/s
      --region stringArray   split the load by worker region, REGION=PERCENT (repeatable, adds up to 100%), e.g. --region mumbai=50% --region frankfurt=50%; replaces load.regions
      --scenario string      scenario name or id on the server
      --shape string         traffic shape override
      --start string         shape start level override
      --target string        target name, base URL or id (default: the only target)
      --vus int              virtual users override
      --workers int          number of workers (0 = all)
```

### SEE ALSO

* [stampede](stampede.md)	 - Self-hosted, distributed load testing

