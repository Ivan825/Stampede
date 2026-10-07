## stampede ai jobs create

Start a job that generates a scenario (asynchronous)

### Synopsis

Start a generation job on the server. Give at least one input: a
description, an OpenAPI document, a HAR recording, an access log or .proto
files. Recorded traffic is redacted before it reaches the provider. With
--target, every journey is dry-run against that target and repaired; with
--scenario, the proposal is compared with that scenario and approving it
saves a new version. Prints the job id, or with --wait follows the job and
shows the result.

```
stampede ai jobs create [flags]
```

### Examples

```
  stampede ai jobs create --describe "people browse items, add one to the cart and check out" \
    --from-openapi openapi.yaml --target staging --wait
  stampede ai jobs create --from-har session.har --target staging --scenario checkout
```

### Options

```
      --describe string       plain-language description of your users and what they do
      --from-har string       HAR recording of real use
      --from-log string       web server access log, used to estimate the journey mix
      --from-openapi string   OpenAPI 3.x document (YAML or JSON)
  -h, --help                  help for create
      --json                  print JSON for scripting
      --max-repairs int       repair rounds before a failing journey is flagged for a human, 0 to 3 (default 3)
      --no-dry-run            skip the dry run and only check the scenario statically
      --proto stringArray     .proto file whose services become grpc steps (repeatable; imports name files by base name)
      --provider string       AI provider name or id (default: the only one, or the one named default)
      --scenario string       existing scenario to compare the proposal with (approving saves a new version)
      --target string         target to dry-run against (name, base URL or id); without it the scenario is only checked statically
      --wait                  wait for the job to finish and show it
```

### Options inherited from parent commands

```
      --project string   project name, slug or id (default: the default or only project)
```

### SEE ALSO

* [stampede ai jobs](stampede_ai_jobs.md)	 - Start, follow and approve AI journey generation jobs

