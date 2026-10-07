## stampede integrations

List, add and delete Prometheus, trace and agent integrations (admin)

### Synopsis

Scenarios run on the server refer to integrations by name, such as
observe: { prometheus: { integration: prom, queries: ... } }, so the server
only contacts URLs an admin configured here. A prometheus integration is a
Prometheus base URL; traces is a link template containing {traceId}; agent
is a stampede agent's control API, for fault injection. Bearer tokens are
encrypted and never shown.

### Options

```
  -h, --help   help for integrations
```

### SEE ALSO

* [stampede](stampede.md)	 - Self-hosted, distributed load testing
* [stampede integrations create](stampede_integrations_create.md)	 - Add an integration
* [stampede integrations delete](stampede_integrations_delete.md)	 - Delete an integration
* [stampede integrations list](stampede_integrations_list.md)	 - List integrations (tokens are never shown)

