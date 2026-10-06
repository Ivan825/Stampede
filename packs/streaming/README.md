# Streaming pack

Journeys, stresses and targets for video and audio streaming over HLS:
playback sessions, multivariant and media playlists, segments, rendition
switches, live playlists that slide forward, and player heartbeats.

| File | What it tests |
|---|---|
| `journeys/vod-mix.yaml` | On demand: browse the catalogue 20%; start-up only (press play, playlists, first segment) 30%; watch 50%: two segments at 240p, switch to 480p, then one segment every two seconds with heartbeats. Popular titles get most plays |
| `journeys/live-viewer.yaml` | A live viewer for about 30 seconds: reload the sliding playlist and fetch the newest segment every two seconds |
| `stresses/premiere-spike.yaml` | Arrivals spike from 5/s to 150/s, nearly all starting the same new title; playback start and first segment under 500ms |
| `stresses/live-event.yaml` | Viewers ramp to 1,000 over two minutes and stay; live playlists under 200ms, newest segments under 1s |
| `targets.yaml` | Default targets: under 1% errors, playback start and first segment under 500ms, segments under 1s |

**Start-up time** is what a viewer waits between pressing play and the
picture: the playback call, the multivariant playlist, the media
playlist and the first segment. Stampede reports each of these steps;
add up their p95s for a conservative start-up figure, and hold each with
a target. After start-up, a segment that takes longer than its own
duration (two seconds here) to arrive means the player's buffer drains.

The journeys follow StreamLab: `POST /api/playback` with `videoId` or
`channel` returns a `manifest` URL and a signed `token`; playlists and
segments live at `/vod/{video}/{rendition}/...` and
`/live/{channel}/{rendition}/...` (renditions 240p, 480p, 720p, 1080p,
segments `seg00000.ts` upward, two seconds each), with the token as a
query parameter; `POST /api/playback/{session}/heartbeat` takes the
player's position, rendition, buffer and stalls. Every journey and stress
is run against [StreamLab](../../examples/packlab/README.md#streamlab) in
CI.

For your own service, change the paths, rendition names and segment
naming. The on-demand journeys name segments explicitly, so a player's
order is kept; the live ones read the newest segment from the playlist
with a regular expression. If your playlists use relative URIs, build
the segment paths from the playlist's own path as `vod-mix.yaml` does.
Point `TARGET_URL` at your CDN to test what viewers get, or at the origin
to test what the CDN's misses cost. DASH works the same way with the
manifest and segment paths changed.

```sh
go run ./examples/packlab -product streaming         # StreamLab on :8100
stampede init --target http://localhost:8100         # detects this pack
stampede run stampede/streaming/stresses/live-event.yaml -e TARGET_URL=http://localhost:8100
```
