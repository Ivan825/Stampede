## stampede run

Run a scenario in-process and write a report (no server needed)

### Synopsis

Run a scenario with an in-process engine. Nothing else needs to be running:
no server, no database. Prints a summary and can write HTML, PDF, CSV,
JSON, JUnit and Markdown reports.

With --cluster the scenario runs on the workers of the server you signed
in to with stampede login instead: the file is saved to the server as a
new version, the run is started against a target saved on the server
(--target, or the one whose base URL is --base-url, or the project's only
target) and followed live, and the server's report is written to the same
outputs. This is the same as stampede start --file.

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
  stampede run checkout.yaml --cluster --project shop --target staging --duration 10m -o report.html
```

### Options

```
      --ai-base-url string    provider API base URL for --narrative
      --allow-host strings    extra public hosts requests may reach besides the target
      --base-url string       override target.baseURL
      --cluster               run on the signed-in server's workers instead of in-process
      --csv string            write per-step figures as CSV to this file (- for stdout)
  -d, --detach                with --cluster: print the run id and return without following
      --duration string       override the duration, e.g. 30s or 5m
  -e, --env stringArray       set ${env.KEY} for the scenario (KEY=VALUE, repeatable)
  -h, --help                  help for run
      --iterations int        run a fixed number of iterations instead
      --json string           write a JSON report to this file (- for stdout)
      --junit string          write JUnit XML (one test per target) to this file
      --max-vus int           cap on virtual users in rate mode (default: five times the peak rate); raise it for long journeys
      --md string             write a Markdown summary to this file (- for stdout)
      --model string          model for --narrative (default claude-sonnet-5-5 for anthropic)
      --narrative             add an AI-written summary; every claim cites the report's figures (needs a provider key)
      --note string           with --cluster: note recorded with the run
  -o, --out string            write an HTML report to this file
      --pause string          pause between repeats (default "10s")
      --pdf string            write a PDF report to this file (needs Chrome or Chromium)
      --project string        with --cluster: project name, slug or id (default: the only project)
      --provider string       model provider for --narrative: anthropic, openai, gemini, ollama or openai-compatible (default "anthropic")
  -q, --quiet                 no live progress
      --rate string           override the arrival rate, e.g. 100/s (switches to rate mode)
      --region stringArray    with --cluster: split the load by worker region, REGION=PERCENT (repeatable, adds up to 100%), e.g. --region mumbai=50% --region frankfurt=50%; replaces load.regions
      --repeat int            run the scenario this many times (for stampede compare); report files get -1, -2 ... suffixes (default 1)
      --shape string          apply a traffic shape: smoke, baseline, stress, spike, soak, breakpoint, steps, recovery, wave
      --target string         with --cluster: target name, base URL or id saved on the server
      --timeline-csv string   write the per-second timeline as CSV to this file (- for stdout)
  -v, --verbose               log step errors as they happen
      --vus int               override the number of virtual users
      --workers int           with --cluster: number of workers (0 = all)
```

### SEE ALSO

* [stampede](stampede.md)	 - Self-hosted, distributed load testing

