## stampede ai providers set

Create or replace an AI provider

### Synopsis

Create a provider, or change one by name; settings not given keep their
current values. The API key is encrypted with the server's master key and
never shown again. It is read from the environment variable named by
--api-key-env, or from stdin with --api-key-stdin, or asked without echo
in a terminal (leave it empty to keep the stored key). Jobs use the
provider named "default", or the only one.

```
stampede ai providers set <name> [flags]
```

### Examples

```
  stampede ai providers set default --kind anthropic --api-key-env ANTHROPIC_API_KEY
  stampede ai providers set local --kind openai-compatible --base-url http://llm.internal:8000/v1 --model qwen3
  stampede ai providers set default --monthly-token-cap 5000000
```

### Options

```
      --api-key-env string      read the API key from this environment variable
      --api-key-stdin           read the API key from stdin
      --base-url string         API base URL (required for openai-compatible)
  -h, --help                    help for set
      --json                    print JSON for scripting
      --kind string             anthropic, openai, gemini, ollama or openai-compatible (required for a new provider)
      --model string            model name (default claude-sonnet-5-5 for anthropic; required for the others)
      --monthly-token-cap int   refuse jobs once the organisation used this many tokens in a month (default 2000000)
```

### SEE ALSO

* [stampede ai providers](stampede_ai_providers.md)	 - List, set and delete the organisation's AI providers (admin)

