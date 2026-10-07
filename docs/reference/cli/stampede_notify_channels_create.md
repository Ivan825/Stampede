## stampede notify channels create

Add a webhook, Slack or Discord channel

### Synopsis

Add a channel. The destination URL is stored encrypted and never shown
again; give it with --url, or with --url-env to keep a Slack or Discord
webhook (which works as a password) out of shell history. A generic
webhook signs each body with HMAC-SHA256 in X-Stampede-Signature: its
secret is read from --secret-env, or generated and printed once.
Destinations on private, loopback or link-local addresses need
--allow-private. Events: run.finished, run.target_failed, run.killed,
drift.detected (default all).

```
stampede notify channels create <name> [flags]
```

### Examples

```
  stampede notify channels create team --kind slack --url-env SLACK_WEBHOOK --event run.target_failed --event run.killed
  stampede notify channels create ci --kind webhook --url https://ci.example.com/hooks/stampede
```

### Options

```
      --allow-private       allow a destination on a private, loopback or link-local address
      --event stringArray   event to send (repeatable; default all)
  -h, --help                help for create
      --json                print JSON for scripting
      --kind string         webhook, slack or discord
      --secret-env string   webhooks: read the signing secret (16+ characters) from this environment variable
      --url string          destination URL
      --url-env string      read the destination URL from this environment variable
```

### SEE ALSO

* [stampede notify channels](stampede_notify_channels.md)	 - List, add, delete and test notification channels

