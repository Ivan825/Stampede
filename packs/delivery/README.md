# Delivery pack

Journeys, stresses and targets for ride hailing and food delivery:
nearby cars, prices and ETAs, matching, live tracking over a WebSocket,
and drivers' apps streaming their locations.

| File | What it tests |
|---|---|
| `journeys/rider-mix.yaml` | Riders across 1,000 accounts: look at nearby cars and the price 40%; request a ride, wait to be matched, follow the driver to the end and rate the trip 35%; order food and follow it to the door 25% |
| `journeys/driver-shift.yaml` | A driver's app keeps a WebSocket open and sends its location every four seconds; every update must be acknowledged within 100ms |
| `stresses/dinner-rush.yaml` | Requests climb from 2/s to 60/s, rides and food orders together, each tracked until the driver is on the way, while others refresh the map; matching under 2s, the map under 200ms |
| `stresses/location-flood.yaml` | Drivers come online until 1,000 apps are streaming locations, while riders check the map and request rides; acknowledgements under 100ms, the map under 200ms, matching under 2s |
| `targets.yaml` | Default targets: under 1% errors, matching under 2s, the map under 200ms, location acknowledgements under 100ms |

**Time to match** is the `matched` step: the tracking socket is opened
as soon as the trip is requested, and the step waits for the "driver
assigned" message, so its time is how long the rider waited for a
driver. Waiting for the trip to finish is a step too, whose time is the
length of the trip; that is why these files set no overall p95.

The journeys follow RideLab's API: `POST /api/login` with a rider or
driver `id` and `password`, `GET /api/drivers/nearby?lat=&lng=`, `GET
/api/eta?from=lat,lng&to=lat,lng` (seconds and fare), `/api/restaurants`
and `/api/restaurants/{id}/menu`, `POST /api/trips` with `type` ride or
food, the tracking socket `/api/trips/{id}/track` (messages `status`
and `driver_location` with `etaSeconds`), `POST /api/trips/{id}/rating`,
and the driver socket `/api/drivers/stream`, which acknowledges each
`{type: location, lat, lng}`. RideLab runs trips thirty times faster
than real time. Every journey and stress is run against
[RideLab](../../examples/packlab/README.md#ridelab) in CI.

For your own service, change the paths and message shapes, put test
riders (with pickup and drop-off points in your city) in
`data/riders.csv` and test drivers in `data/drivers.csv`. Trips on a
real clock take minutes: keep the tracking steps' timeouts longer than
your trips, or stop tracking once the driver is on the way, as the
stresses do. Run against staging: these journeys create trips.

```sh
go run ./examples/packlab -product delivery          # RideLab on :8103
stampede init --target http://localhost:8103         # detects this pack
stampede run stampede/delivery/stresses/dinner-rush.yaml -e TARGET_URL=http://localhost:8103
```
