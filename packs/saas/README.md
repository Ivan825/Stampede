# SaaS pack

Journeys, stresses and targets for multi-tenant B2B apps with a REST and a
GraphQL API.

| File | What it tests |
|---|---|
| `journeys/saas-mix.yaml` | Everyday mix across 19 tenants: sign in and open the dashboard and a record over GraphQL (persisted queries) 40%, list, open, edit and create records over REST 30%, create and win a record with GraphQL mutations 10%, run two reports 20% |
| `journeys/tenant-isolation.yaml` | Reading another tenant's record over REST gets 403/404 and over GraphQL `null`; wrong passwords and missing tokens are refused |
| `stresses/noisy-tenant.yaml` | The biggest tenant runs reports and pages through 100,000 records non-stop; every other tenant's journey must keep p95 under 300ms |
| `stresses/report-spike.yaml` | Monday 9:00: dashboards and reports spike from 5/s to 50/s across every tenant |
| `targets.yaml` | Default targets: p95 under 500ms, under 1% errors, dashboard under 300ms, report journeys under 2s |

The journeys follow SaaSLab's API: `POST /api/login` returns a bearer
token scoped to the user's tenant, `/api/records` (page, limit, status),
`PATCH /api/records/{id}`, `/api/reports/{pipeline|revenue-by-month|activity}`
and `POST /graphql` with `dashboard`, `records`, `record`, `report`,
`createRecord` and `updateRecord`. Every journey and stress is run against
[SaaSLab](../../examples/packlab/README.md#saaslab) in CI.

For your own app, put test users from several tenants in `data/users.csv`
(`email,password,tenant`), set your biggest tenant's user in
`noisy-tenant.yaml` and a record of a tenant not in the CSV in
`tenant-isolation.yaml`, and rewrite the GraphQL operations against your
schema; `stampede generate --from-graphql schema.graphql` can draft
them (it needs a model provider).

```sh
go run ./examples/packlab -product saas              # SaaSLab on :8091
stampede init --target http://localhost:8091         # detects this pack
stampede run stampede/saas/journeys/saas-mix.yaml -e TARGET_URL=http://localhost:8091
```
