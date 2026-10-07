## stampede pack create

Scaffold a new pack folder with a journey, a stress, targets and data

### Synopsis

Creates <dir>/<name> with the layout of the built-in packs: pack.yaml,
README.md, journeys/, stresses/, targets.yaml and data/. The journey and the
stress request the target's home page and are valid scenarios from the
start; edit them to follow your product's API, then dry-run them with
stampede pack test <dir>/<name> --target <url>. See docs/guides/packs.md.

```
stampede pack create <name> [flags]
```

### Examples

```
  stampede pack create fintech
  stampede pack test fintech --target http://localhost:8080
```

### Options

```
      --dir string   folder to create the pack in (default ".")
  -h, --help         help for create
```

### SEE ALSO

* [stampede pack](stampede_pack.md)	 - List, install, test and create product packs

