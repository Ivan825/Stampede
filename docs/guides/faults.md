# Fault injection

Load tests usually assume every dependency is healthy. Production rarely
is. `stampede agent` breaks dependencies on purpose while load runs, so you
can see what your users would see when the database slows down, the cache
drops connections or a service disappears, and whether the system recovers
when the fault ends.

## How it works

The agent runs inside your environment, next to the dependencies. It
puts a TCP proxy in front of each one; you point your app at the proxy
instead of the dependency. With no fault active the proxy forwards traffic
untouched.

A scenario's `faults` block lists what to break and when, measured from the
start of load. Stampede asks the agent to apply each fault on time, and the
report shades the fault windows on its timeline charts so cause and effect
line up.

Safety rules:

- Every fault has a duration of at most the agent's `--max-duration`
  (default 30 minutes). The agent reverts it when the duration ends, even if
  Stampede has gone away.
- When a run ends, stops or is killed, Stampede clears all of its faults.
  Stopping the agent clears everything too.
- Containers and deployments are touched only if they are named on the
  agent's command line (`--allow-container`, `--allow-deployment`; globs are
  allowed).
- The control API needs a token. Every action and revert is written to the
  agent's JSON audit log (`--audit-file` keeps a copy).
- Before any load is sent, Stampede checks that the agent is reachable and
  has every proxy and permission the timeline needs. If not, the run does
  not start.

## Run the agent

```sh
export STAMPEDE_AGENT_TOKEN=$(openssl rand -hex 16)
stampede agent \
  --proxy db=:15432=postgres:5432 \
  --proxy cache=:16379=redis:6379 \
  --allow-container 'shop-search*' \
  --audit-file /var/log/stampede-agent.jsonl
```

Point the app at the proxies, for example `DATABASE_URL=postgres://...@agent:15432/shop`.

| Flag | Meaning |
|---|---|
| `--proxy NAME=LISTEN=UPSTREAM` | a TCP proxy (repeatable) |
| `--listen` | control API address (default `:7070`) |
| `--token` | control API token (default `$STAMPEDE_AGENT_TOKEN`, at least 16 characters) |
| `--max-duration` | longest fault allowed (default 30m) |
| `--allow-container` | Docker containers it may pause, stop, kill or restart |
| `--docker-host` | Docker Engine API (default `unix:///var/run/docker.sock`) |
| `--allow-deployment` | Kubernetes deployments (`namespace/name`) it may scale |
| `--kubernetes-api` | API server URL, e.g. a `kubectl proxy`; default the in-cluster service account |
| `--audit-file` | also append the audit log to this file |

In Kubernetes, the agent's service account needs `get` and `patch` on
`deployments/scale` for the deployments it may scale.

## Describe faults in a scenario

```yaml
faults:
  agent:
    url: ${env.AGENT_URL}          # stampede run
    token: ${secret.AGENT_TOKEN}
    # integration: chaos           # on a server, instead of url and token
  timeline:
    - name: slow database
      at: 1m                       # after load starts
      for: 1m
      proxy: db
      latency: 200ms               # added to each direction
      jitter: 50ms
    - at: 2m30s
      for: 30s
      proxy: cache
      reset: true                  # reset open and new connections
    - at: 3m30s
      for: 20s
      container: shop-search
      action: pause                # pause, stop, kill or restart
    - at: 4m
      for: 30s
      deployment: shop/api
      replicas: 1
```

| Proxy fault | Effect |
|---|---|
| `latency`, `jitter` | added delay in each direction (bytes are never reordered) |
| `bandwidth` | per connection and direction, in bits per second (`1mbps`, `768kbps`) |
| `reset` | resets open connections when the fault starts, and new ones while it lasts |
| `refuse` | resets new connections; open ones carry on |
| `blackhole` | stops forwarding, so requests hang until they time out |

Container faults are undone when they end: a paused container is unpaused,
a stopped or killed one is started again. A deployment is scaled back to
the replica count it had before.

## With stampede run

```sh
AGENT_URL=http://agent.staging:7070 STAMPEDE_SECRET_AGENT_TOKEN=... \
  stampede run checkout-under-faults.yaml -o report.html
```

## On a server

The server only contacts URLs an admin configured. An admin adds the agent
as a fault agent integration with `stampede integrations create`, or with
`POST /api/v1/integrations` `{"name": "chaos", "kind": "agent", "url":
"http://agent.shop.svc:7070", "bearerToken": "..."}`. The token is stored
encrypted. Scenarios then name it:

```yaml
faults:
  agent: { integration: chaos }
  timeline: [...]
```

The server injects the faults itself, also for distributed runs, and starts
the timeline when the workers start load. The `faults` block is never sent
to workers.

## In the report

The report lists every fault with the window it was active in and whether
it was applied and reverted. The throughput, latency, error and target
metric charts shade those windows. An AI narrative can cite them
(`fault.0`, `fault.1`, ...).

## Limitations

- Faults act at the TCP level through the agent's proxies. Faults inside a
  protocol (wrong responses, specific error codes) are not supported.
- Packet loss and jitter for virtual users themselves are emulated by
  [`target.network`](test-types.md), not by the agent.
- Docker and Kubernetes actions are tested against fake APIs in this
  repository's CI, not against a real Docker daemon or cluster.
