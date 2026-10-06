# Ticketing pack

Journeys, stresses and targets for ticketing and booking APIs: events,
seat maps, seat holds, orders and a waiting room.

| File | What it tests |
|---|---|
| `journeys/browse-and-book.yaml` | Everyday mix: browse events and seat maps 60%, hold 1 to 4 seats 40% (80% of holders buy, 20% let the hold expire); 409 sold out is a correct answer |
| `journeys/waiting-room.yaml` | Headline on-sale: join the waiting room, wait on a WebSocket until admitted, hold two seats, buy |
| `stresses/on-sale-rush.yaml` | Arrivals spike from 10/s to 300/s through the waiting room until the event sells out, while other events are still being booked |
| `stresses/seat-lock-contention.yaml` | 60 buyers race for ten front-row seats; losers get 409, and every buyer then checks that no seat was sold twice |
| `targets.yaml` | Default targets: p95 under 500ms, under 1% errors, holds under 300ms |

The journeys follow TicketLab's API: `GET /api/events`, `GET
/api/events/{id}/seats`, `POST /api/holds` with `quantity` (best
available) or named `seats`, `POST /api/orders` to buy what the session
holds, `POST /api/queue/{id}/join` and the waiting-room socket
`/ws/queue?event={id}`, which sends `position` messages and then
`admitted`. The session is a cookie, so the admission carries from the
socket to the hold. Every journey and stress is run against
[TicketLab](../../examples/packlab/README.md#ticketlab) in CI, and the test
checks that the contention stress really produces 409s.

For your own API, change the paths and fields, the event ids under `vars`
and the seat names in the contention stress. If your waiting room is a
polling endpoint rather than a socket, replace the `ws` step with a
`while` loop that polls until admitted.

```sh
go run ./examples/packlab -product ticketing         # TicketLab on :8094
stampede init --target http://localhost:8094         # detects this pack
stampede run stampede/ticketing/journeys/browse-and-book.yaml -e TARGET_URL=http://localhost:8094
```
