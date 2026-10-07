## stampede report

Render a saved JSON report as HTML, PDF, CSV, JUnit, Markdown or a summary

### Synopsis

Render a JSON report (from stampede run --json) as a summary or as HTML,
PDF, CSV, JUnit or Markdown files. The argument is a file or, when no such
file exists, the id of a run on the server you signed in to (a unique
prefix of the id is enough), whose report is downloaded.

```
stampede report <report.json | run-id> [flags]
```

### Examples

```
  stampede report report.json -o report.html
  stampede report 3f2a91c0 --pdf run.pdf
```

### Options

```
      --ai-base-url string    provider API base URL for --narrative
      --csv string            write per-step figures as CSV to this file (- for stdout)
  -h, --help                  help for report
      --json string           write the JSON report (with any narrative) to this file (- for stdout)
      --junit string          write JUnit XML to this file
      --md string             write Markdown to this file (- for stdout)
      --model string          model for --narrative (default claude-sonnet-5-5 for anthropic)
      --narrative             add an AI-written summary; every claim cites the report's figures (needs a provider key)
  -o, --out string            write HTML to this file
      --pdf string            write a PDF report to this file (needs Chrome or Chromium)
      --provider string       model provider for --narrative: anthropic, openai, gemini, ollama or openai-compatible (default "anthropic")
      --timeline-csv string   write the per-second timeline as CSV to this file (- for stdout)
```

### SEE ALSO

* [stampede](stampede.md)	 - Self-hosted, distributed load testing

