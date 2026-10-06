# Content pack

Journeys, stresses and targets for news sites, blogs and other content
platforms served through a cache or CDN: home, section and article
pages, RSS, a JSON API and the newsroom's publish call.

| File | What it tests |
|---|---|
| `journeys/reader-mix.yaml` | Everyday mix: home page and top story 35%, a section and an article 20%, an article from a social link with tracking parameters 15%, a returning reader revalidating with `If-None-Match` 10%, the JSON API 12%, RSS 5%, search 3%; pages must carry `Cache-Control` with a max-age and an `ETag` |
| `stresses/breaking-news.yaml` | Arrivals spike from 10/s to 200/s: most readers follow social links to the newest story, others reload the home page, and the newsroom publishes an update every few seconds; home and story pages under 100ms |
| `stresses/cold-cache.yaml` | Readers ramp from 10/s to 100/s across 5,000 articles plus home and section pages, starting from an empty cache; articles under 500ms while the cache fills, the home page under 100ms |
| `targets.yaml` | Default targets: p95 under 200ms, under 1% errors, the home page and its top story under 50ms |

**Latency targets are hit-rate targets here.** A cached page takes a few
milliseconds and a rendered one tens or hundreds, so a p95 well under the
render time holds only while at least 95% of requests are cache hits. To
see the hit rate itself, look at your CDN's analytics or the cache's own
header (`X-Cache`, `CF-Cache-Status`, `Age`); NewsLab also reports it at
`GET /api/cache/stats`.

The journeys follow NewsLab: HTML pages at `/`, `/section/{name}` and
`/articles/{slug}` (the home page's lead link has class `top-story`,
teasers have class `headline`), `/feed.xml`, `/api/articles` (by section)
and `/api/articles/{slug}`, `/api/search?q=`, and `POST /api/articles`
with the newsroom's bearer token. Every journey and stress is run against
[NewsLab](../../examples/packlab/README.md#newslab) in CI.

For your own site, change the CSS selectors in the extractors, the
section names and the article URL pattern, and point `TARGET_URL` at the
address readers use, so requests go through your CDN. Run
`breaking-news.yaml` against staging: it publishes articles. To make
`cold-cache.yaml` start cold, run it straight after a deploy or a purge.

```sh
go run ./examples/packlab -product content           # NewsLab on :8099
stampede init --target http://localhost:8099         # detects this pack
stampede run stampede/content/stresses/breaking-news.yaml -e TARGET_URL=http://localhost:8099
```
