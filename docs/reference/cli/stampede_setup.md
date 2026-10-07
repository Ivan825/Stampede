## stampede setup

Create the first organisation and owner account on a new server

### Synopsis

Set up a server that has no accounts yet: create the organisation and its
owner, then sign in as the owner and store an API token for this CLI, as
stampede login does. Questions you leave out as flags are asked. The
password is read from STAMPEDE_PASSWORD or asked without echo; it is
never a flag, so it stays out of shell history. It needs at least ten
characters.

```
stampede setup [flags]
```

### Examples

```
  stampede setup --server http://localhost:8080 --organisation Acme --name "Ada Lovelace" --email ada@acme.test
  STAMPEDE_PASSWORD=... stampede setup --organisation Acme --name Ada --email ada@acme.test
```

### Options

```
      --email string          your email, used to sign in
  -h, --help                  help for setup
      --name string           your name
      --organisation string   organisation name
      --server string         server URL (default http://localhost:8080)
```

### SEE ALSO

* [stampede](stampede.md)	 - Self-hosted, distributed load testing

