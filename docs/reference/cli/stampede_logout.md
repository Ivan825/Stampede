## stampede logout

Revoke this CLI's API token and forget it

### Synopsis

Revoke the API token stampede login stored, on the server, and remove it
from the config file. With --keep-token the token is only forgotten here
and stays valid. A token set in STAMPEDE_TOKEN is revoked as well; unset
the variable afterwards.

```
stampede logout [flags]
```

### Options

```
  -h, --help         help for logout
      --keep-token   only forget the token here; it stays valid on the server
```

### SEE ALSO

* [stampede](stampede.md)	 - Self-hosted, distributed load testing

