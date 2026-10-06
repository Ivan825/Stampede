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

```
stampede drift <scenario.yaml> [flags]
```

### Examples

```
  stampede drift shop.yaml --from-openapi v2.yaml --previous-openapi v1.yaml
  stampede drift shop.yaml --from-openapi openapi.yaml --target http://staging:8080
```

### Options

```
      --allow-host strings        extra public hosts the dry run may reach
  -e, --env stringArray           set ${env.KEY} for the dry run (KEY=VALUE, repeatable)
      --from-graphql string       the current API as a GraphQL schema (SDL or introspection JSON)
      --from-har string           the current API as seen in a HAR recording
      --from-log string           the current API as seen in an access log
      --from-openapi string       the current API as an OpenAPI 3.x spec (YAML or JSON)
      --graphql-path string       path of the GraphQL API (default "/graphql")
  -h, --help                      help for drift
      --json                      print JSON
      --no-dry-run                skip the dry run even with --target
      --previous-graphql string   the previous GraphQL schema
      --previous-har string       a HAR recording of the previous version
      --previous-log string       an access log of the previous version
      --previous-openapi string   the previous OpenAPI spec, to list removed endpoints
      --target string             dry-run every journey once against this URL (sends real requests)
```

### SEE ALSO

* [stampede](stampede.md)	 - Self-hosted, distributed load testing

