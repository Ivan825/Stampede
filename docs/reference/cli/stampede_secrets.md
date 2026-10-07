## stampede secrets

List, set and delete a project's secrets

### Synopsis

Secrets are values such as passwords and API keys that scenarios use as
${secret.NAME}. They are encrypted at rest with the server's master key,
never returned by the API and never printed here. Names are identifiers
(letters, digits and underscores).

### Options

```
  -h, --help             help for secrets
      --project string   project name, slug or id (default: the default or only project)
```

### SEE ALSO

* [stampede](stampede.md)	 - Self-hosted, distributed load testing
* [stampede secrets delete](stampede_secrets_delete.md)	 - Delete a secret
* [stampede secrets list](stampede_secrets_list.md)	 - List a project's secret names (values are never shown)
* [stampede secrets set](stampede_secrets_set.md)	 - Create or replace a secret

