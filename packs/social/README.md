# Social pack

Journeys, stresses and targets for social networks and forums: home
feeds, posts, likes, comments, follows and live notifications.

| File | What it tests |
|---|---|
| `journeys/feed-mix.yaml` | Everyday mix across 2,000 users: scroll two pages of the feed and open a post 45%, like 20%, publish a post (it must top the author's own list at once) 10%, comment 10%, explore trending posts and follow someone 10%, read notifications 5% |
| `journeys/live-notifications.yaml` | One user keeps a notification WebSocket open; another comments on their latest post; the notification must reach the socket (target: p95 under 1s from the comment request) |
| `stresses/viral-spike.yaml` | Arrivals spike from 10/s to 300/s: most open a celebrity's latest post and like it, some comment, the celebrity has notifications open, and other users keep scrolling; likes under 300ms, feeds under 500ms |
| `stresses/feed-rush.yaml` | The morning scroll: arrivals climb from 10/s to 200/s, each reading three feed pages; the first page under 300ms |
| `targets.yaml` | Default targets: p95 under 500ms, under 1% errors, the feed under 300ms, likes under 200ms |

The journeys follow SocialLab's API: `POST /api/login` with `username`
and `password` for a bearer token, `GET /api/feed` (newest first, `before`
cursor from `next_cursor`), `POST /api/posts`, `/api/posts/{id}` with
`/like` (POST and DELETE; liking twice changes nothing) and `/comments`,
`/api/users/{username}` with `/posts` and `/follow`, `GET /api/trending`,
`GET /api/notifications` and the socket `/api/notifications/ws`, which
sends `welcome`, then `notification` messages with a `kind` (like,
comment, follow), and answers `ping` with `pong`. Every journey and stress
is run against [SocialLab](../../examples/packlab/README.md#sociallab) in
CI.

For your own API, change the paths and JSONPaths, put test accounts in
`data/users.csv`, and set `vars.celebrity` in the viral spike to an
account with many followers. The two journeys that need a second account
(a fan) take it from `user3000` to `user4999`; change that range to
accounts you have. If your notifications arrive over server-sent events
or push rather than a socket, replace the `ws` block with an `sse` step
or a poll of the notifications list.

```sh
go run ./examples/packlab -product social            # SocialLab on :8098
stampede init --target http://localhost:8098         # detects this pack
stampede run stampede/social/stresses/viral-spike.yaml -e TARGET_URL=http://localhost:8098
```
