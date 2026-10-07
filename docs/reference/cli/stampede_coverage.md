## stampede coverage

Show which API endpoints a scenario's journeys exercise and which none does

### Synopsis

Map a scenario's requests onto the endpoints of an API: an OpenAPI spec, a
GraphQL schema, or the endpoints seen in a HAR recording or access log. The
result lists every endpoint with the journeys that call it, the endpoints
no journey touches, and requests that match no endpoint (often a typo or an
endpoint that was renamed). No model and no network access are needed.

With --scenario, the scenario saved on the server is checked by the
server, against an OpenAPI document given with --from-openapi or fetched
by the server from --spec-url (a URL on the host of one of the project's
targets).

```
stampede coverage [scenario.yaml] [flags]
```

### Examples

```
  stampede coverage shop.yaml --from-openapi openapi.yaml
  stampede coverage shop.yaml --from-log access.log --json
  stampede coverage --scenario checkout --spec-url https://staging.example.com/openapi.json
```

### Options

```
      --from-graphql string   the API as a GraphQL schema (SDL or introspection JSON)
      --from-har string       the API as seen in a HAR recording
      --from-log string       the API as seen in an access log
      --from-openapi string   the API as an OpenAPI 3.x spec (YAML or JSON)
      --graphql-path string   path of the GraphQL API (default "/graphql")
  -h, --help                  help for coverage
      --json                  print JSON
      --project string        with --scenario: project name, slug or id (default: the default or only project)
      --scenario string       check a scenario saved on the server (name or id) instead of a file
      --spec-url string       with --scenario: have the server fetch the OpenAPI document from this URL on a target's host
      --version int           with --scenario: the scenario version to check (default the latest)
```

### SEE ALSO

* [stampede](stampede.md)	 - Self-hosted, distributed load testing

