# Scheduled runs

A schedule starts a run of a saved scenario against a target at set times,
such as every night at 02:00 or at 06:30 on weekdays. Use it for regression
checks that should not depend on someone remembering to press the button,
or for soak tests that run while nobody is using staging.

Schedules belong to a project and run on the server. Each firing goes
through exactly the same checks as a run started by hand: the scenario must
still be valid, the target's caps and the server's hard caps apply, and a
public target must be verified to lift the low caps.

## Create one

In the web UI, open **Schedules** in the project and choose **New
schedule**. Pick the scenario and target, write the cron expression (or
use a preset), and the dialog shows the next three times it will fire in
the schedule's time zone. Load overrides, environment variables, the
number of workers and a note are optional.

From the command line:

```sh
stampede schedules create nightly --project shop --scenario checkout \
  --target staging --cron "0 2 * * *"

stampede schedules create weekday-soak --project shop --scenario browse \
  --target staging --cron "30 6 * * MON-FRI" --timezone Europe/London \
  --shape soak --duration 30m
```

`stampede schedules list` shows each schedule's next run and how its last
run went; `enable`, `disable`, `delete` and `run` take a schedule's name
or id. See the [CLI reference](../reference/cli/stampede_schedules.md).

When you save a schedule the server checks it in full, the same way it
would check the run if it fired now, so a wrong override or a cap that
would refuse the run is reported straight away rather than at 02:00.

A schedule always runs the scenario's latest version.

## Cron expressions

Five fields, separated by spaces:

```
minute  hour  day-of-month  month  day-of-week
0-59    0-23  1-31          1-12   0-7 (0 and 7 are Sunday)
```

Each field takes `*`, a number, a range (`1-5`), a list (`1,15`) and a
step (`*/15`, `10-50/20`, or `5/15` for 5, 20, 35 and 50). Months and days
of the week also take three-letter English names (`JAN`, `MON-FRI`). The
macros `@hourly`, `@daily` (or `@midnight`), `@weekly`, `@monthly` and
`@yearly` (or `@annually`) are accepted too.

| Expression | Fires |
|---|---|
| `0 2 * * *` | 02:00 every day |
| `*/30 * * * *` | every 30 minutes |
| `30 6 * * MON-FRI` | 06:30 on weekdays |
| `0 3 1 * *` | 03:00 on the first of each month |
| `0 0 * * 0` | midnight every Sunday |

As in standard cron, when both day of month and day of week are given
(neither starts with `*`), a day matches if either does: `0 12 13 * FRI`
fires on the 13th and on every Friday. An expression that never fires,
such as `0 0 30 2 *`, is refused.

## Time zones

Times are UTC unless you give an IANA time zone name such as
`Europe/London` or `America/New_York`. With a zone, the expression follows
that zone's local clock, including daylight saving time:

- A time that does not exist because the clocks go forward fires as much
  later as the clocks jumped. In New York, `30 2 * * *` fires at 03:30 on
  the day 02:00 becomes 03:00.
- A time that happens twice because the clocks go back fires once, the
  first time.

The web UI and `GET /schedules/preview` show the next firings, which is
the quickest way to check an expression.

## When a schedule fires

The server looks for due schedules every 15 seconds (`--scheduler-interval`;
see [configuration](../reference/configuration.md)), so a run starts up to
that long after its time. Each run's note is `scheduled: <name>`, so it is
easy to pick out in the runs list, and the audit log records it as started
by the schedule's owner, for example `ana@example.com (schedule:nightly)`.

A firing is skipped, and the reason shown on the schedule, when:

- the schedule's previous run is still in progress (a slow soak never
  overlaps the next one);
- the schedule's owner has left the organisation or no longer has the
  runner role;
- the run is no longer accepted, for example because the scenario was
  changed and is now invalid, or the target's caps were lowered.

Skips for the last two reasons are also written to the audit log, with
`scheduler` as the actor. The next firing is tried as normal.

If the server was down when a schedule was due, it fires once when the
server starts again, not once for every time it missed. Only the active
server replica fires schedules, and each firing is claimed with a single
conditional database update, so a firing cannot start two runs even if
two replicas or two checks race for it. The flip side is that a firing
claimed by a server that then crashes before starting the run is lost; the
next one runs normally.

## Who can do what

| | Role |
|---|---|
| List and read schedules | any |
| Start a schedule's run now | runner and above |
| Create, change, enable, disable and delete | editor and above |

A schedule's runs start as its owner: whoever created it or last changed
it. If the owner leaves or loses the runner role, the schedule is skipped
until an editor saves it (enabling it is enough), which makes them the
owner. **Run now** starts the run as you, and does not change when the
schedule next fires.

Environment variables are stored with the schedule and anyone in the
project can read them, unlike a run's, whose values are not kept. Put
credentials in the project's secrets (**Secrets** in the web UI, read in a
scenario as `${secret.NAME}`) instead.

Deleting a schedule keeps the runs it started. Deleting its scenario or
target deletes the schedule.

## API

All endpoints are under the `schedules` tag in the
[OpenAPI document](../../api/openapi.yaml).

| Method and path | |
|---|---|
| `GET /projects/{id}/schedules` | list, with next run and last run status and verdict |
| `POST /projects/{id}/schedules` | create `{name, scenarioId, targetId, cron, timezone?, overrides?, env?, workers?, enabled?, note?}` |
| `GET /schedules/{id}` | one schedule |
| `PATCH /schedules/{id}` | change any of the fields above; `{"enabled": false}` disables it |
| `DELETE /schedules/{id}` | delete |
| `POST /schedules/{id}/run` | start its run now; `409` while its previous run is active |
| `GET /schedules/preview?cron=&timezone=&count=` | the next firings of an expression (default 3, at most 20) |

```sh
curl -s -H "Authorization: Bearer $STAMPEDE_TOKEN" -H "Content-Type: application/json" \
  -X POST "$STAMPEDE_SERVER/api/v1/projects/$PROJECT/schedules" \
  -d '{"name":"nightly","scenarioId":"...","targetId":"...","cron":"0 2 * * *"}'
```

A schedule's `lastSkipReason` is empty when its last firing started a run
and explains why not otherwise; `lastRunId`, `lastRunStatus` and
`lastRunVerdict` describe the last run it started.
