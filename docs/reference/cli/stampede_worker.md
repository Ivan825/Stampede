## stampede worker

Run a load-generating worker for a Stampede server

### Synopsis

Run a worker that connects out to a Stampede server and generates its share
of every distributed run the server assigns. Only an outbound connection is
needed, so workers run fine behind NAT and firewalls.

The worker reconnects on its own if the connection drops. If it loses the
server for 10 seconds during a run it stops generating load by itself.

With --mtls (for servers started with --worker-mtls) the worker enrolls for
its own certificate from the server's built-in CA, proving it knows the join
token without sending it, and connects with that certificate. Pin the CA
with --ca-fingerprint, as logged by the server.

The join token can also be given in STAMPEDE_JOIN_TOKEN, which keeps it out
of the process list.

```
stampede worker [flags]
```

### Examples

```
  stampede worker --server stampede.internal:8081 --mtls --ca-fingerprint sha256:9f2c... --region mumbai
  stampede worker --server stampede.internal:7443 --token $TOKEN --region mumbai
  stampede worker --server 10.0.0.5:7443 --insecure --label pool=spot --max-vus 2000
```

### Options

```
      --ca string               PEM file of the CA that signed the server certificate (default the system roots)
      --ca-fingerprint string   with --mtls, the server CA's fingerprint (sha256:...); without it the CA is trusted on first enrollment, authenticated by the join token
  -h, --help                    help for worker
      --insecure                connect without TLS (trusted networks only)
      --label stringArray       extra label KEY=VALUE (repeatable)
      --max-vus int             most virtual users this worker accepts (0 = no limit)
      --mtls                    enroll with the server's built-in CA and connect with this worker's own certificate (server started with --worker-mtls)
      --name string             worker name (default the host name)
      --region string           region label used to split load by region
      --server string           server address, host:port (required)
      --token string            join token (default $STAMPEDE_JOIN_TOKEN)
  -v, --verbose                 debug logging
```

### SEE ALSO

* [stampede](stampede.md)	 - Self-hosted, distributed load testing

