# REST API reference

<!-- Generated from api/openapi.yaml by `make api-docs`. Do not edit by hand. -->

Stampede API, version 1.0.0. Every path below is relative to `/api/v1` on the server, for example `http://localhost:8080/api/v1/version`. The OpenAPI document itself is [api/openapi.yaml](../../api/openapi.yaml).

The public REST API of a Stampede server. The web UI, the CLI and CI
integrations all use this API.

Authenticate with a session cookie (from `POST /auth/login`) or an API
token sent as `Authorization: Bearer stp_...`. Requests that use the
session cookie and change state must also send `X-Stampede-CSRF: 1`.

## Authentication

| Scheme | Type | Details |
|---|---|---|
| `bearer` | http | `Authorization: Bearer ...` API token, `stp_...` |
| `session` | apiKey | cookie `stampede_session` |

Operations marked **no authentication** can be called without either.

## Operations

- [System](#system): [`GET /version`](#get-version), [`POST /setup`](#post-setup)
- [Auth](#auth): [`POST /auth/login`](#post-authlogin), [`GET /auth/config`](#get-authconfig), [`POST /auth/logout`](#post-authlogout), [`GET /me`](#get-me), [`PUT /me/password`](#put-mepassword)
- [Users](#users): [`GET /users`](#get-users), [`POST /users`](#post-users), [`PATCH /users/{userId}`](#patch-usersuserid), [`DELETE /users/{userId}`](#delete-usersuserid)
- [Tokens](#tokens): [`GET /tokens`](#get-tokens), [`POST /tokens`](#post-tokens), [`DELETE /tokens/{tokenId}`](#delete-tokenstokenid)
- [Projects](#projects): [`GET /projects`](#get-projects), [`POST /projects`](#post-projects), [`GET /projects/{projectId}`](#get-projectsprojectid), [`PATCH /projects/{projectId}`](#patch-projectsprojectid), [`DELETE /projects/{projectId}`](#delete-projectsprojectid)
- [Targets](#targets): [`GET /projects/{projectId}/targets`](#get-projectsprojectidtargets), [`POST /projects/{projectId}/targets`](#post-projectsprojectidtargets), [`GET /targets/{targetId}`](#get-targetstargetid), [`PATCH /targets/{targetId}`](#patch-targetstargetid), [`DELETE /targets/{targetId}`](#delete-targetstargetid), [`POST /targets/{targetId}/verify`](#post-targetstargetidverify)
- [Secrets](#secrets): [`GET /projects/{projectId}/secrets`](#get-projectsprojectidsecrets), [`PUT /projects/{projectId}/secrets`](#put-projectsprojectidsecrets), [`DELETE /projects/{projectId}/secrets/{name}`](#delete-projectsprojectidsecretsname)
- [Scenarios](#scenarios): [`POST /scenarios/validate`](#post-scenariosvalidate), [`GET /projects/{projectId}/scenarios`](#get-projectsprojectidscenarios), [`POST /projects/{projectId}/scenarios`](#post-projectsprojectidscenarios), [`GET /scenarios/{scenarioId}`](#get-scenariosscenarioid), [`DELETE /scenarios/{scenarioId}`](#delete-scenariosscenarioid), [`GET /scenarios/{scenarioId}/versions`](#get-scenariosscenarioidversions), [`POST /scenarios/{scenarioId}/versions`](#post-scenariosscenarioidversions), [`GET /scenarios/{scenarioId}/versions/{version}`](#get-scenariosscenarioidversionsversion), [`POST /scenarios/{scenarioId}/coverage`](#post-scenariosscenarioidcoverage), [`POST /scenarios/{scenarioId}/drift`](#post-scenariosscenarioiddrift)
- [Runs](#runs): [`GET /projects/{projectId}/runs`](#get-projectsprojectidruns), [`POST /projects/{projectId}/runs`](#post-projectsprojectidruns), [`GET /runs/{runId}`](#get-runsrunid), [`POST /runs/{runId}/stop`](#post-runsrunidstop), [`POST /runs/{runId}/kill`](#post-runsrunidkill), [`POST /runs/kill-all`](#post-runskill-all), [`GET /runs/{runId}/live`](#get-runsrunidlive), [`GET /runs/{runId}/timeline`](#get-runsrunidtimeline), [`GET /runs/{runId}/report`](#get-runsrunidreport), [`POST /compare`](#post-compare), [`GET /runs/{runId}/workers`](#get-runsrunidworkers)
- [Schedules](#schedules): [`GET /projects/{projectId}/schedules`](#get-projectsprojectidschedules), [`POST /projects/{projectId}/schedules`](#post-projectsprojectidschedules), [`GET /schedules/preview`](#get-schedulespreview), [`GET /schedules/{scheduleId}`](#get-schedulesscheduleid), [`PATCH /schedules/{scheduleId}`](#patch-schedulesscheduleid), [`DELETE /schedules/{scheduleId}`](#delete-schedulesscheduleid), [`POST /schedules/{scheduleId}/run`](#post-schedulesscheduleidrun)
- [Workers](#workers): [`GET /workers`](#get-workers)
- [Audit](#audit): [`GET /audit`](#get-audit)
- [AI](#ai): [`GET /ai/providers`](#get-aiproviders), [`POST /ai/providers`](#post-aiproviders), [`DELETE /ai/providers/{providerId}`](#delete-aiprovidersproviderid), [`GET /projects/{projectId}/ai/jobs`](#get-projectsprojectidaijobs), [`POST /projects/{projectId}/ai/jobs`](#post-projectsprojectidaijobs), [`POST /runs/{runId}/narrative`](#post-runsrunidnarrative), [`GET /ai/jobs/{jobId}`](#get-aijobsjobid), [`POST /ai/jobs/{jobId}/approve`](#post-aijobsjobidapprove)
- [Integrations](#integrations): [`GET /integrations`](#get-integrations), [`POST /integrations`](#post-integrations), [`DELETE /integrations/{integrationId}`](#delete-integrationsintegrationid), [`GET /notifications/channels`](#get-notificationschannels), [`POST /notifications/channels`](#post-notificationschannels), [`DELETE /notifications/channels/{channelId}`](#delete-notificationschannelschannelid), [`POST /notifications/channels/{channelId}/test`](#post-notificationschannelschannelidtest), [`GET /notifications/channels/{channelId}/deliveries`](#get-notificationschannelschanneliddeliveries)
- [Packs](#packs): [`GET /packs`](#get-packs), [`GET /packs/{packName}`](#get-packspackname)
- [Settings](#settings): [`GET /settings/sso`](#get-settingssso), [`GET /settings/limits`](#get-settingslimits)

## System

### GET /version

Operation `getVersion`, **no authentication**.

| Status | Description | Body |
|---|---|---|
| 200 | Server version and setup state | `application/json` [VersionInfo](#versioninfo) |

### POST /setup

Create the first organisation and owner account.

Only allowed while the server has no users.

Operation `setup`, **no authentication**.

**Request body** (required): `application/json` [SetupRequest](#setuprequest)

| Status | Description | Body |
|---|---|---|
| 201 | Owner created and signed in | `application/json` [Session](#session) |
| 409 | Conflicts with the current state | `application/json` [Error](#error) |
| 422 | The request is well-formed but invalid | `application/json` [Error](#error) |

## Auth

### POST /auth/login

Operation `login`, **no authentication**.

**Request body** (required): `application/json` [LoginRequest](#loginrequest)

| Status | Description | Body |
|---|---|---|
| 200 | Signed in; sets the session cookie | `application/json` [Session](#session) |
| 401 | Not signed in or token invalid | `application/json` [Error](#error) |
| 429 | Too many attempts | `application/json` [Error](#error) |

### GET /auth/config

How people can sign in (public).

Operation `getAuthConfig`, **no authentication**.

| Status | Description | Body |
|---|---|---|
| 200 | The sign-in methods this server offers | `application/json` [AuthConfig](#authconfig) |

### POST /auth/logout

Operation `logout`.

| Status | Description | Body |
|---|---|---|
| 204 | Signed out | — |

### GET /me

Operation `getMe`.

| Status | Description | Body |
|---|---|---|
| 200 | The current user | `application/json` [Me](#me) |
| 401 | Not signed in or token invalid | `application/json` [Error](#error) |

### PUT /me/password

Operation `changePassword`.

**Request body** (required): `application/json` object

| Field | Type | Required | Description |
|---|---|---|---|
| `current` | string | yes |  |
| `new` | string | yes |  |

| Status | Description | Body |
|---|---|---|
| 204 | Changed | — |
| 401 | Not signed in or token invalid | `application/json` [Error](#error) |
| 422 | The request is well-formed but invalid | `application/json` [Error](#error) |

## Users

### GET /users

Operation `listUsers`.

| Status | Description | Body |
|---|---|---|
| 200 | Members of the organisation | `application/json` array of [User](#user) |

### POST /users

Requires admin.

Operation `createUser`.

**Request body** (required): `application/json` [UserCreate](#usercreate)

| Status | Description | Body |
|---|---|---|
| 201 | Created | `application/json` [User](#user) |
| 403 | The caller's role does not allow this | `application/json` [Error](#error) |
| 409 | Conflicts with the current state | `application/json` [Error](#error) |
| 422 | The request is well-formed but invalid | `application/json` [Error](#error) |

### PATCH /users/{userId}

Operation `updateUser`.

| Parameter | In | Type | Required | Description |
|---|---|---|---|---|
| `userId` | path | string (uuid) | yes |  |

**Request body** (required): `application/json` object

| Field | Type | Required | Description |
|---|---|---|---|
| `name` | string | no |  |
| `role` | [Role](#role) | no |  |

| Status | Description | Body |
|---|---|---|
| 200 | Updated | `application/json` [User](#user) |
| 403 | The caller's role does not allow this | `application/json` [Error](#error) |
| 404 | Not found | `application/json` [Error](#error) |

### DELETE /users/{userId}

Operation `deleteUser`.

| Parameter | In | Type | Required | Description |
|---|---|---|---|---|
| `userId` | path | string (uuid) | yes |  |

| Status | Description | Body |
|---|---|---|
| 204 | Removed | — |
| 403 | The caller's role does not allow this | `application/json` [Error](#error) |
| 404 | Not found | `application/json` [Error](#error) |

## Tokens

### GET /tokens

Operation `listTokens`.

| Status | Description | Body |
|---|---|---|
| 200 | The caller's API tokens (secrets are never returned) | `application/json` array of [Token](#token) |

### POST /tokens

Operation `createToken`.

**Request body** (required): `application/json` [TokenCreate](#tokencreate)

| Status | Description | Body |
|---|---|---|
| 201 | Created. The secret is shown only in this response. | `application/json` [TokenCreated](#tokencreated) |
| 422 | The request is well-formed but invalid | `application/json` [Error](#error) |

### DELETE /tokens/{tokenId}

Operation `deleteToken`.

| Parameter | In | Type | Required | Description |
|---|---|---|---|---|
| `tokenId` | path | string (uuid) | yes |  |

| Status | Description | Body |
|---|---|---|
| 204 | Revoked | — |
| 404 | Not found | `application/json` [Error](#error) |

## Projects

### GET /projects

Operation `listProjects`.

| Status | Description | Body |
|---|---|---|
| 200 | Projects | `application/json` array of [Project](#project) |

### POST /projects

Operation `createProject`.

**Request body** (required): `application/json` [ProjectCreate](#projectcreate)

| Status | Description | Body |
|---|---|---|
| 201 | Created | `application/json` [Project](#project) |
| 409 | Conflicts with the current state | `application/json` [Error](#error) |
| 422 | The request is well-formed but invalid | `application/json` [Error](#error) |

### GET /projects/{projectId}

Operation `getProject`.

| Parameter | In | Type | Required | Description |
|---|---|---|---|---|
| `projectId` | path | string (uuid) | yes |  |

| Status | Description | Body |
|---|---|---|
| 200 | Project | `application/json` [Project](#project) |
| 404 | Not found | `application/json` [Error](#error) |

### PATCH /projects/{projectId}

Operation `updateProject`.

| Parameter | In | Type | Required | Description |
|---|---|---|---|---|
| `projectId` | path | string (uuid) | yes |  |

**Request body** (required): `application/json` [ProjectCreate](#projectcreate)

| Status | Description | Body |
|---|---|---|
| 200 | Updated | `application/json` [Project](#project) |
| 404 | Not found | `application/json` [Error](#error) |

### DELETE /projects/{projectId}

Operation `deleteProject`.

| Parameter | In | Type | Required | Description |
|---|---|---|---|---|
| `projectId` | path | string (uuid) | yes |  |

| Status | Description | Body |
|---|---|---|
| 204 | Deleted with everything in it | — |
| 404 | Not found | `application/json` [Error](#error) |

## Targets

### GET /projects/{projectId}/targets

Operation `listTargets`.

| Parameter | In | Type | Required | Description |
|---|---|---|---|---|
| `projectId` | path | string (uuid) | yes |  |

| Status | Description | Body |
|---|---|---|
| 200 | Targets | `application/json` array of [Target](#target) |

### POST /projects/{projectId}/targets

Operation `createTarget`.

| Parameter | In | Type | Required | Description |
|---|---|---|---|---|
| `projectId` | path | string (uuid) | yes |  |

**Request body** (required): `application/json` [TargetCreate](#targetcreate)

| Status | Description | Body |
|---|---|---|
| 201 | Created | `application/json` [Target](#target) |
| 422 | The request is well-formed but invalid | `application/json` [Error](#error) |

### GET /targets/{targetId}

Operation `getTarget`.

| Parameter | In | Type | Required | Description |
|---|---|---|---|---|
| `targetId` | path | string (uuid) | yes |  |

| Status | Description | Body |
|---|---|---|
| 200 | Target | `application/json` [Target](#target) |
| 404 | Not found | `application/json` [Error](#error) |

### PATCH /targets/{targetId}

Operation `updateTarget`.

| Parameter | In | Type | Required | Description |
|---|---|---|---|---|
| `targetId` | path | string (uuid) | yes |  |

**Request body** (required): `application/json` [TargetCreate](#targetcreate)

| Status | Description | Body |
|---|---|---|
| 200 | Updated | `application/json` [Target](#target) |
| 404 | Not found | `application/json` [Error](#error) |

### DELETE /targets/{targetId}

Operation `deleteTarget`.

| Parameter | In | Type | Required | Description |
|---|---|---|---|---|
| `targetId` | path | string (uuid) | yes |  |

| Status | Description | Body |
|---|---|---|
| 204 | Deleted | — |
| 404 | Not found | `application/json` [Error](#error) |

### POST /targets/{targetId}/verify

Check the ownership token for a public target.

Operation `verifyTarget`.

| Parameter | In | Type | Required | Description |
|---|---|---|---|---|
| `targetId` | path | string (uuid) | yes |  |

| Status | Description | Body |
|---|---|---|
| 200 | Verification state after the check | `application/json` [Target](#target) |
| 404 | Not found | `application/json` [Error](#error) |

## Secrets

### GET /projects/{projectId}/secrets

Operation `listSecrets`.

| Parameter | In | Type | Required | Description |
|---|---|---|---|---|
| `projectId` | path | string (uuid) | yes |  |

| Status | Description | Body |
|---|---|---|
| 200 | Secret names (values are never returned) | `application/json` array of [Secret](#secret) |

### PUT /projects/{projectId}/secrets

Operation `putSecret`.

| Parameter | In | Type | Required | Description |
|---|---|---|---|---|
| `projectId` | path | string (uuid) | yes |  |

**Request body** (required): `application/json` [SecretPut](#secretput)

| Status | Description | Body |
|---|---|---|
| 200 | Stored, encrypted at rest | `application/json` [Secret](#secret) |
| 422 | The request is well-formed but invalid | `application/json` [Error](#error) |

### DELETE /projects/{projectId}/secrets/{name}

Operation `deleteSecret`.

| Parameter | In | Type | Required | Description |
|---|---|---|---|---|
| `projectId` | path | string (uuid) | yes |  |
| `name` | path | string | yes |  |

| Status | Description | Body |
|---|---|---|
| 204 | Deleted | — |
| 404 | Not found | `application/json` [Error](#error) |

## Scenarios

### POST /scenarios/validate

Operation `validateScenario`.

**Request body** (required): `application/json` object

| Field | Type | Required | Description |
|---|---|---|---|
| `yaml` | string | yes |  |

| Status | Description | Body |
|---|---|---|
| 200 | Validation result | `application/json` [Validation](#validation) |

### GET /projects/{projectId}/scenarios

Operation `listScenarios`.

| Parameter | In | Type | Required | Description |
|---|---|---|---|---|
| `projectId` | path | string (uuid) | yes |  |
| `tag` | query | string | no |  |

| Status | Description | Body |
|---|---|---|
| 200 | Scenarios with their latest version | `application/json` array of [Scenario](#scenario) |

### POST /projects/{projectId}/scenarios

Operation `createScenario`.

| Parameter | In | Type | Required | Description |
|---|---|---|---|---|
| `projectId` | path | string (uuid) | yes |  |

**Request body** (required): `application/json` [ScenarioVersionCreate](#scenarioversioncreate)

| Status | Description | Body |
|---|---|---|
| 201 | Created with version 1 | `application/json` [Scenario](#scenario) |
| 409 | Conflicts with the current state | `application/json` [Error](#error) |
| 422 | The request is well-formed but invalid | `application/json` [Error](#error) |

### GET /scenarios/{scenarioId}

Operation `getScenario`.

| Parameter | In | Type | Required | Description |
|---|---|---|---|---|
| `scenarioId` | path | string (uuid) | yes |  |

| Status | Description | Body |
|---|---|---|
| 200 | Scenario with its latest version | `application/json` [Scenario](#scenario) |
| 404 | Not found | `application/json` [Error](#error) |

### DELETE /scenarios/{scenarioId}

Operation `deleteScenario`.

| Parameter | In | Type | Required | Description |
|---|---|---|---|---|
| `scenarioId` | path | string (uuid) | yes |  |

| Status | Description | Body |
|---|---|---|
| 204 | Deleted | — |
| 404 | Not found | `application/json` [Error](#error) |

### GET /scenarios/{scenarioId}/versions

Operation `listScenarioVersions`.

| Parameter | In | Type | Required | Description |
|---|---|---|---|---|
| `scenarioId` | path | string (uuid) | yes |  |

| Status | Description | Body |
|---|---|---|
| 200 | All versions, newest first | `application/json` array of [ScenarioVersion](#scenarioversion) |

### POST /scenarios/{scenarioId}/versions

Operation `createScenarioVersion`.

| Parameter | In | Type | Required | Description |
|---|---|---|---|---|
| `scenarioId` | path | string (uuid) | yes |  |

**Request body** (required): `application/json` [ScenarioVersionCreate](#scenarioversioncreate)

| Status | Description | Body |
|---|---|---|
| 201 | New version saved | `application/json` [ScenarioVersion](#scenarioversion) |
| 422 | The request is well-formed but invalid | `application/json` [Error](#error) |

### GET /scenarios/{scenarioId}/versions/{version}

Operation `getScenarioVersion`.

| Parameter | In | Type | Required | Description |
|---|---|---|---|---|
| `scenarioId` | path | string (uuid) | yes |  |
| `version` | path | integer | yes |  |

| Status | Description | Body |
|---|---|---|
| 200 | The version | `application/json` [ScenarioVersion](#scenarioversion) |
| 404 | Not found | `application/json` [Error](#error) |

### POST /scenarios/{scenarioId}/coverage

Which endpoints of an API the scenario's journeys exercise.

The same mapping as `stampede coverage`: every endpoint of the API
with the journeys that call it, and requests that match no
endpoint. No model and no requests to the target are needed. Give
the API as an OpenAPI document, or as `specURL` on the host of one
of the project's targets (or a host it allows); fetching by URL needs
the editor role.

Operation `scenarioCoverage`.

| Parameter | In | Type | Required | Description |
|---|---|---|---|---|
| `scenarioId` | path | string (uuid) | yes |  |

**Request body** (required): `application/json` [CoverageRequest](#coveragerequest)

| Status | Description | Body |
|---|---|---|
| 200 | Coverage | `application/json` [ScenarioCoverage](#scenariocoverage) |
| 403 | The caller's role does not allow this | `application/json` [Error](#error) |
| 404 | Not found | `application/json` [Error](#error) |
| 422 | The request is well-formed but invalid | `application/json` [Error](#error) |

### POST /scenarios/{scenarioId}/drift

Find journeys an API change broke.

The same checks as `stampede drift`: with a previous version of the
API, endpoints removed since and the journeys that call them;
requests that use no endpoint of the current API; and with
`targetId`, a dry run of every journey with one user (real requests,
which needs the runner role).

Operation `scenarioDrift`.

| Parameter | In | Type | Required | Description |
|---|---|---|---|---|
| `scenarioId` | path | string (uuid) | yes |  |

**Request body** (required): `application/json` [DriftRequest](#driftrequest)

| Status | Description | Body |
|---|---|---|
| 200 | Drift | `application/json` [ScenarioDrift](#scenariodrift) |
| 403 | The caller's role does not allow this | `application/json` [Error](#error) |
| 404 | Not found | `application/json` [Error](#error) |
| 422 | The request is well-formed but invalid | `application/json` [Error](#error) |

## Runs

### GET /projects/{projectId}/runs

Operation `listRuns`.

| Parameter | In | Type | Required | Description |
|---|---|---|---|---|
| `projectId` | path | string (uuid) | yes |  |
| `scenarioId` | query | string (uuid) | no |  |
| `limit` | query | integer | no | Default `50`. |
| `before` | query | string (date-time) | no | Return runs created before this time (pagination). |

| Status | Description | Body |
|---|---|---|
| 200 | Runs, newest first | `application/json` array of [Run](#run) |

### POST /projects/{projectId}/runs

Operation `createRun`.

| Parameter | In | Type | Required | Description |
|---|---|---|---|---|
| `projectId` | path | string (uuid) | yes |  |

**Request body** (required): `application/json` [RunCreate](#runcreate)

| Status | Description | Body |
|---|---|---|
| 201 | Run accepted and scheduled | `application/json` [Run](#run) |
| 403 | The caller's role does not allow this | `application/json` [Error](#error) |
| 409 | Conflicts with the current state | `application/json` [Error](#error) |
| 422 | The request is well-formed but invalid | `application/json` [Error](#error) |

### GET /runs/{runId}

Operation `getRun`.

| Parameter | In | Type | Required | Description |
|---|---|---|---|---|
| `runId` | path | string (uuid) | yes |  |

| Status | Description | Body |
|---|---|---|
| 200 | Run | `application/json` [Run](#run) |
| 404 | Not found | `application/json` [Error](#error) |

### POST /runs/{runId}/stop

Stop gracefully; in-flight iterations may finish.

Operation `stopRun`.

| Parameter | In | Type | Required | Description |
|---|---|---|---|---|
| `runId` | path | string (uuid) | yes |  |

| Status | Description | Body |
|---|---|---|
| 202 | Stopping | — |
| 404 | Not found | `application/json` [Error](#error) |
| 409 | Conflicts with the current state | `application/json` [Error](#error) |

### POST /runs/{runId}/kill

Kill switch for one run; all load stops immediately.

Operation `killRun`.

| Parameter | In | Type | Required | Description |
|---|---|---|---|---|
| `runId` | path | string (uuid) | yes |  |

| Status | Description | Body |
|---|---|---|
| 202 | Killed | — |
| 404 | Not found | `application/json` [Error](#error) |

### POST /runs/kill-all

Kill switch for every active run in the organisation.

Operation `killAllRuns`.

| Status | Description | Body |
|---|---|---|
| 200 | Runs that were killed | `application/json` object |

Fields of the 200 response:

| Field | Type | Required | Description |
|---|---|---|---|
| `killed` | array of string (uuid) | yes |  |

### GET /runs/{runId}/live

Live metrics as server-sent events.

Emits `point` events (a timeline Point per interval), `status`
events when the run changes state, and `event` events for worker
and safety events. Ends after the run completes.

Operation `streamRun`.

| Parameter | In | Type | Required | Description |
|---|---|---|---|---|
| `runId` | path | string (uuid) | yes |  |

| Status | Description | Body |
|---|---|---|
| 200 | Event stream | `text/event-stream` string |
| 404 | Not found | `application/json` [Error](#error) |

### GET /runs/{runId}/timeline

Operation `getRunTimeline`.

| Parameter | In | Type | Required | Description |
|---|---|---|---|---|
| `runId` | path | string (uuid) | yes |  |

| Status | Description | Body |
|---|---|---|
| 200 | Per-interval points recorded so far | `application/json` array of [Point](#point) |
| 404 | Not found | `application/json` [Error](#error) |

### GET /runs/{runId}/report

Operation `getRunReport`.

| Parameter | In | Type | Required | Description |
|---|---|---|---|---|
| `runId` | path | string (uuid) | yes |  |
| `format` | query | string: `json`, `html`, `junit`, `markdown`, `csv`, `timeline-csv` | no | csv is one row per step, per journey and for the whole run; timeline-csv is one row per second. Latencies are in milliseconds. Default `json`. |

| Status | Description | Body |
|---|---|---|
| 200 | The report. JSON follows the `stampede run --json` format. | `application/json` [Report](#report)<br>`text/html` string<br>`application/xml` string<br>`text/markdown` string<br>`text/csv` string |
| 404 | Not found | `application/json` [Error](#error) |
| 409 | Conflicts with the current state | `application/json` [Error](#error) |

### POST /compare

Compare finished runs of version A with runs of version B.

The same comparison as `stampede compare`. For p50, p95, p99, error
rate, throughput and (for breakpoint runs) the highest load that
held, it gives both means, the relative change, a bootstrap 95%
confidence interval for the change and the noise floor measured
between repeats of one version. A change is a regression or an
improvement only when the interval excludes zero and the change is
larger than the noise floor. Per-step p95 and error rate are
compared too but do not affect the verdict. Every run must belong
to the caller's organisation and have finished with a report.

Operation `compareRuns`.

**Request body** (required): `application/json` [CompareRequest](#comparerequest)

| Status | Description | Body |
|---|---|---|
| 200 | The comparison | `application/json` [Comparison](#comparison) |
| 403 | The caller's role does not allow this | `application/json` [Error](#error) |
| 422 | The request is well-formed but invalid | `application/json` [Error](#error) |

### GET /runs/{runId}/workers

Health of the load generators running a run.

Each worker's latest self-monitoring while the run executes on this
server: CPU, scheduling lag, whether it reports itself saturated and
its last heartbeat. A run executed in-process lists the server
itself. `live` is false once the run has ended, or while another
replica runs it; the list is then empty.

Operation `listRunWorkers`.

| Parameter | In | Type | Required | Description |
|---|---|---|---|---|
| `runId` | path | string (uuid) | yes |  |

| Status | Description | Body |
|---|---|---|
| 200 | Worker health | `application/json` [RunWorkers](#runworkers) |
| 404 | Not found | `application/json` [Error](#error) |

## Schedules

Start runs on a cron schedule. Anyone can read them; editors and above change them.

### GET /projects/{projectId}/schedules

Operation `listSchedules`.

| Parameter | In | Type | Required | Description |
|---|---|---|---|---|
| `projectId` | path | string (uuid) | yes |  |

| Status | Description | Body |
|---|---|---|
| 200 | Schedules, by name | `application/json` array of [Schedule](#schedule) |

### POST /projects/{projectId}/schedules

Create a schedule.

The scenario, target and overrides are checked the same way as
`POST /projects/{projectId}/runs`, so a schedule that is accepted
would start a run if fired now. Runs start as the caller.

Operation `createSchedule`.

| Parameter | In | Type | Required | Description |
|---|---|---|---|---|
| `projectId` | path | string (uuid) | yes |  |

**Request body** (required): `application/json` [ScheduleCreate](#schedulecreate)

| Status | Description | Body |
|---|---|---|
| 201 | Created | `application/json` [Schedule](#schedule) |
| 403 | The caller's role does not allow this | `application/json` [Error](#error) |
| 409 | Conflicts with the current state | `application/json` [Error](#error) |
| 422 | The request is well-formed but invalid | `application/json` [Error](#error) |

### GET /schedules/preview

The next times a cron expression fires.

Operation `previewSchedule`.

| Parameter | In | Type | Required | Description |
|---|---|---|---|---|
| `cron` | query | string | yes |  |
| `timezone` | query | string | no | IANA time zone; defaults to UTC |
| `count` | query | integer | no | Default `3`. |

| Status | Description | Body |
|---|---|---|
| 200 | Upcoming firings | `application/json` [SchedulePreview](#schedulepreview) |
| 422 | The request is well-formed but invalid | `application/json` [Error](#error) |

### GET /schedules/{scheduleId}

Operation `getSchedule`.

| Parameter | In | Type | Required | Description |
|---|---|---|---|---|
| `scheduleId` | path | string (uuid) | yes |  |

| Status | Description | Body |
|---|---|---|
| 200 | Schedule | `application/json` [Schedule](#schedule) |
| 404 | Not found | `application/json` [Error](#error) |

### PATCH /schedules/{scheduleId}

Change a schedule, or enable or disable it.

Only the fields sent change. The caller becomes the schedule's
owner, so later runs start as them.

Operation `updateSchedule`.

| Parameter | In | Type | Required | Description |
|---|---|---|---|---|
| `scheduleId` | path | string (uuid) | yes |  |

**Request body** (required): `application/json` [ScheduleUpdate](#scheduleupdate)

| Status | Description | Body |
|---|---|---|
| 200 | Updated | `application/json` [Schedule](#schedule) |
| 403 | The caller's role does not allow this | `application/json` [Error](#error) |
| 404 | Not found | `application/json` [Error](#error) |
| 409 | Conflicts with the current state | `application/json` [Error](#error) |
| 422 | The request is well-formed but invalid | `application/json` [Error](#error) |

### DELETE /schedules/{scheduleId}

Operation `deleteSchedule`.

| Parameter | In | Type | Required | Description |
|---|---|---|---|---|
| `scheduleId` | path | string (uuid) | yes |  |

| Status | Description | Body |
|---|---|---|
| 204 | Deleted; runs it started are kept | — |
| 403 | The caller's role does not allow this | `application/json` [Error](#error) |
| 404 | Not found | `application/json` [Error](#error) |

### POST /schedules/{scheduleId}/run

Start the schedule's run now.

Starts a run as the caller without changing when the schedule next
fires. Refused while the schedule's previous run is still active.

Operation `runSchedule`.

| Parameter | In | Type | Required | Description |
|---|---|---|---|---|
| `scheduleId` | path | string (uuid) | yes |  |

| Status | Description | Body |
|---|---|---|
| 201 | Run accepted and scheduled | `application/json` [Run](#run) |
| 403 | The caller's role does not allow this | `application/json` [Error](#error) |
| 404 | Not found | `application/json` [Error](#error) |
| 409 | Conflicts with the current state | `application/json` [Error](#error) |
| 422 | The request is well-formed but invalid | `application/json` [Error](#error) |

## Workers

### GET /workers

Operation `listWorkers`.

| Status | Description | Body |
|---|---|---|
| 200 | Connected workers | `application/json` array of [Worker](#worker) |

## Audit

### GET /audit

Operation `listAudit`.

| Parameter | In | Type | Required | Description |
|---|---|---|---|---|
| `limit` | query | integer | no | Default `100`. |
| `before` | query | string (date-time) | no |  |

| Status | Description | Body |
|---|---|---|
| 200 | Audit log, newest first | `application/json` array of [AuditEntry](#auditentry) |
| 403 | The caller's role does not allow this | `application/json` [Error](#error) |

## AI

Optional AI journey generation (bring your own key). Runs before load, never during.

### GET /ai/providers

AI providers configured for the organisation (keys are never returned).

Operation `listAIProviders`.

| Status | Description | Body |
|---|---|---|
| 200 | Providers | `application/json` array of [AIProvider](#aiprovider) |

### POST /ai/providers

Create or replace an AI provider by name.

AI journey generation is optional and bring-your-own-key. The key is
encrypted with the server's master key and never returned. Omit
`apiKey` when replacing a provider to keep its stored key. Needs the
admin role.

Operation `putAIProvider`.

**Request body** (required): `application/json` [AIProviderPut](#aiproviderput)

| Status | Description | Body |
|---|---|---|
| 200 | Replaced | `application/json` [AIProvider](#aiprovider) |
| 201 | Created | `application/json` [AIProvider](#aiprovider) |
| 403 | The caller's role does not allow this | `application/json` [Error](#error) |
| 409 | Conflicts with the current state | `application/json` [Error](#error) |
| 422 | The request is well-formed but invalid | `application/json` [Error](#error) |

### DELETE /ai/providers/{providerId}

Operation `deleteAIProvider`.

| Parameter | In | Type | Required | Description |
|---|---|---|---|---|
| `providerId` | path | string (uuid) | yes |  |

| Status | Description | Body |
|---|---|---|
| 204 | Deleted | — |
| 403 | The caller's role does not allow this | `application/json` [Error](#error) |
| 404 | Not found | `application/json` [Error](#error) |

### GET /projects/{projectId}/ai/jobs

Operation `listAIJobs`.

| Parameter | In | Type | Required | Description |
|---|---|---|---|---|
| `projectId` | path | string (uuid) | yes |  |
| `limit` | query | integer | no | Default `50`. |

| Status | Description | Body |
|---|---|---|
| 200 | Generation jobs, newest first | `application/json` array of [AIJobSummary](#aijobsummary) |

### POST /projects/{projectId}/ai/jobs

Generate a scenario with an AI model (asynchronous).

Starts a generation job: understand the inputs, draft a scenario,
check it statically, dry-run each journey once with one user against
the target, and repair failures (up to `maxRepairs` rounds). Recorded
traffic is redacted before it reaches the provider. The result is a
proposal; nothing is saved until `POST /ai/jobs/{jobId}/approve`.
Refused with 429 when the organisation's AI token use this month
has reached the provider's cap.

Operation `createAIJob`.

| Parameter | In | Type | Required | Description |
|---|---|---|---|---|
| `projectId` | path | string (uuid) | yes |  |

**Request body** (required): `application/json` [AIJobCreate](#aijobcreate)

| Status | Description | Body |
|---|---|---|
| 202 | Job accepted | `application/json` [AIJob](#aijob) |
| 403 | The caller's role does not allow this | `application/json` [Error](#error) |
| 409 | Conflicts with the current state | `application/json` [Error](#error) |
| 422 | The request is well-formed but invalid | `application/json` [Error](#error) |
| 429 | Monthly AI token cap reached, or too many jobs queued | `application/json` [Error](#error) |

### POST /runs/{runId}/narrative

Write an AI summary of a finished run's report.

Asks the organisation's AI provider for a summary of the report.
Every claim cites the report figures it rests on and is labelled
measured or suspected; claims that cite unknown figures, or state
numbers the cited figures do not contain, are dropped. Only the
report's aggregate figures are sent, with error messages redacted.
The narrative is saved into the report, so the HTML and Markdown
downloads include it, and replaces any earlier one. Refused with
429 when the organisation's AI token use this month has reached
the provider's cap.

Operation `createRunNarrative`.

| Parameter | In | Type | Required | Description |
|---|---|---|---|---|
| `runId` | path | string (uuid) | yes |  |

**Request body** (optional): `application/json` [NarrativeCreate](#narrativecreate)

| Status | Description | Body |
|---|---|---|
| 200 | The narrative, now part of the report | `application/json` [NarrativeResult](#narrativeresult) |
| 403 | The caller's role does not allow this | `application/json` [Error](#error) |
| 404 | Not found | `application/json` [Error](#error) |
| 409 | Conflicts with the current state | `application/json` [Error](#error) |
| 422 | The request is well-formed but invalid | `application/json` [Error](#error) |
| 429 | Monthly AI token cap reached | `application/json` [Error](#error) |
| 502 | The AI provider failed or returned nothing usable | `application/json` [Error](#error) |

### GET /ai/jobs/{jobId}

Job status, stage, token usage, proposal, dry-run traces, problems and diff.

Operation `getAIJob`.

| Parameter | In | Type | Required | Description |
|---|---|---|---|---|
| `jobId` | path | string (uuid) | yes |  |

| Status | Description | Body |
|---|---|---|
| 200 | The job | `application/json` [AIJob](#aijob) |
| 404 | Not found | `application/json` [Error](#error) |

### POST /ai/jobs/{jobId}/approve

Save the proposal as a new scenario or a new scenario version.

A person approves a finished job by saving its proposal: as a new
version of `scenarioId` (default: the scenario the job compared
against), otherwise as a new scenario. Jobs with flagged journeys
need `allowUnvalidated`. Needs the editor role and is audited.

Operation `approveAIJob`.

| Parameter | In | Type | Required | Description |
|---|---|---|---|---|
| `jobId` | path | string (uuid) | yes |  |

**Request body** (required): `application/json` [AIJobApprove](#aijobapprove)

| Status | Description | Body |
|---|---|---|
| 201 | Saved | `application/json` [AIJobApproval](#aijobapproval) |
| 403 | The caller's role does not allow this | `application/json` [Error](#error) |
| 404 | Not found | `application/json` [Error](#error) |
| 409 | Conflicts with the current state | `application/json` [Error](#error) |
| 422 | The request is well-formed but invalid | `application/json` [Error](#error) |

## Integrations

Prometheus and trace integrations for server runs, and notification channels. Admin only.

### GET /integrations

Integrations configured for the organisation (tokens are never returned).

Operation `listIntegrations`.

| Status | Description | Body |
|---|---|---|
| 200 | Integrations | `application/json` array of [Integration](#integration) |

### POST /integrations

Add a named Prometheus or traces integration.

Scenarios run on the server refer to integrations by name
(`observe: { prometheus: { integration: <name>, queries: ... } }`),
so the server only ever contacts URLs an admin configured here.
A Prometheus bearer token is encrypted with the server's master key
and never returned. Needs the admin role and is audited.

Operation `createIntegration`.

**Request body** (required): `application/json` [IntegrationCreate](#integrationcreate)

| Status | Description | Body |
|---|---|---|
| 201 | Created | `application/json` [Integration](#integration) |
| 403 | The caller's role does not allow this | `application/json` [Error](#error) |
| 409 | Conflicts with the current state | `application/json` [Error](#error) |
| 422 | The request is well-formed but invalid | `application/json` [Error](#error) |

### DELETE /integrations/{integrationId}

Operation `deleteIntegration`.

| Parameter | In | Type | Required | Description |
|---|---|---|---|---|
| `integrationId` | path | string (uuid) | yes |  |

| Status | Description | Body |
|---|---|---|
| 204 | Deleted | — |
| 403 | The caller's role does not allow this | `application/json` [Error](#error) |
| 404 | Not found | `application/json` [Error](#error) |

### GET /notifications/channels

Notification channels of the organisation (URLs and secrets are never returned).

Operation `listNotificationChannels`.

| Status | Description | Body |
|---|---|---|
| 200 | Channels | `application/json` array of [NotificationChannel](#notificationchannel) |
| 403 | The caller's role does not allow this | `application/json` [Error](#error) |

### POST /notifications/channels

Add a webhook, Slack or Discord channel.

The destination URL is encrypted with the server's master key. A
generic webhook signs each body with HMAC-SHA256 in the
`X-Stampede-Signature` header; its secret is generated when not
given and returned only in this response. Destinations on private,
loopback or link-local addresses are refused unless
`allowPrivate` is set. Needs the admin role and is audited.

Operation `createNotificationChannel`.

**Request body** (required): `application/json` [NotificationChannelCreate](#notificationchannelcreate)

| Status | Description | Body |
|---|---|---|
| 201 | Created | `application/json` [NotificationChannelCreated](#notificationchannelcreated) |
| 403 | The caller's role does not allow this | `application/json` [Error](#error) |
| 409 | Conflicts with the current state | `application/json` [Error](#error) |
| 422 | The request is well-formed but invalid | `application/json` [Error](#error) |

### DELETE /notifications/channels/{channelId}

Operation `deleteNotificationChannel`.

| Parameter | In | Type | Required | Description |
|---|---|---|---|---|
| `channelId` | path | string (uuid) | yes |  |

| Status | Description | Body |
|---|---|---|
| 204 | Deleted | — |
| 403 | The caller's role does not allow this | `application/json` [Error](#error) |
| 404 | Not found | `application/json` [Error](#error) |

### POST /notifications/channels/{channelId}/test

Send a test notification now (one attempt, no retries).

Operation `testNotificationChannel`.

| Parameter | In | Type | Required | Description |
|---|---|---|---|---|
| `channelId` | path | string (uuid) | yes |  |

| Status | Description | Body |
|---|---|---|
| 200 | The attempt, also recorded in the delivery log | `application/json` [NotificationDelivery](#notificationdelivery) |
| 403 | The caller's role does not allow this | `application/json` [Error](#error) |
| 404 | Not found | `application/json` [Error](#error) |
| 409 | Conflicts with the current state | `application/json` [Error](#error) |

### GET /notifications/channels/{channelId}/deliveries

The most recent delivery attempts of a channel, newest first.

Operation `listNotificationDeliveries`.

| Parameter | In | Type | Required | Description |
|---|---|---|---|---|
| `channelId` | path | string (uuid) | yes |  |
| `limit` | query | integer | no | Default `50`. |

| Status | Description | Body |
|---|---|---|
| 200 | Attempts | `application/json` array of [NotificationDelivery](#notificationdelivery) |
| 403 | The caller's role does not allow this | `application/json` [Error](#error) |
| 404 | Not found | `application/json` [Error](#error) |

## Packs

The product packs built into the server.

### GET /packs

The product packs built into this server.

Operation `listPacks`.

| Status | Description | Body |
|---|---|---|
| 200 | The pack catalogue, shipped and planned | `application/json` array of [PackEntry](#packentry) |

### GET /packs/{packName}

A shipped pack with its journey and stress scenarios.

Operation `getPack`.

| Parameter | In | Type | Required | Description |
|---|---|---|---|---|
| `packName` | path | string | yes |  |

| Status | Description | Body |
|---|---|---|
| 200 | The pack | `application/json` [PackDetail](#packdetail) |
| 404 | Not found | `application/json` [Error](#error) |

## Settings

Read-only server configuration. Admin only.

### GET /settings/sso

The server's single sign-on configuration (admin).

Read-only. Set with `stampede server --oidc-*` flags; the client secret is never returned.

Operation `getSSOSettings`.

| Status | Description | Body |
|---|---|---|
| 200 | SSO configuration | `application/json` [SSOSettings](#ssosettings) |
| 403 | The caller's role does not allow this | `application/json` [Error](#error) |

### GET /settings/limits

The server's load caps and every target's caps (admin).

Read-only. A run must fit the server's caps, the caps for unverified public targets when they apply, and its target's own caps.

Operation `getLimitSettings`.

| Status | Description | Body |
|---|---|---|
| 200 | Limits | `application/json` [LimitSettings](#limitsettings) |
| 403 | The caller's role does not allow this | `application/json` [Error](#error) |

## Schemas

### Error

| Field | Type | Required | Description |
|---|---|---|---|
| `error` | object | yes |  |
| `error.code` | string | yes |  |
| `error.message` | string | yes |  |
| `error.details` | array of string | no |  |

### VersionInfo

| Field | Type | Required | Description |
|---|---|---|---|
| `version` | string | yes |  |
| `commit` | string | yes |  |
| `setupRequired` | boolean | yes |  |

### Role

string: `owner`, `admin`, `editor`, `runner`, `viewer`

### SetupRequest

| Field | Type | Required | Description |
|---|---|---|---|
| `organisation` | string | yes |  |
| `name` | string | yes |  |
| `email` | string (email) | yes |  |
| `password` | string | yes |  |

### LoginRequest

| Field | Type | Required | Description |
|---|---|---|---|
| `email` | string (email) | yes |  |
| `password` | string | yes |  |

### Session

| Field | Type | Required | Description |
|---|---|---|---|
| `user` | [Me](#me) | yes |  |
| `expiresAt` | string (date-time) | yes |  |

### Me

| Field | Type | Required | Description |
|---|---|---|---|
| `id` | string (uuid) | yes |  |
| `email` | string | yes |  |
| `name` | string | yes |  |
| `role` | [Role](#role) | yes |  |
| `orgId` | string (uuid) | yes |  |
| `orgName` | string | yes |  |

### User

| Field | Type | Required | Description |
|---|---|---|---|
| `id` | string (uuid) | yes |  |
| `email` | string | yes |  |
| `name` | string | yes |  |
| `role` | [Role](#role) | yes |  |
| `createdAt` | string (date-time) | yes |  |
| `lastLoginAt` | string (date-time), nullable | no |  |

### UserCreate

| Field | Type | Required | Description |
|---|---|---|---|
| `email` | string (email) | yes |  |
| `name` | string | yes |  |
| `role` | [Role](#role) | yes |  |
| `password` | string | yes |  |

### Token

| Field | Type | Required | Description |
|---|---|---|---|
| `id` | string (uuid) | yes |  |
| `name` | string | yes |  |
| `prefix` | string | yes | First characters of the token |
| `role` | [Role](#role) | yes |  |
| `createdAt` | string (date-time) | yes |  |
| `lastUsedAt` | string (date-time), nullable | no |  |
| `expiresAt` | string (date-time), nullable | no |  |

### TokenCreate

| Field | Type | Required | Description |
|---|---|---|---|
| `name` | string | yes |  |
| `role` | [Role](#role) | no |  |
| `expiresInDays` | integer | no |  |

### TokenCreated

Every field of [Token](#token), plus:

| Field | Type | Required | Description |
|---|---|---|---|
| `secret` | string | yes | The full token. Shown once. |

### Project

| Field | Type | Required | Description |
|---|---|---|---|
| `id` | string (uuid) | yes |  |
| `name` | string | yes |  |
| `slug` | string | yes |  |
| `description` | string | no |  |
| `createdAt` | string (date-time) | yes |  |

### ProjectCreate

| Field | Type | Required | Description |
|---|---|---|---|
| `name` | string | yes |  |
| `description` | string | no |  |

### Caps

| Field | Type | Required | Description |
|---|---|---|---|
| `maxRate` | number (double) | no | Iterations per second |
| `maxVUs` | integer | no |  |
| `maxDurationSeconds` | integer | no |  |

### Target

| Field | Type | Required | Description |
|---|---|---|---|
| `id` | string (uuid) | yes |  |
| `projectId` | string (uuid) | yes |  |
| `name` | string | yes |  |
| `baseURL` | string | yes |  |
| `private` | boolean | yes | Loopback or private network; needs no verification. |
| `verified` | boolean | yes |  |
| `verifiedAt` | string (date-time), nullable | no |  |
| `verificationMethod` | string, nullable | no |  |
| `verificationToken` | string | yes | Publish as a DNS TXT record `stampede-verify=<token>` or at /.well-known/stampede-verify.txt |
| `allowHosts` | array of string | no |  |
| `caps` | [Caps](#caps) | yes |  |
| `createdAt` | string (date-time) | yes |  |

### TargetCreate

| Field | Type | Required | Description |
|---|---|---|---|
| `name` | string | yes |  |
| `baseURL` | string | yes |  |
| `allowHosts` | array of string | no |  |
| `caps` | [Caps](#caps) | no |  |

### Secret

| Field | Type | Required | Description |
|---|---|---|---|
| `name` | string | yes |  |
| `updatedAt` | string (date-time) | yes |  |

### SecretPut

| Field | Type | Required | Description |
|---|---|---|---|
| `name` | string | yes |  |
| `value` | string | yes |  |

### Validation

| Field | Type | Required | Description |
|---|---|---|---|
| `valid` | boolean | yes |  |
| `problems` | array of string | yes |  |
| `plan` | [PlanSummary](#plansummary) | no |  |

### PlanSummary

| Field | Type | Required | Description |
|---|---|---|---|
| `executor` | string | yes |  |
| `mode` | string | yes |  |
| `shape` | string | no |  |
| `peak` | number (double) | yes |  |
| `durationSeconds` | number (double) | yes |  |
| `journeys` | integer | yes |  |
| `steps` | integer | yes |  |

### Scenario

| Field | Type | Required | Description |
|---|---|---|---|
| `id` | string (uuid) | yes |  |
| `projectId` | string (uuid) | yes |  |
| `name` | string | yes |  |
| `description` | string | no |  |
| `tags` | array of string | yes |  |
| `latestVersion` | [ScenarioVersion](#scenarioversion) | yes |  |
| `createdAt` | string (date-time) | yes |  |
| `updatedAt` | string (date-time) | yes |  |

### ScenarioVersion

| Field | Type | Required | Description |
|---|---|---|---|
| `scenarioId` | string (uuid) | yes |  |
| `version` | integer | yes |  |
| `yaml` | string | yes |  |
| `message` | string | no |  |
| `createdBy` | string, nullable | no |  |
| `createdAt` | string (date-time) | yes |  |
| `plan` | [PlanSummary](#plansummary) | no |  |

### ScenarioVersionCreate

| Field | Type | Required | Description |
|---|---|---|---|
| `yaml` | string | yes |  |
| `message` | string | no |  |

### RunStatus

string: `scheduling`, `starting`, `running`, `stopping`, `analyzing`, `completed`, `aborted`, `failed`

### RunOverrides

| Field | Type | Required | Description |
|---|---|---|---|
| `shape` | string | no |  |
| `mode` | string: `vus`, `rate` | no |  |
| `vus` | integer | no |  |
| `rate` | string | no |  |
| `duration` | string | no |  |
| `start` | string | no |  |
| `max` | string | no |  |

### RunCreate

| Field | Type | Required | Description |
|---|---|---|---|
| `scenarioId` | string (uuid) | yes |  |
| `version` | integer | no | Defaults to the latest |
| `targetId` | string (uuid) | yes |  |
| `overrides` | [RunOverrides](#runoverrides) | no |  |
| `env` | map of string | no |  |
| `workers` | integer | no | 0 uses every connected worker |
| `note` | string | no |  |

### Run

| Field | Type | Required | Description |
|---|---|---|---|
| `id` | string (uuid) | yes |  |
| `projectId` | string (uuid) | yes |  |
| `scenarioId` | string (uuid) | yes |  |
| `scenarioName` | string | no |  |
| `scenarioVersion` | integer | yes |  |
| `targetId` | string (uuid) | yes |  |
| `targetURL` | string | no |  |
| `status` | [RunStatus](#runstatus) | yes |  |
| `verdict` | string: `pass`, `fail`, `generator-limited`, `no-targets`, nullable | no |  |
| `stopReason` | string, nullable | no |  |
| `error` | string, nullable | no |  |
| `overrides` | [RunOverrides](#runoverrides) | no |  |
| `plan` | [PlanSummary](#plansummary) | no |  |
| `workers` | integer | no |  |
| `note` | string | no |  |
| `createdBy` | string, nullable | no |  |
| `createdAt` | string (date-time) | yes |  |
| `startedAt` | string (date-time), nullable | no |  |
| `endedAt` | string (date-time), nullable | no |  |
| `summary` | [RunSummary](#runsummary) | no |  |

### Schedule

| Field | Type | Required | Description |
|---|---|---|---|
| `id` | string (uuid) | yes |  |
| `projectId` | string (uuid) | yes |  |
| `name` | string | yes |  |
| `scenarioId` | string (uuid) | yes |  |
| `scenarioName` | string | no |  |
| `targetId` | string (uuid) | yes |  |
| `targetName` | string | no |  |
| `cron` | string | yes |  |
| `timezone` | string | yes |  |
| `overrides` | [RunOverrides](#runoverrides) | no |  |
| `env` | map of string | no | Stored as given and shown to anyone who can read the schedule; use project secrets for sensitive values. |
| `workers` | integer | yes |  |
| `enabled` | boolean | yes |  |
| `note` | string | no |  |
| `ownerId` | string (uuid), nullable | no | Runs start as this user |
| `ownerEmail` | string, nullable | no |  |
| `createdAt` | string (date-time) | yes |  |
| `updatedAt` | string (date-time) | yes |  |
| `nextRunAt` | string (date-time), nullable | no | Null while disabled |
| `lastFiredAt` | string (date-time), nullable | no | When the schedule last came due or was run by hand |
| `lastRunId` | string (uuid), nullable | no |  |
| `lastRunAt` | string (date-time), nullable | no |  |
| `lastRunStatus` | [RunStatus](#runstatus) | no |  |
| `lastRunVerdict` | string, nullable | no | pass, fail, generator-limited or no-targets, once the last run has finished |
| `lastSkipReason` | string | yes | Why the last firing started no run; empty when it did |

### ScheduleCreate

| Field | Type | Required | Description |
|---|---|---|---|
| `name` | string | yes |  |
| `scenarioId` | string (uuid) | yes |  |
| `targetId` | string (uuid) | yes |  |
| `cron` | string | yes | Five fields (minute hour day-of-month month day-of-week) or a macro such as @daily |
| `timezone` | string | no | IANA time zone; defaults to UTC |
| `overrides` | [RunOverrides](#runoverrides) | no |  |
| `env` | map of string | no |  |
| `workers` | integer | no | 0 uses every connected worker |
| `enabled` | boolean | no | Default `true`. |
| `note` | string | no |  |

### ScheduleUpdate

| Field | Type | Required | Description |
|---|---|---|---|
| `name` | string | no |  |
| `scenarioId` | string (uuid) | no |  |
| `targetId` | string (uuid) | no |  |
| `cron` | string | no |  |
| `timezone` | string | no |  |
| `overrides` | [RunOverrides](#runoverrides) | no |  |
| `env` | map of string | no |  |
| `workers` | integer | no |  |
| `enabled` | boolean | no |  |
| `note` | string | no |  |

### SchedulePreview

| Field | Type | Required | Description |
|---|---|---|---|
| `timezone` | string | yes |  |
| `next` | array of string (date-time) | yes |  |

### RunSummary

| Field | Type | Required | Description |
|---|---|---|---|
| `requests` | integer | no |  |
| `errorRate` | number (double) | no |  |
| `rps` | number (double) | no |  |
| `p95` | number (double) | no | seconds |
| `p99` | number (double) | no | seconds |

### Point

| Field | Type | Required | Description |
|---|---|---|---|
| `t` | number (double) | yes | Seconds since start |
| `rps` | number (double) | yes |  |
| `errorRate` | number (double) | yes |  |
| `p50` | number (double) | yes |  |
| `p95` | number (double) | yes |  |
| `p99` | number (double) | yes |  |
| `vus` | integer | yes |  |
| `planned` | number (double) | yes |  |
| `dropped` | integer | yes |  |
| `iterations` | integer | no |  |
| `schedLagP99` | number (double) | no |  |

### Report

The full report; same format as `stampede run --json`.

object

### CompareRequest

| Field | Type | Required | Description |
|---|---|---|---|
| `a` | array of string (uuid) | yes | Runs of the baseline version |
| `b` | array of string (uuid) | yes | Runs of the new version |
| `labelA` | string | no | Name for the baseline (default A) |
| `labelB` | string | no | Name for the new version (default B) |

### CompareVerdict

string: `regression`, `improvement`, `no-change`, `inconclusive`

### CompareSide

| Field | Type | Required | Description |
|---|---|---|---|
| `label` | string | yes |  |
| `runs` | array of string | yes |  |

### MetricDelta

| Field | Type | Required | Description |
|---|---|---|---|
| `name` | string | yes | p50, p95, p99, error rate, throughput or max sustainable load |
| `higherIsBetter` | boolean | yes |  |
| `a` | array of number (double) | yes | The value from each run of A. Latencies are in seconds, error rates are fractions. |
| `b` | array of number (double) | yes |  |
| `meanA` | number (double) | yes |  |
| `meanB` | number (double) | yes |  |
| `change` | number (double), nullable | yes | (meanB - meanA) / meanA; null when meanA is 0 and meanB is not. |
| `ciLow` | number (double), nullable | yes | Lower bound of the 95% interval of the change |
| `ciHigh` | number (double), nullable | yes | Upper bound of the 95% interval of the change |
| `noiseFloor` | number (double) | yes | Largest relative spread between repeats of one version (at least 0.02) |
| `verdict` | [CompareVerdict](#compareverdict) | yes |  |

### StepDelta

| Field | Type | Required | Description |
|---|---|---|---|
| `journey` | string | yes |  |
| `step` | string | yes |  |
| `metrics` | array of [MetricDelta](#metricdelta) | yes | p95 and error rate |

### Comparison

| Field | Type | Required | Description |
|---|---|---|---|
| `a` | [CompareSide](#compareside) | yes |  |
| `b` | [CompareSide](#compareside) | yes |  |
| `comparable` | boolean | yes | False when the runs differ in scenario |
| `problems` | array of string | yes | Why the runs are not comparable |
| `metrics` | array of [MetricDelta](#metricdelta) | yes |  |
| `steps` | array of [StepDelta](#stepdelta) | yes | Per-step comparisons; informational, they do not change the verdict |
| `verdict` | [CompareVerdict](#compareverdict) | yes |  |
| `confidence` | number (double) | yes | Confidence level of the intervals (0.95) |
| `markdown` | string | yes | The comparison as Markdown |

### Worker

| Field | Type | Required | Description |
|---|---|---|---|
| `id` | string | yes |  |
| `name` | string | yes |  |
| `region` | string | yes |  |
| `version` | string | no |  |
| `labels` | map of string | no |  |
| `cpus` | integer | no |  |
| `memoryBytes` | integer | no |  |
| `protocols` | array of string | no | Drivers the worker supports, such as http, grpc or browser. |
| `plugins` | array of string | no | Plugins installed on the worker, such as mqtt or kafka. |
| `status` | string: `idle`, `busy`, `saturated`, `lost` | yes |  |
| `runId` | string, nullable | no |  |
| `connectedAt` | string (date-time) | yes |  |
| `lastSeenAt` | string (date-time) | yes |  |

### AuditEntry

| Field | Type | Required | Description |
|---|---|---|---|
| `id` | integer (int64) | yes |  |
| `at` | string (date-time) | yes |  |
| `actor` | string | yes | User email or token name |
| `action` | string | yes |  |
| `subject` | string | no |  |
| `details` | object | no |  |
| `ip` | string | no |  |

### AIProviderKind

string: `anthropic`, `openai`, `gemini`, `ollama`, `openai-compatible`

### AIProvider

| Field | Type | Required | Description |
|---|---|---|---|
| `id` | string (uuid) | yes |  |
| `name` | string | yes |  |
| `kind` | [AIProviderKind](#aiproviderkind) | yes |  |
| `model` | string | yes |  |
| `baseURL` | string | no |  |
| `hasKey` | boolean | yes | Whether an API key is stored. The key itself is never returned. |
| `monthlyTokenCap` | integer (int64) | yes | Jobs are refused once the organisation's AI token use this calendar month (UTC) reaches this cap. |
| `usedTokensThisMonth` | integer (int64) | yes |  |
| `createdAt` | string (date-time) | yes |  |
| `updatedAt` | string (date-time) | yes |  |

### AIProviderPut

| Field | Type | Required | Description |
|---|---|---|---|
| `name` | string | no | Default `default`. |
| `kind` | [AIProviderKind](#aiproviderkind) | yes |  |
| `model` | string | no | Required except for anthropic (default claude-sonnet-5-5). |
| `baseURL` | string | no | API base URL; required for openai-compatible. |
| `apiKey` | string | no | Stored encrypted; omit to keep the current key. |
| `monthlyTokenCap` | integer (int64) | no | Default 2000000. |

### AIJobCreate

| Field | Type | Required | Description |
|---|---|---|---|
| `providerId` | string (uuid) | no | Defaults to the organisation's only provider |
| `description` | string | no |  |
| `openapi` | string | no | OpenAPI 3.x document (YAML or JSON) |
| `har` | string | no | HAR recording (JSON) |
| `accessLog` | string | no | Web server access log |
| `targetId` | string (uuid) | no | Required for the dry run. |
| `scenarioId` | string (uuid) | no | Existing scenario to compare the proposal with. |
| `dryRun` | boolean | no | Default `true`. |
| `maxRepairs` | integer | no | Default `3`. |

### AIUsage

| Field | Type | Required | Description |
|---|---|---|---|
| `inputTokens` | integer (int64) | yes |  |
| `outputTokens` | integer (int64) | yes |  |

### AuthConfig

| Field | Type | Required | Description |
|---|---|---|---|
| `password` | boolean | yes | Email and password sign-in is available. |
| `sso` | object | no | Set when single sign-on is configured. |
| `sso.name` | string | yes | Label for the sign-in button. |
| `sso.loginURL` | string | yes | Start sign-in here (a browser navigation, not a fetch); add ?next=/path to return there. |

### NarrativeCreate

| Field | Type | Required | Description |
|---|---|---|---|
| `providerId` | string (uuid) | no | Provider to use; default the only one, or the one named "default". |

### NarrativeResult

| Field | Type | Required | Description |
|---|---|---|---|
| `narrative` | [Narrative](#narrative) | yes |  |
| `usage` | [AIUsage](#aiusage) | yes |  |

### Narrative

| Field | Type | Required | Description |
|---|---|---|---|
| `summary` | string | yes |  |
| `model` | string | no |  |
| `claims` | array of [NarrativeClaim](#narrativeclaim) | yes |  |
| `facts` | array of [NarrativeFact](#narrativefact) | no | The report figures the claims cite. |

### NarrativeClaim

| Field | Type | Required | Description |
|---|---|---|---|
| `text` | string | yes |  |
| `label` | string: `measured`, `suspected` | yes |  |
| `refs` | array of string | yes | Ids of the facts the claim rests on. |

### NarrativeFact

| Field | Type | Required | Description |
|---|---|---|---|
| `id` | string | yes |  |
| `text` | string | yes |  |
| `where` | string | yes | The report section that shows it. |

### AIJobStatus

succeeded means every journey passed its dry run; needs_review means some journeys are flagged.

string: `queued`, `running`, `succeeded`, `needs_review`, `failed`

### AIProblem

| Field | Type | Required | Description |
|---|---|---|---|
| `journey` | string | no |  |
| `message` | string | yes |  |
| `fatal` | boolean | no | The proposal cannot be used while this problem remains. |

### AICheck

| Field | Type | Required | Description |
|---|---|---|---|
| `name` | string | yes |  |
| `ok` | boolean | yes |  |
| `detail` | string | no |  |

### AIStepTrace

One request of a dry run. Bodies are truncated; secrets and personal data are redacted.

| Field | Type | Required | Description |
|---|---|---|---|
| `step` | string | yes |  |
| `method` | string | no |  |
| `url` | string | no |  |
| `requestHeaders` | map of string | no |  |
| `requestBody` | string | no |  |
| `status` | integer | no |  |
| `responseHeaders` | map of string | no |  |
| `responseBody` | string | no |  |
| `durationMs` | number (double) | yes |  |
| `extracted` | map of string | no |  |
| `checks` | array of [AICheck](#aicheck) | no |  |
| `ok` | boolean | yes |  |
| `error` | string | no |  |
| `note` | string | no |  |

### AITrace

| Field | Type | Required | Description |
|---|---|---|---|
| `pass` | integer | yes |  |
| `branches` | array of string | no |  |
| `ok` | boolean | yes |  |
| `error` | string | no |  |
| `steps` | array of [AIStepTrace](#aisteptrace) | yes |  |

### AIJourney

| Field | Type | Required | Description |
|---|---|---|---|
| `name` | string | yes |  |
| `status` | string: `passed`, `flagged`, `not-run` | yes |  |
| `attempts` | integer | yes | Dry runs of this journey across repair rounds |
| `problems` | array of string | no |  |
| `traces` | array of [AITrace](#aitrace) | yes |  |

### AIJobSummary

| Field | Type | Required | Description |
|---|---|---|---|
| `id` | string (uuid) | yes |  |
| `projectId` | string (uuid) | yes |  |
| `status` | [AIJobStatus](#aijobstatus) | yes |  |
| `stage` | string | yes | understand, draft, static-check, dry-run, repair or done |
| `providerKind` | string | yes |  |
| `model` | string | yes |  |
| `usage` | [AIUsage](#aiusage) | yes |  |
| `dryRun` | boolean | yes |  |
| `error` | string | no |  |
| `createdBy` | string, nullable | no |  |
| `createdAt` | string (date-time) | yes |  |
| `finishedAt` | string (date-time), nullable | no |  |
| `approvedAt` | string (date-time), nullable | no |  |

### AIJob

Every field of [AIJobSummary](#aijobsummary), plus:

| Field | Type | Required | Description |
|---|---|---|---|
| `round` | integer | yes | Repair round (0 is the first draft) |
| `targetId` | string (uuid), nullable | no |  |
| `scenarioId` | string (uuid), nullable | no |  |
| `yaml` | string | no | The proposed scenario |
| `diff` | string | no | Unified diff against scenarioId's latest version |
| `problems` | array of [AIProblem](#aiproblem) | yes |  |
| `journeys` | array of [AIJourney](#aijourney) | yes |  |
| `startedAt` | string (date-time), nullable | no |  |
| `approvedScenarioId` | string (uuid), nullable | no |  |
| `approvedVersion` | integer, nullable | no |  |

### AIJobApprove

| Field | Type | Required | Description |
|---|---|---|---|
| `scenarioId` | string (uuid) | no | Save as a new version of this scenario |
| `message` | string | no |  |
| `allowUnvalidated` | boolean | no | Approve even though some journeys are flagged Default `false`. |

### AIJobApproval

| Field | Type | Required | Description |
|---|---|---|---|
| `scenario` | [Scenario](#scenario) | yes |  |
| `version` | integer | yes |  |

### IntegrationKind

string: `prometheus`, `traces`, `agent`

### Integration

| Field | Type | Required | Description |
|---|---|---|---|
| `id` | string (uuid) | yes |  |
| `name` | string | yes |  |
| `kind` | [IntegrationKind](#integrationkind) | yes |  |
| `url` | string | yes | Prometheus base URL, for traces a link template containing {traceId}, or a stampede agent's control API. |
| `hasToken` | boolean | yes | Whether a bearer token is stored. The token itself is never returned. |
| `createdAt` | string (date-time) | yes |  |
| `updatedAt` | string (date-time) | yes |  |

### IntegrationCreate

| Field | Type | Required | Description |
|---|---|---|---|
| `name` | string | yes |  |
| `kind` | [IntegrationKind](#integrationkind) | yes |  |
| `url` | string | yes | prometheus: the base URL, e.g. http://prometheus:9090. traces: a link template containing {traceId}, e.g. https://jaeger.example.com/trace/{traceId}. agent: a stampede agent's control API, e.g. http://agent.shop.svc:7070. |
| `bearerToken` | string | no | Prometheus (optional) and agent (required). Stored encrypted. |

### NotificationKind

string: `webhook`, `slack`, `discord`

### NotificationEvent

string: `run.finished`, `run.target_failed`, `run.killed`

### NotificationChannel

| Field | Type | Required | Description |
|---|---|---|---|
| `id` | string (uuid) | yes |  |
| `name` | string | yes |  |
| `kind` | [NotificationKind](#notificationkind) | yes |  |
| `events` | array of [NotificationEvent](#notificationevent) | yes |  |
| `allowPrivate` | boolean | yes | Deliveries may reach private, loopback and link-local addresses. |
| `urlHint` | string | yes | The destination's scheme and host only, e.g. https://hooks.slack.com |
| `hasSecret` | boolean | yes | Whether bodies are signed (generic webhooks). |
| `createdAt` | string (date-time) | yes |  |
| `lastDelivery` | [NotificationDelivery](#notificationdelivery) | no |  |

### NotificationChannelCreate

| Field | Type | Required | Description |
|---|---|---|---|
| `name` | string | yes |  |
| `kind` | [NotificationKind](#notificationkind) | yes |  |
| `url` | string | yes | Webhook URL. Stored encrypted and never returned. |
| `events` | array of [NotificationEvent](#notificationevent) | no | Default all events. |
| `allowPrivate` | boolean | no | Default `false`. |
| `secret` | string | no | Webhook signing secret. Generated when omitted; ignored for Slack and Discord. |

### NotificationChannelCreated

| Field | Type | Required | Description |
|---|---|---|---|
| `channel` | [NotificationChannel](#notificationchannel) | yes |  |
| `secret` | string | no | The webhook signing secret. Shown only once. |

### NotificationDelivery

| Field | Type | Required | Description |
|---|---|---|---|
| `id` | integer (int64) | yes |  |
| `deliveryId` | string (uuid) | yes | Same for every attempt of one delivery (X-Stampede-Delivery). |
| `event` | string | yes |  |
| `runId` | string (uuid), nullable | no |  |
| `attempt` | integer | yes |  |
| `ok` | boolean | yes |  |
| `statusCode` | integer | yes | HTTP status; 0 when no response was received. |
| `error` | string | yes |  |
| `durationMs` | integer | yes |  |
| `at` | string (date-time) | yes |  |

### RunWorkers

| Field | Type | Required | Description |
|---|---|---|---|
| `live` | boolean | yes | The run is executing on this server, so health is current. |
| `workers` | array of [RunWorkerHealth](#runworkerhealth) | yes |  |

### RunWorkerHealth

| Field | Type | Required | Description |
|---|---|---|---|
| `id` | string | yes |  |
| `name` | string | yes |  |
| `region` | string | no |  |
| `status` | string: `running`, `saturated`, `lost` | yes | lost means the worker stopped sending heartbeats during the run. |
| `saturated` | boolean | yes | The worker reports itself saturated |
| `reasons` | array of string | no | Why it is saturated, such as cpu or sched-lag. |
| `cpuPercent` | number (double) | yes | Process CPU use as a share of all the machine's cores, 0 to 100. |
| `schedLagP99` | number (double) | yes | 99th percentile of how late iterations were dispatched |
| `gcPauseP99` | number (double) | no | Seconds. |
| `dropped` | integer (int64) | no | Iterations dropped in the last sample because no virtual user was free. |
| `lastHeartbeatAt` | string (date-time), nullable | no |  |

### PackEntry

| Field | Type | Required | Description |
|---|---|---|---|
| `name` | string | yes |  |
| `title` | string | yes |  |
| `status` | string | yes | shipped: built in and tested against a reference app in CI; planned: not yet available. |
| `signature` | string | yes | What the pack's journeys and stresses cover. |
| `drivers` | array of string | yes |  |

### PackDetail

| Field | Type | Required | Description |
|---|---|---|---|
| `name` | string | yes |  |
| `title` | string | yes |  |
| `description` | string | yes |  |
| `status` | string | yes |  |
| `protocols` | array of string | yes |  |
| `referenceApp` | string | no |  |
| `variables` | array of object | yes |  |
| `files` | array of [PackFile](#packfile) | yes |  |

### PackFile

| Field | Type | Required | Description |
|---|---|---|---|
| `path` | string | yes |  |
| `kind` | string: `journey`, `stress` | yes |  |
| `scenario` | string | yes | The scenario's metadata.name. |
| `description` | string | no |  |
| `journeys` | array of string | yes |  |
| `shape` | string | no | The load shape |
| `yaml` | string | yes |  |

### CoverageRequest

| Field | Type | Required | Description |
|---|---|---|---|
| `openapi` | string | no | OpenAPI 3.x document (YAML or JSON). |
| `specURL` | string | no | Fetch the OpenAPI document from this URL; its host must be one of the project's targets or a host a target allows. Needs the editor role. |
| `version` | integer | no | Scenario version to check; default the latest. |

### CoverageEndpoint

| Field | Type | Required | Description |
|---|---|---|---|
| `method` | string | yes |  |
| `path` | string | yes |  |
| `summary` | string | no |  |
| `journeys` | array of string | yes | Journeys that call the endpoint; empty when none does. |

### RequestRef

| Field | Type | Required | Description |
|---|---|---|---|
| `journey` | string | yes |  |
| `method` | string | yes |  |
| `url` | string | yes |  |

### ScenarioCoverage

| Field | Type | Required | Description |
|---|---|---|---|
| `version` | integer | yes | The scenario version checked. |
| `endpoints` | array of [CoverageEndpoint](#coverageendpoint) | yes |  |
| `unmatched` | array of [RequestRef](#requestref) | yes | Requests that use no endpoint of the API, often a typo or a renamed endpoint. |
| `templated` | integer | yes | Requests whose whole URL is an expression |
| `covered` | integer | yes |  |
| `total` | integer | yes |  |

### DriftRequest

| Field | Type | Required | Description |
|---|---|---|---|
| `openapi` | string | no | The current OpenAPI document. |
| `specURL` | string | no | Fetch the current document from a target's host |
| `previousOpenapi` | string | no | The previous OpenAPI document |
| `previousSpecURL` | string | no |  |
| `targetId` | string (uuid) | no | Dry-run every journey once against this target (sends real requests). |
| `version` | integer | no |  |

### DriftEndpoint

| Field | Type | Required | Description |
|---|---|---|---|
| `method` | string | yes |  |
| `path` | string | yes |  |

### DriftJourneyCheck

| Field | Type | Required | Description |
|---|---|---|---|
| `journey` | string | yes |  |
| `ok` | boolean | yes |  |
| `step` | string | no | The first step that failed. |
| `error` | string | no |  |
| `status` | integer | no |  |

### ScenarioDrift

| Field | Type | Required | Description |
|---|---|---|---|
| `version` | integer | yes |  |
| `drifted` | boolean | yes | Something broke; stampede drift exits with code 4. |
| `added` | array of [DriftEndpoint](#driftendpoint) | no | Endpoints added since the previous version (only with a previous version). |
| `removed` | array of [DriftEndpoint](#driftendpoint) | no |  |
| `broken` | array of object | no | Journeys that call a removed endpoint. |
| `unmatched` | array of [RequestRef](#requestref) | yes |  |
| `dryRun` | array of [DriftJourneyCheck](#driftjourneycheck) | no | Set when targetId was given. |

### SSOSettings

| Field | Type | Required | Description |
|---|---|---|---|
| `enabled` | boolean | yes |  |
| `passwordLogin` | boolean | yes | Email and password sign-in is available too. |
| `name` | string | no | Label of the sign-in button. |
| `issuer` | string | no |  |
| `redirectURL` | string | no |  |
| `allowedDomains` | array of string | yes | Email domains allowed to sign in; empty means any. |
| `defaultRole` | string | no | Role given on first sign-in; empty means only existing accounts may sign in. |
| `scopes` | array of string | yes |  |

### LimitCaps

| Field | Type | Required | Description |
|---|---|---|---|
| `maxRate` | number (double) | no | Iterations per second; absent means no cap. |
| `maxVUs` | integer | no |  |
| `maxDurationSeconds` | integer | no |  |

### LimitSettings

| Field | Type | Required | Description |
|---|---|---|---|
| `server` | [LimitCaps](#limitcaps) | yes |  |
| `unverifiedPublic` | [LimitCaps](#limitcaps) | yes |  |
| `abortFloor` | object, nullable | no | Stops any run whose target is clearly failing, even when its scenario sets no abort limits. |
| `abortFloor.errorRate` | number (double) | no |  |
| `abortFloor.p95Seconds` | number (double) | no |  |
| `abortFloor.forSeconds` | number (double) | yes |  |
| `targets` | array of [TargetLimits](#targetlimits) | yes |  |

### TargetLimits

| Field | Type | Required | Description |
|---|---|---|---|
| `id` | string (uuid) | yes |  |
| `name` | string | yes |  |
| `projectId` | string (uuid) | yes |  |
| `projectName` | string | yes |  |
| `baseURL` | string | yes |  |
| `private` | boolean | yes |  |
| `verified` | boolean | yes |  |
| `caps` | [LimitCaps](#limitcaps) | yes |  |
| `effective` | [LimitCaps](#limitcaps) | yes |  |

