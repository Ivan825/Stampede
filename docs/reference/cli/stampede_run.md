## stampede run

Run a scenario in-process and write a report (no server needed)

### Synopsis

Run a scenario with an in-process engine. Nothing else needs to be running:
no server, no database. Prints a summary and can write HTML, JSON, JUnit
and Markdown reports.

Exit codes: 0 when every target passes (or none are set), 3 when a target
fails, 1 on any other error.

```
stampede run <scenario.yaml> [flags]
```

### Examples

```
  stampede run checkout.yaml
  stampede run checkout.yaml --shape spike --rate 200/s -o report.html
  stampede run smoke.yaml -e TARGET_URL=http://localhost:8090 --junit junit.xml
```

### Options

```
      --ai-base-url string   provider API base URL for --narrative
      --allow-host strings   extra public hosts requests may reach besides the target
      --base-url string      override target.baseURL
      --duration string      override the duration, e.g. 30s or 5m
  -e, --env stringArray      set ${env.KEY} for the scenario (KEY=VALUE, repeatable)
  -h, --help                 help for run
      --iterations int       run a fixed number of iterations instead
      --json string          write a JSON report to this file (- for stdout)
      --junit string         write JUnit XML (one test per target) to this file
      --md string            write a Markdown summary to this file (- for stdout)
      --model string         model for --narrative (default claude-sonnet-5-5 for anthropic)
      --narrative            add an AI-written summary; every claim cites the report's figures (needs a provider key)
  -o, --out string           write an HTML report to this file
      --pause string         pause between repeats (default "10s")
      --provider string      model provider for --narrative: anthropic, openai, gemini, ollama or openai-compatible (default "anthropic")
  -q, --quiet                no live progress
      --rate string          override the arrival rate, e.g. 100/s (switches to rate mode)
      --repeat int           run the scenario this many times (for stampede compare); report files get -1, -2 ... suffixes (default 1)
      --shape string         apply a traffic shape: smoke, baseline, stress, spike, soak, breakpoint, steps, recovery, wave
  -v, --verbose              log step errors as they happen
      --vus int              override the number of virtual users
```

### SEE ALSO

* [stampede](stampede.md)	 - Self-hosted, distributed load testing

