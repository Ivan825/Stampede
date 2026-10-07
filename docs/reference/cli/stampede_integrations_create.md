## stampede integrations create

Add an integration

### Synopsis

Add a named integration. A bearer token (optional for Prometheus,
required for an agent) is read from the environment variable named by
--token-env or from stdin with --token-stdin; it is stored encrypted.

```
stampede integrations create <name> [flags]
```

### Examples

```
  stampede integrations create prom --kind prometheus --url http://prometheus:9090
  stampede integrations create jaeger --kind traces --url "https://jaeger.example.com/trace/{traceId}"
  stampede integrations create shop-agent --kind agent --url http://agent.shop.svc:7070 --token-env AGENT_TOKEN
```

### Options

```
  -h, --help               help for create
      --json               print JSON for scripting
      --kind string        prometheus, traces or agent
      --token-env string   read the bearer token from this environment variable
      --token-stdin        read the bearer token from stdin
      --url string         Prometheus base URL, trace link template with {traceId}, or agent control API URL
```

### SEE ALSO

* [stampede integrations](stampede_integrations.md)	 - List, add and delete Prometheus, trace and agent integrations (admin)

