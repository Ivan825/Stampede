# Open and closed load models

Every scenario chooses one in `load.mode`.

## Closed model: `mode: vus`

A fixed population of virtual users loops through journeys. When the
system slows down, each user waits longer for each response, so fewer
requests are sent. This models a known number of concurrent sessions, such
as staff using an internal tool, but it hides overload: a slow system
receives less traffic and looks healthier than it is.

```yaml
load: { mode: vus, vus: 50, duration: 10m }
```

## Open model: `mode: rate`

New iterations start on a fixed schedule whatever the system does, the way
real public traffic arrives. If no virtual user is free when an iteration is
due, Stampede starts another one, up to `maxVUs`. Beyond that the iteration
is recorded as **dropped**, which shows the generator could not keep the
schedule.

```yaml
load: { mode: rate, rate: 200/s, duration: 10m, maxVUs: 2000 }
```

Use the open model for anything public facing, and always for breakpoint,
spike and stress tests.

## Shapes and stages

Either list stages yourself (each ramps linearly to its `target`):

```yaml
load:
  mode: rate
  stages:
    - { duration: 2m, target: 100/s }
    - { duration: 10m, target: 100/s }
    - { duration: 2m, target: 0/s }
```

or pick a [shape](../guides/test-types.md) such as `shape: spike` with
`start` and `max`. `iterations: 500` runs a fixed number of iterations
instead.

## How the schedule is kept

The open model computes the exact intended start time of every arrival from
the plan (for a linear ramp, by solving for when the integral of the rate
reaches the arrival's number), so there is no drift from timer rounding.
Over a test, the started count matches the plan exactly: 200/s for 2 seconds
starts exactly 400 iterations. Across workers, each worker takes a
non-overlapping share of arrivals, and the shares add up to the plan exactly.
