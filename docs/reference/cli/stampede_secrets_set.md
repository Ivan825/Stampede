## stampede secrets set

Create or replace a secret

### Synopsis

Store a secret. The value is read from the environment variable named by
--from-env, or from stdin when it is piped, or asked without echo. It is
never a flag value and never printed, so it stays out of shell history and
logs.

```
stampede secrets set <NAME> [flags]
```

### Examples

```
  stampede secrets set SHOP_PASSWORD                      # asks for the value
  stampede secrets set API_KEY --from-env STAGING_API_KEY
  vault read -field=key secret/shop | stampede secrets set API_KEY
```

### Options

```
      --from-env string   read the value from this environment variable
  -h, --help              help for set
```

### Options inherited from parent commands

```
      --project string   project name, slug or id (default: the default or only project)
```

### SEE ALSO

* [stampede secrets](stampede_secrets.md)	 - List, set and delete a project's secrets

