# Coverage and drift

Two checks keep saved scenarios honest as an API changes. Neither needs an
AI model.

## Coverage map

`stampede coverage` maps a scenario's requests onto an API's endpoints:

```sh
stampede coverage shop.yaml --from-openapi openapi.yaml
```

```
  6 of 18 endpoints covered (33%)

  ✗ GET     /api/cart                                    no journey
  ✓ POST    /api/cart                                    checkout
  ✓ POST    /api/login                                   checkout, order-history
  ✗ GET     /api/orders/{id}                             no journey
  ...
```

The API can come from an OpenAPI spec (`--from-openapi`), a GraphQL schema
(`--from-graphql`), or the endpoints seen in real traffic (`--from-har`,
`--from-log`). Requests that match no endpoint are listed separately: they
are usually a typo or an endpoint that was renamed. Requests whose whole
URL is an expression (`get: ${next}`) cannot be matched statically and are
counted. `--json` prints the result for other tools.

## Drift detection

`stampede drift` checks a saved scenario against the current version of the
API and exits with code 4 when a journey broke:

```sh
stampede drift shop.yaml --from-openapi v2.yaml --previous-openapi v1.yaml \
  --target http://staging:8080
```

```
  API change: 1 endpoints added, 1 removed
    - POST /api/cart
    + POST /api/basket
  ✗ journey buy calls removed endpoints: POST /api/cart
  ✗ buy: POST /api/cart is not an endpoint of the current API
  ✓ browse passes its dry run
  ✗ buy fails its dry run (step POST /api/cart: status 404)
```

- `--previous-*` gives the previous version of the API. Endpoints removed
  since then, and the journeys that call them, are listed. A renamed path
  parameter does not count as a change.
- Requests that use no endpoint of the current API are reported.
- `--target` dry-runs every journey once with one user, as
  [`stampede generate`](../ai.md) does, and reports the journeys that now
  fail. These are real requests: point it at a test environment.
  `-e KEY=VALUE`, `STAMPEDE_SECRET_*` and `--allow-host` work as for
  `stampede run`.

Run it in CI after an API change, or on a schedule, so a broken journey is
found before the next load test. To repair a scenario, regenerate it with
the old one as the starting point:

```sh
stampede generate --from-openapi v2.yaml --diff-against shop.yaml \
  --target http://staging:8080 -o shop.yaml
```

The generator dry-runs and repairs each journey, and shows the diff before
anything is written.
