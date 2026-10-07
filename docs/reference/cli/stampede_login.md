## stampede login

Sign in to a Stampede server and store an API token for this CLI

### Synopsis

Sign in with your email and password and store an API token for this
CLI in the config file, so later commands need no credentials. The
password is read from STAMPEDE_PASSWORD or asked without echo. For CI,
set STAMPEDE_SERVER and STAMPEDE_TOKEN instead (stampede tokens create).

```
stampede login [flags]
```

### Options

```
      --email string    account email
  -h, --help            help for login
      --server string   server URL (default http://localhost:8080)
```

### SEE ALSO

* [stampede](stampede.md)	 - Self-hosted, distributed load testing

