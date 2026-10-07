## stampede narrative

Have the server's AI provider write a summary of a finished run

### Synopsis

Ask the organisation's AI provider for a written summary of a finished
run's report. Every claim cites the report figures it rests on and is
labelled measured or suspected; claims citing unknown figures are dropped.
Only aggregate figures are sent, with error messages redacted. The
narrative is saved into the report, so stampede report and the HTML and
Markdown downloads include it. stampede report --narrative does the same
on this machine with your own key.

```
stampede narrative <run> [flags]
```

### Options

```
  -h, --help              help for narrative
      --json              print JSON for scripting
      --provider string   AI provider name or id (default: the only one, or the one named default)
```

### SEE ALSO

* [stampede](stampede.md)	 - Self-hosted, distributed load testing

