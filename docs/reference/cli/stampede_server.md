## stampede server

Run the control plane: REST API, run manager and web UI

### Synopsis

Run the Stampede control plane. It needs PostgreSQL (TimescaleDB recommended)
and applies database migrations on start.

Environment:
  STAMPEDE_DATABASE_URL     postgres://user:pass@host:5432/stampede
  STAMPEDE_MASTER_KEY       32 random bytes, base64, for encrypting secrets
                            (generate one with: stampede keygen)
  STAMPEDE_MASTER_KEY_FILE  or read the key from a file

```
stampede server [flags]
```

### Options

```
      --abort-errors string      stop any run whose error rate stays at or above this (0 disables) (default "90%")
      --abort-for duration       how long --abort-errors must hold before a run is stopped (default 30s)
      --addr string              listen address (default ":8080")
      --data-dir string          directory holding CSV/JSON feeder files for runs
      --database-url string      PostgreSQL URL
      --executor string          auto (workers when connected, else in-process), workers or local (default "auto")
  -h, --help                     help for server
      --join-token string        secret workers present to join (required for workers)
      --log-format string        json or text (default "json")
      --log-level string         debug, info, warn or error (default "info")
      --max-duration duration    hard cap on run duration (0 = none)
      --max-rate float           hard cap on arrival rate for every run (0 = none)
      --max-vus int              hard cap on virtual users for every run (0 = none)
      --migrate-dry-run          report pending migrations and exit
      --migrate-only             apply migrations and exit
      --secure-cookies           mark session cookies Secure (use behind HTTPS)
      --trusted-proxy strings    CIDR of a reverse proxy whose X-Forwarded-For is trusted (repeatable)
      --worker-addr string       gRPC address workers connect to (empty disables workers) (default ":8081")
      --worker-tls-cert string   TLS certificate for the worker port
      --worker-tls-key string    TLS key for the worker port
```

### SEE ALSO

* [stampede](stampede.md)	 - Self-hosted, distributed load testing

