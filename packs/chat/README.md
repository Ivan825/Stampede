# Chat and collaboration pack

Journeys, stresses and targets for chat services that speak WebSocket.

| File | What it tests |
|---|---|
| `journeys/chat-mix.yaml` | Everyday mix: join a room and send five messages 70%, read rooms and history over HTTP 20%, leave an idle tab open for half a minute 10% |
| `stresses/fan-out.yaml` | Up to 300 users in one room, each sending 20 messages; the ack time is the time to reach every member |
| `stresses/reconnect-storm.yaml` | Every client reconnects at once (spike from 10 to 300 connections a second): sign in, connect, fetch missed history, send |
| `stresses/open-connections.yaml` | 2,000 mostly idle connections held open with a heartbeat every 10 to 20 seconds |
| `targets.yaml` | Default targets: ack p95 under 250ms, connect p99 under 1s, under 1% errors |

The journeys follow ChatLab's protocol: `POST /api/login` returns a token,
the socket is `/ws?room=<name>` with `Authorization: Bearer <token>`, and
messages are JSON with a `type` (`welcome`, `say`, `ack`, `message`,
`typing`, `history`, `presence`, `ping`, `pong`). The server acks a `say`
once the message has gone to every member, so `ack` latency is fan-out
time. Every journey and stress is run against
[ChatLab](../../examples/packlab/README.md#chatlab) in CI.

For your own service, change the socket path and auth, and the `send` and
`expect` shapes. If your server does not acknowledge messages, have a
second user in the room `expect` the message instead. A connection keeps
the last 32 unread messages; in very busy rooms an `expect` should match
something addressed to this user (an ack, a reply) rather than any room
message.

```sh
go run ./examples/packlab -product chat              # ChatLab on :8093
stampede init --target http://localhost:8093         # detects this pack
stampede run stampede/chat/journeys/chat-mix.yaml -e TARGET_URL=http://localhost:8093
```
