## stampede coverage

Show which API endpoints a scenario's journeys exercise and which none does

### Synopsis

Map a scenario's requests onto the endpoints of an API: an OpenAPI spec, a
GraphQL schema, or the endpoints seen in a HAR recording or access log. The
result lists every endpoint with the journeys that call it, the endpoints
no journey touches, and requests that match no endpoint (often a typo or an
endpoint that was renamed). No model and no network access are needed.

```
stampede coverage <scenario.yaml> [flags]
```

### Examples

```
  stampede coverage shop.yaml --from-openapi openapi.yaml
  stampede coverage shop.yaml --from-log access.log --json
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
```

### SEE ALSO

* [stampede](stampede.md)	 - Self-hosted, distributed load testing

