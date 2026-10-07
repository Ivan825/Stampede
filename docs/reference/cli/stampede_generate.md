## stampede generate

Draft a scenario with an AI model and dry-run every journey (optional, bring your own key)

### Synopsis

Generate a scenario from a description, an OpenAPI spec, a GraphQL schema,
.proto files, a HAR recording and/or an access log, using a language model
you choose. The model only writes the scenario; it is never used while load
runs. With --proto, the services' unary and server streaming methods become
grpc steps, dry-run against --target (plaintext gRPC for http://, TLS for
https://).

Stampede builds a dependency map from the inputs, asks the model for a
scenario that fits the scenario schema, checks it statically (it must
compile, use only known endpoints and never call payment, SMS, email or
CAPTCHA services), then runs every journey once with a single user
against --target. Failures go back to the model with redacted evidence,
up to --max-repairs rounds. Secrets, tokens, cookies, emails, phone and
card numbers in recorded traffic are redacted before anything is sent.

The scenario is written only when every journey passed, or with
--allow-unvalidated, in which case journeys that failed are marked with a
FLAGGED comment. Exit code 4 means the proposal was not written.

Keys come from the environment: ANTHROPIC_API_KEY, OPENAI_API_KEY or
GEMINI_API_KEY. Ollama needs none; openai-compatible servers use
OPENAI_API_KEY when it is set.

```
stampede generate [flags]
```

### Examples

```
  stampede generate --from-openapi examples/shoplab/openapi.yaml \
      --describe "shoppers browse, some log in and buy" \
      --target http://localhost:8090 -o shop.yaml
  stampede generate --from-har session.har --from-log access.log --target http://localhost:8090 \
      --provider ollama --model qwen2.5-coder:14b -o mix.yaml
  stampede generate --from-openapi api.yaml --provider openai --model gpt-5 --no-dry-run -o draft.yaml
  stampede generate --proto protos/orders.proto --describe "clients place and track orders" \
      --target http://localhost:9090 -o orders.yaml
```

### Options

```
      --allow-host strings              extra public hosts the dry run may reach besides the target
      --allow-unvalidated               write the scenario even if some journeys failed, marking them with a comment
      --base-url string                 provider API base URL (required for openai-compatible; e.g. http://localhost:11434 for a remote Ollama)
      --crawl string                    crawl this URL in headless Chrome and use what the pages load (documents and API calls) in place of a HAR file; also the default --target
      --crawl-depth int                 most clicks away from the --crawl URL (default 3)
      --crawl-pages int                 most pages to visit with --crawl (default 30)
      --describe string                 plain-language description of your users and what they do
      --diff-against string             show a diff against this scenario (default: the --out file if it exists)
  -e, --env stringArray                 set ${env.KEY} for the dry run (KEY=VALUE, repeatable)
      --from-graphql string             GraphQL schema: SDL (.graphql) or an introspection result (.json)
      --from-har string                 HAR recording of real use (browser devtools or a proxy)
      --from-log string                 web server access log, used to estimate the journey mix
      --from-openapi string             OpenAPI 3.x spec (YAML or JSON)
      --graphql-path string             path of the GraphQL API on the target (default "/graphql")
  -h, --help                            help for generate
      --introspect                      fetch the GraphQL schema from --target by introspection
      --max-repairs int                 repair rounds before a failing journey is flagged for a human (0 disables repair) (default 3)
      --max-tokens int                  maximum tokens per model reply (default 16000)
      --model string                    model name (default claude-sonnet-5-5 for anthropic; required for the others)
      --no-dry-run                      skip the dry run and only check the scenario statically
  -o, --out string                      write the scenario to this file (required)
      --proto stringArray               .proto file whose services become grpc steps (repeatable); steps name it in proto: so runs load the same descriptors
      --proto-import-path stringArray   directory where imports of the --proto files are found (repeatable); --proto paths are then relative to it
      --provider string                 model provider: anthropic, openai, gemini, ollama or openai-compatible (default "anthropic")
      --target string                   base URL of the system to dry-run against, e.g. http://localhost:8090
      --traces string                   write per-journey dry-run traces as JSON to this file
```

### SEE ALSO

* [stampede](stampede.md)	 - Self-hosted, distributed load testing

