## stampede caps set

Change the organisation's caps (admin)

```
stampede caps set [flags]
```

### Examples

```
  stampede caps set --max-rate 1000 --max-vus 5000 --max-duration 2h
  stampede caps set --max-vus 0      # remove the VU cap
  stampede caps set --clear          # remove every cap
```

### Options

```
      --clear                   remove every organisation cap
  -h, --help                    help for set
      --json                    print JSON for scripting
      --max-duration duration   cap on a run's duration, e.g. 30m (0 removes it)
      --max-rate float          cap on the arrival rate, iterations per second (0 removes it)
      --max-vus int             cap on virtual users (0 removes it)
```

### SEE ALSO

* [stampede caps](stampede_caps.md)	 - Show or set the organisation's hard caps on every run

