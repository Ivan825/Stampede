# Your first real target

1. **Point at a test environment.** Staging, not production, until you trust
   your scenarios. Third-party services (payments, SMS, email, CAPTCHA) must
   be in test mode or mocked.
2. **Private targets just work.** Loopback and private network addresses
   (10/8, 172.16/12, 192.168/16, fc00::/7) need no setup.
3. **Public targets need ownership.** Until verified, a public host runs
   under low caps (50/s, 50 users, 10 minutes). Verify with
   `stampede target verify https://staging.example.com`, or, for a target
   saved on a server, `stampede targets verify`: publish the token as a DNS
   TXT record `stampede-verify=<token>` or at
   `/.well-known/stampede-verify.txt`. The web UI's **Targets** page shows
   each target's verification status.
4. **Start from something.** `stampede init --target <url>` if your product
   matches a [pack](packs.md); `stampede generate` with an OpenAPI spec
   ([AI generation](../ai.md)); or write a scenario by hand
   ([scenarios](../concepts/scenarios.md)).
5. **Smoke first.** `stampede run my.yaml --shape smoke` proves every
   journey works before you add load.
6. **Then baseline, then breakpoint.** Know normal behaviour before looking
   for the limit.

Requests can only reach the target host and private hosts. If a journey
legitimately calls another public host you own (a CDN, an auth domain),
allow it with `--allow-host` or the target's allowed hosts. See
[safety](../safety.md).
