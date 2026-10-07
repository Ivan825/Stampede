## stampede scenarios show

Show a scenario, or print one version's YAML

### Synopsis

Without flags, summarise a scenario and its latest version. With --yaml,
print the latest version's YAML; with --version N, print version N's YAML,
so stampede scenarios show checkout --version 3 > checkout.yaml restores
an old version locally.

```
stampede scenarios show <scenario> [flags]
```

### Options

```
  -h, --help          help for show
      --json          print JSON for scripting
      --version int   print this version's YAML
      --yaml          print the latest version's YAML
```

### Options inherited from parent commands

```
      --project string   project name, slug or id (default: the default or only project)
```

### SEE ALSO

* [stampede scenarios](stampede_scenarios.md)	 - List, show and delete the scenarios saved on the server

