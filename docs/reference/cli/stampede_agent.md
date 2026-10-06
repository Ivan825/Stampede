## stampede agent

Inject faults into dependencies during a load test

### Synopsis

Run the fault-injection agent inside your environment, next to the
dependencies you want to break on purpose.

Each --proxy puts a TCP proxy in front of a dependency. Point your app at the
proxy instead of the dependency; while no fault is active it forwards traffic
untouched. A scenario's faults block (or the control API) then adds latency,
jitter or a bandwidth cap, resets connections, refuses new ones or stops
forwarding, for a set time.

With explicit permission the agent also pauses, stops, kills or restarts
Docker containers (--allow-container) and scales Kubernetes deployments
(--allow-deployment). Name each one; globs are allowed.

Every fault has a duration of at most --max-duration and is reverted when it
ends, when it is cleared (the run ends or the kill switch is used), and when
the agent stops. Every action is written to the audit log.

The control API needs the token from --token or STAMPEDE_AGENT_TOKEN.

```
stampede agent [flags]
```

### Examples

```
  STAMPEDE_AGENT_TOKEN=$(openssl rand -hex 16) stampede agent \
    --proxy db=:15432=postgres:5432 --proxy cache=:16379=redis:6379
  stampede agent --proxy api=:18080=payments.internal:80 --allow-container 'shop-*'
```

### Options

```
      --allow-container stringArray    Docker container (name or glob) the agent may pause, stop, kill or restart (repeatable)
      --allow-deployment stringArray   Kubernetes deployment (namespace/name or glob) the agent may scale (repeatable)
      --audit-file string              also append the audit log (JSON lines) to this file
      --docker-host string             Docker Engine API (default "unix:///var/run/docker.sock")
  -h, --help                           help for agent
      --kubernetes-api string          Kubernetes API URL, e.g. a kubectl proxy (default: the in-cluster service account)
      --listen string                  address of the control API (default ":7070")
      --max-duration duration          longest fault allowed (default 30m0s)
      --proxy stringArray              NAME=LISTEN=UPSTREAM, e.g. db=:15432=postgres:5432 (repeatable)
      --token string                   control API token (default $STAMPEDE_AGENT_TOKEN)
```

### SEE ALSO

* [stampede](stampede.md)	 - Self-hosted, distributed load testing

