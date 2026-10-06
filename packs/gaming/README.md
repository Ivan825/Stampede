# Gaming backends pack

Journeys, stresses and targets for game backends: matchmaking over
WebSocket, a game server over UDP and a leaderboard over HTTP. The UDP
steps use the [udp plugin](../../plugins/udp/README.md):

```sh
stampede plugin install udp
```

| File | What it tests |
|---|---|
| `journeys/play-session.yaml` | Everyday mix: sign in, queue for a duel, play 20 inputs on the game server, post a score 70%; read the leaderboard and a player's rank 30% |
| `stresses/matchmaking-rush.yaml` | Everyone queues for squads at once (spike from 10 to 300 players a second): time to a match as the queue grows |
| `stresses/full-server.yaml` | Up to 300 players in matches, each sending 10 to 20 inputs a second for 300 inputs |
| `stresses/leaderboard-flood.yaml` | End of a tournament (spike from 20 to 200 a second): post a score, read your rank and the top 100 |
| `targets.yaml` | Default targets: matched p95 under 5s, input p95 under 100ms, submit score p95 under 300ms, under 1% errors |

The journeys follow GameLab's protocol. `POST /api/login {"player": name}`
returns a token. The matchmaking socket is `/api/matchmaking` with
`Authorization: Bearer <token>`: send `{"type": "queue", "mode": "duel"}`
(or `squad`), receive `queued`, then `match` with the match id, the game
server's `host:port` and a ticket. The game server speaks text datagrams:
`join <match> <ticket>` is answered `joined <match> <tick>`, and
`input <match> <seq> ...` is answered `state <match> <seq> <tick>` on the
server's next tick, so the `input` step's latency includes up to one tick.
The UDP steps send to the address matchmaking returned, so the target
policy has to allow that host. Every journey and stress is run against
[GameLab](../../examples/packlab/README.md#gamelab) in CI.

For your own game, change the socket path and messages, and the
datagrams: if your server speaks a binary protocol, send hex or base64
payloads (`encoding: hex`) and `match` on what identifies a reply to this
player. UDP does not pair replies with requests; keep something unique to
the match or the request in `match` so a late reply is not taken for a
new one.

```sh
stampede plugin install udp
go run ./examples/packlab -product gaming            # GameLab: HTTP on :8116, UDP on :8117
stampede init --target http://localhost:8116         # detects this pack
stampede run stampede/gaming/journeys/play-session.yaml -e TARGET_URL=http://localhost:8116
```
