## stampede scenarios validate

Check scenario files the way the server will

### Synopsis

Send scenario files to the server's validator, which knows the plugins
installed on the server and its workers, and print each file's problems
or its load plan. Nothing is saved. stampede validate checks files on this
machine instead.

```
stampede scenarios validate <scenario.yaml>... [flags]
```

### Options

```
  -h, --help   help for validate
      --json   print JSON for scripting
```

### Options inherited from parent commands

```
      --project string   project name, slug or id (default: the default or only project)
```

### SEE ALSO

* [stampede scenarios](stampede_scenarios.md)	 - List, show and delete the scenarios saved on the server

