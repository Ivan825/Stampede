# Mobile backends pack

Journeys, stresses and targets for the API behind a mobile app, driven
the way the app drives it: launches, resumes, sync, analytics and the
home screen.

| File | What it tests |
|---|---|
| `journeys/app-mix.yaml` | Across 2,000 users: cold launch (remote config, sign-in, device registration, full sync, the home screen as a persisted GraphQL query) 40%; resume with a delta sync carrying an offline change 25%; complete an item (GraphQL mutation, analytics events) 20%; a background analytics flush 10%; an old app version that must get 426 with the version to update to 5% |
| `journeys/slow-network.yaml` | A returning user's launch over an emulated 3G network (300ms round trips, 1.6 Mbit/s down): config, session, delta sync, home screen |
| `stresses/push-storm.yaml` | A push makes everyone open the app: launches spike from 10/s to 500/s; config and home screen under 200 and 300ms, delta sync under 300ms |
| `stresses/offline-catchup.yaml` | Devices come back online together, each with five offline changes and 20 queued events; reconnections climb from 5/s to 150/s |
| `targets.yaml` | Default targets: p95 under 500ms, under 1% errors, config under 200ms, sync under 500ms, the home screen under 300ms |

Every request carries `X-App-Version` (set once under `target.headers`);
config also sends `X-Platform` and `X-Device-Id`. The journeys follow
MobileLab's API: `GET /api/v1/config` (feature flags; with an ETag when
cacheable), `POST /api/v1/auth/login`, `POST /api/v1/devices`, `POST
/api/v1/sync` with a `since` token and `changes` (it answers with the
changes since, and the `next` token), `POST /api/v1/events` with a batch,
and `POST /api/v1/graphql` with automatic persisted queries. Every
journey and stress is run against
[MobileLab](../../examples/packlab/README.md#mobilelab) in CI.

For your own backend, change the paths, the version header and the
GraphQL operations (the hash of each query is computed from the query
text, so persisted queries work unchanged), and put test accounts in
`data/users.csv`. Stresses that resume a session use a sync token under
`vars`; use one your clients would really hold. If the app talks gRPC,
replace the HTTP steps with `grpc` steps; the journey shapes stay the
same.

```sh
go run ./examples/packlab -product mobile-backends   # MobileLab on :8104
stampede init --target http://localhost:8104         # detects this pack
stampede run stampede/mobile-backends/stresses/push-storm.yaml -e TARGET_URL=http://localhost:8104
```
