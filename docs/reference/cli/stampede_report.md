## stampede report

Render a saved JSON report as HTML, JUnit, Markdown or a summary

```
stampede report <report.json> [flags]
```

### Options

```
      --ai-base-url string   provider API base URL for --narrative
  -h, --help                 help for report
      --json string          write the JSON report (with any narrative) to this file (- for stdout)
      --junit string         write JUnit XML to this file
      --md string            write Markdown to this file (- for stdout)
      --model string         model for --narrative (default claude-sonnet-5-5 for anthropic)
      --narrative            add an AI-written summary; every claim cites the report's figures (needs a provider key)
  -o, --out string           write HTML to this file
      --provider string      model provider for --narrative: anthropic, openai, gemini, ollama or openai-compatible (default "anthropic")
```

### SEE ALSO

* [stampede](stampede.md)	 - Self-hosted, distributed load testing

