## stampede audit

Read the organisation's audit log, newest first (admin)

### Synopsis

Print who did what and when: sign-ins, runs started and killed, changes to
users, tokens, projects, targets, secrets, schedules, caps, AI providers,
integrations and channels. --since and --before take a duration ago (24h,
7d), a date or an RFC 3339 time; --action keeps entries whose action
starts with the given text, such as run. or token.

```
stampede audit [flags]
```

### Examples

```
  stampede audit --since 24h
  stampede audit --action run.kill --limit 20 --json
```

### Options

```
      --action string   only actions starting with this, such as run. or token.create
      --before string   only entries before this time
  -h, --help            help for audit
      --json            print JSON for scripting
      --limit int       most entries to print (default 100)
      --since string    only entries after this time (24h, 7d, 2026-10-01 or RFC 3339)
```

### SEE ALSO

* [stampede](stampede.md)	 - Self-hosted, distributed load testing

