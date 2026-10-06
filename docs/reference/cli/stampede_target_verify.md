## stampede target verify

Prove you own a public target so higher load is allowed

### Synopsis

Public targets run under low caps until you prove ownership. This prints a
token and checks whether it is published either as a DNS TXT record
(stampede-verify=<token>) on the host or on _stampede.<host>, or at
/.well-known/stampede-verify.txt. Private and loopback targets need no
verification.

```
stampede target verify <url> [flags]
```

### Options

```
  -h, --help   help for verify
```

### SEE ALSO

* [stampede target](stampede_target.md)	 - Manage load test targets

