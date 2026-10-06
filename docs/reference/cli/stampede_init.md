## stampede init

Detect what kind of product a target is and set up the matching pack

### Synopsis

init probes the target (its OpenAPI document at common paths, its OpenID
Connect discovery document, its home page and response headers), scores
the shipped product packs against what it finds, asks you to confirm,
installs the pack into ./stampede and dry-runs its journeys once against
the target.

```
stampede init [flags]
```

### Options

```
      --dir string      folder to install the pack into (default "stampede")
  -h, --help            help for init
      --target string   base URL of the system to test
  -y, --yes             do not ask for confirmation
```

### SEE ALSO

* [stampede](stampede.md)	 - Self-hosted, distributed load testing

