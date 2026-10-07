## stampede drift

Find journeys an API change broke

### Synopsis

Check a saved scenario against the current version of an API, so journeys that
broke after an API change are found before the next load test:

  - with --previous-openapi (or the other --previous-* flags), endpoints
    removed since the previous version and the journeys that call them;
  - requests that use no endpoint of the current API;
  - with --target, a dry run of every journey with one user (real requests,
    as in stampede generate), reporting the journeys that now fail.

Exit code 4 when something drifted. To repair, regenerate with the scenario
as the starting point: stampede generate --from-openapi new.yaml
--diff-against scenario.yaml --target URL -o scenario.yaml.

With --scenario, the scenario saved on the server is checked by the
server: the API comes from --from-openapi or --spec-url (and
--previous-openapi or --previous-spec-url), and --target names one of the
project's targets for the dry run.

The subcommands read the checks that drift schedules run on the server
(stampede schedules create --kind drift): drift results lists them, drift
show prints one, and drift repair asks the AI generator to fix the broken
journeys. A scenario file named like a subcommand needs a path, such as
./results.

```
stampede drift [scenario.yaml] [flags]
```

### Examples

```
  stampede drift shop.yaml --from-openapi v2.yaml --previous-openapi v1.yaml
  stampede drift shop.yaml --from-openapi openapi.yaml --target http://staging:8080
  stampede drift --scenario checkout --spec-url https://staging.example.com/openapi.json --target staging
  stampede drift results --project shop
  stampede drift repair 7c1e0a2b
```

### Options

```
      --allow-host strings         extra public hosts the dry run may reach
  -e, --env stringArray            set ${env.KEY} for the dry run (KEY=VALUE, repeatable)
      --from-graphql string        the current API as a GraphQL schema (SDL or introspection JSON)
      --from-har string            the current API as seen in a HAR recording
      --from-log string            the current API as seen in an access log
      --from-openapi string        the current API as an OpenAPI 3.x spec (YAML or JSON)
      --graphql-path string        path of the GraphQL API (default "/graphql")
  -h, --help                       help for drift
      --json                       print JSON
      --no-dry-run                 skip the dry run even with --target
      --previous-graphql string    the previous GraphQL schema
      --previous-har string        a HAR recording of the previous version
      --previous-log string        an access log of the previous version
      --previous-openapi string    the previous OpenAPI spec, to list removed endpoints
      --previous-spec-url string   with --scenario: have the server fetch the previous OpenAPI document from this URL
      --project string             with --scenario: project name, slug or id (default: the default or only project)
      --scenario string            check a scenario saved on the server (name or id) instead of a file
      --spec-url string            with --scenario: have the server fetch the OpenAPI document from this URL on a target's host
      --target string              dry-run every journey once against this URL (sends real requests); with --scenario, a target of the project (name, base URL or id)
      --version int                with --scenario: the scenario version to check (default the latest)
```

### SEE ALSO

* [stampede](stampede.md)	 - Self-hosted, distributed load testing
* [stampede drift repair](stampede_drift_repair.md)	 - Ask the AI generator to repair the journeys a drift check found broken
* [stampede drift results](stampede_drift_results.md)	 - List the results of a project's scheduled drift checks, newest first
* [stampede drift show](stampede_drift_show.md)	 - Show one scheduled drift check: each journey's dry run and the spec diff

