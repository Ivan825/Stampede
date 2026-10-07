## stampede tokens create

Create an API token and print its secret once

### Synopsis

Create an API token. API tokens cannot create other tokens, so this signs
in with your password (STAMPEDE_PASSWORD, or asked without echo). The
secret is printed once on stdout; store it now, it cannot be shown again.

```
stampede tokens create <name> [flags]
```

### Examples

```
  stampede tokens create ci --role runner --expires-in-days 90
  export STAMPEDE_TOKEN=$(stampede tokens create nightly --role runner)
```

### Options

```
      --expires-in-days int   expire the token after this many days (default never)
  -h, --help                  help for create
      --json                  print JSON for scripting
      --role string           the token's role, at most yours (default yours): owner, admin, editor, runner or viewer
```

### SEE ALSO

* [stampede tokens](stampede_tokens.md)	 - List, create and revoke your API tokens

