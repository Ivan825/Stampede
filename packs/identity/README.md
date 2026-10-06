# Login and identity pack

Journeys, stresses and targets for OAuth 2.0 and OpenID Connect token
services.

| File | What it tests |
|---|---|
| `journeys/login-mix.yaml` | Everyday mix: sign in and read userinfo 45%, long sessions refreshed three times 35%, service tokens with introspection 15%, sign-out 5% (a revoked refresh token must then be refused) |
| `journeys/failed-login.yaml` | Wrong passwords, unknown users and forged tokens get `invalid_grant` or 401 quickly, never a 5xx |
| `stresses/login-storm.yaml` | Sign-ins spike from 10/s to 150/s; password hashing is expensive by design, so this finds the sign-in ceiling and whether userinfo suffers |
| `stresses/refresh-wave.yaml` | 200 sessions refresh on the same 5-second beat ten times; refresh latency should stay flat as tokens pile up |
| `targets.yaml` | Default targets: p95 under 500ms, under 1% errors, sign-in under 800ms, refresh under 200ms |

The journeys use the standard endpoints: form-encoded `POST /oauth/token`
(`password`, `refresh_token` and `client_credentials` grants), `GET
/userinfo`, `POST /oauth/introspect` and `POST /oauth/revoke`. Refresh
tokens are rotated: each refresh extracts the new one. Every journey and
stress is run against [AuthLab](../../examples/packlab/README.md#authlab)
in CI.

`stampede init` recognises an identity provider by its OpenID Connect
discovery document (`/.well-known/openid-configuration`). For your own
provider, change the token paths if they differ (Keycloak uses
`/realms/<realm>/protocol/openid-connect/token`), set the client ids under
`vars`, keep client secrets in `${secret.X}` rather than in the file, and
replace `data/users.csv` with test accounts that exist. Many providers turn
off the password grant; if yours does, keep the refresh and service-token
journeys and sign users in with tokens issued another way.

```sh
go run ./examples/packlab -product identity          # AuthLab on :8095
stampede init --target http://localhost:8095         # detects this pack
stampede run stampede/identity/journeys/login-mix.yaml -e TARGET_URL=http://localhost:8095
```
