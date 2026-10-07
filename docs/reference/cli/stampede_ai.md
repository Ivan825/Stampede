## stampede ai

Manage the server's AI providers and journey generation jobs

### Synopsis

AI journey generation on the server is optional and bring-your-own-key:
an admin adds a provider (Anthropic, OpenAI, Gemini, Ollama or any
OpenAI-compatible server), then editors start jobs that draft a scenario
from a description, an OpenAPI document, a HAR file, an access log or
.proto files, dry-run every journey against a target and repair it. A job
is a proposal; nothing is saved until someone approves it. stampede
generate does the same on this machine without a server.

### Options

```
  -h, --help   help for ai
```

### SEE ALSO

* [stampede](stampede.md)	 - Self-hosted, distributed load testing
* [stampede ai jobs](stampede_ai_jobs.md)	 - Start, follow and approve AI journey generation jobs
* [stampede ai providers](stampede_ai_providers.md)	 - List, set and delete the organisation's AI providers (admin)

