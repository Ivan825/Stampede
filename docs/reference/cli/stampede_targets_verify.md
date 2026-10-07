## stampede targets verify

Prove you own a public target so higher load is allowed

### Synopsis

Public targets run under low caps until you prove ownership by publishing a
token either as a DNS TXT record (stampede-verify=<token>) on the host or
on _stampede.<host>, or at /.well-known/stampede-verify.txt. Private and
loopback targets need no verification.

Signed in to a server, this checks the token of one of the project's
targets (by name, base URL or id) on the server and prints how to publish
it while it is missing. Given a URL that is not a target on the server, or
not signed in, or with --local, it checks this machine's token instead,
which is what stampede run uses.

```
stampede targets verify <target> [flags]
```

### Examples

```
  stampede targets verify staging
  stampede target verify https://staging.example.com --local
```

### Options

```
  -h, --help    help for verify
      --json    print JSON for scripting
      --local   check this machine's token for <url> (for stampede run) instead of a server target
```

### Options inherited from parent commands

```
      --project string   project name, slug or id (default: the default or only project)
```

### SEE ALSO

* [stampede targets](stampede_targets.md)	 - List and manage a project's targets, and prove you own them

