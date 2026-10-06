# Plugins

Plugins add step types to Stampede for protocols it does not speak itself,
such as `mqtt.publish` or `kafka.produce`. A plugin is a separate
executable that Stampede starts next to the engine and talks to over gRPC,
so a plugin that crashes or panics fails its own steps and nothing else.

Five first-party plugins live in this repository, each a Go module of its
own under [plugins/](../plugins) so the main binary stays lean:

| Plugin | Steps | Tested against |
|---|---|---|
| [mqtt](../plugins/mqtt/README.md) | `connect`, `publish`, `subscribe`, `expect`, `disconnect` | in-process Mochi MQTT broker |
| [kafka](../plugins/kafka/README.md) | `produce`, `consume` | franz-go's in-process kfake cluster |
| [redis](../plugins/redis/README.md) | `command` (single or pipeline) | miniredis |
| [sql](../plugins/sql/README.md) | `query`, `exec` (PostgreSQL, MySQL, SQLite) | SQLite in-process; PostgreSQL and MySQL service containers in CI |
| [udp](../plugins/udp/README.md) | `send` (optionally awaiting a reply) | a local UDP echo listener |

None of them has been run against a production broker, cluster or database
in this repository's CI; the fakes above are what they are tested against.

## Using plugins

```sh
stampede plugin install mqtt          # build and install a first-party plugin
stampede plugin list                  # what is installed, with versions and steps
stampede plugin show mqtt             # each step's settings (its JSON Schema)
stampede plugin remove mqtt
```

A plugin is an executable named `stampede-plugin-<name>`. Stampede looks
for it in the plugin directory (`$STAMPEDE_PLUGIN_DIR`, or `plugins/` under
the user config directory: `~/.config/stampede/plugins` on Linux,
`~/Library/Application Support/stampede/plugins` on macOS), then on `PATH`.

`stampede plugin install` accepts:

| Argument | What happens |
|---|---|
| `mqtt`, `kafka`, `redis`, `sql`, `udp` | `go build` in `plugins/<name>` of a Stampede checkout: `--source`, `$STAMPEDE_SOURCE`, or the checkout containing the current directory; with none, the repository is cloned (`--ref` picks a branch or tag) |
| `./path/to/dir` | `go build` of the main package in that directory |
| `./stampede-plugin-x` | the executable is copied |
| `example.com/x/cmd/plugin@v1.2.0` | `go install` (`@latest` when no version is given) |

Building needs a Go toolchain (and git for a clone). Whatever is built is
started and must describe itself correctly before it is installed, under
the name it describes itself with.

**Distributed runs.** Workers find plugins the same way, so install a
plugin on every worker that will run scenarios using it (or set
`STAMPEDE_PLUGIN_DIR` for the worker process). Workers advertise their
plugins as `plugin:<name>` in the capacity they report to the server
when they connect (not yet shown by `stampede workers`). A worker that
lacks a plugin a run needs refuses the run before any load starts, and the
run fails with the worker's name and the missing plugin. The Docker image
does not include plugins.

## In a scenario

```yaml
- name: publish reading
  plugin: mqtt.publish              # <plugin>.<step>
  with:                             # the step's settings
    topic: devices/${vu}/telemetry
    payload: '{"seq": ${iter}}'
    qos: 1
  check: { maxLatency: 50ms, json: { "$.messageId": exists } }
  extract: { msgId: "$.messageId" }
  timeout: 5s
```

- `with` holds the step's settings. Strings may contain `${}` expressions,
  rendered for every call. A string that is a single expression keeps the
  expression's type, so `qos: "${level}"` sends a number when `level` is a
  number.
- Settings are checked against the JSON Schema the plugin describes for the
  step: by `stampede validate` when the plugin is installed on that machine
  (it says so when it is not), and before every run starts (on every
  worker). Values that contain `${}` are only known at run time, so their
  type and format are checked by the plugin on each call
  (`invalid config` when they do not fit); everything else (missing,
  misspelt or extra settings, literal values) fails before the run.
- A step returns a JSON object of values. `check` (`json`, `expr`,
  `bodyContains`, `maxLatency`) and `extract` (`$.path`, `regex:`, `body`)
  work on it exactly as on an HTTP response's JSON body; `check.status`
  does not apply.
- `timeout` bounds the step (default `target.timeout`); the plugin gives up
  at the deadline and reports its own timeout class.

Plugin steps are recorded like every other step: their own latency
histogram, error rate and error labels, protocol counts (the plugin's name)
and targets such as `device/publish reading.p95 < 50ms`. A failed step ends
the iteration.

**Latency** is measured by the plugin, around the operation itself (a
broker acknowledgement, a query, a round trip), and can be broken into the
usual phases (`dns`, `connect`, `tls`, `wait`, `download`, `firstEvent`).
The sample ends when the plugin's answer reaches the engine, so in
open-model runs the hop to the plugin process (tens of microseconds) counts
as queueing, never as service time. A step that receives several messages
can report them as events, shown like a stream: time to first message and
messages per second.

**Sessions.** Each virtual user opens its own session in every plugin it
uses: its own MQTT client, Kafka consumers, database connection or UDP
socket, kept across iterations and closed when the user retires. A step
may report that it had nothing to do (connecting a client that is already
connected): nothing is recorded for it, and its extractors still run.

**Errors** are labelled by the plugin with short classes such as
`mqtt not authorized` or `sql 23505`. The engine adds `plugin crashed` (the
process died: it is restarted on the next call, at most once a second, and
users open new sessions), `plugin unavailable`, `timeout`,
`template error` and `blocked by safety`. Classes longer than 64
characters are cut.

**Safety.** A plugin marks the setting that holds the address it connects
to with `"x-stampede-target": true` in its schema (`broker`, `brokers`,
`addr`, `dsn`). Before every call the engine reads the hosts in that value
(URLs, `host:port` lists, PostgreSQL and MySQL DSNs; SQLite `file:` URIs
and unix sockets count as local) and applies the run's target policy: a
host outside it fails the step as `blocked by safety`, and an address the
engine cannot read is refused rather than allowed. The policy only covers
marked settings: a plugin is code you install and run, and Stampede cannot
see what else it connects to (a Kafka client talks to every broker the
cluster advertises, for example). A scenario made only of plugin steps
needs no `target.baseURL`; `stampede run` then reaches only private hosts
and hosts given with `--allow-host`.

## Writing a plugin

A plugin is a Go program built on [`pkg/pluginsdk`](../pkg/pluginsdk). It
describes its steps, each with a JSON Schema for its settings, and calls
`Serve`:

```go
package main

import (
	"context"
	"time"

	"github.com/Ivan825/Stampede/pkg/pluginsdk"
)

type session struct{ greeted int }

func main() {
	pluginsdk.Serve(&pluginsdk.Plugin{
		Name:    "greeter", // the executable is stampede-plugin-greeter
		Version: "0.1.0",
		// One session per virtual user; closed (if it is an io.Closer)
		// when the user retires.
		NewSession: func(context.Context, pluginsdk.SessionInfo) (any, error) {
			return &session{}, nil
		},
		Steps: []pluginsdk.Step{{
			Name:        "greet",
			Description: "Greets someone.",
			Schema: `{
			  "type": "object",
			  "additionalProperties": false,
			  "required": ["name", "server"],
			  "properties": {
			    "name": {"type": "string"},
			    "server": {"type": "string", "x-stampede-target": true}
			  }
			}`,
			Run: func(ctx context.Context, c *pluginsdk.Call) (*pluginsdk.Result, error) {
				var cfg struct {
					Name   string `json:"name"`
					Server string `json:"server"`
				}
				if err := c.Decode(&cfg); err != nil { // rejects unknown fields
					return nil, err
				}
				s := c.Session.(*session)
				start := time.Now()
				// ... talk to cfg.Server, honouring ctx's deadline ...
				if cfg.Name == "" {
					return nil, pluginsdk.Fail("greeter refused", nil) // a bounded error class
				}
				s.greeted++
				return &pluginsdk.Result{
					Latency: time.Since(start), // zero: the SDK times Run
					Phases:  pluginsdk.Phases{Wait: time.Since(start)},
					Values:  map[string]any{"greeting": "hello " + cfg.Name, "count": s.greeted},
				}, nil
			},
		}},
	})
}
```

The SDK:

- validates the description when the plugin starts (name: lowercase
  letters, digits and hyphens; a version; unique step names; schemas that
  are JSON Schema 2020-12 objects and compile; `x-stampede-target` only on
  top-level string or string-array properties);
- validates every rendered config against its schema before `Run` sees it,
  and `Call.Decode` rejects fields the target struct does not have;
- runs `Run` concurrently for different users, never concurrently for the
  same session;
- gives `Run` a context with the step's deadline, and the step's trace
  context (`Call.Traceparent`, `Call.Baggage`) for protocols that can carry
  it;
- turns errors into failed steps: `pluginsdk.Fail(class, err)` sets the
  class; a deadline becomes `timeout`; anything else `<plugin> error`;
- recovers panics in `Run`, `NewSession` and `Close` (the step fails with
  `plugin panic` and the stack is logged through the host);
- limits returned values to 1 MiB of JSON, which must be an object.

Keep error classes bounded: they group failures in reports, so put ids,
addresses and messages in the error, not the class.

### Conformance

[`pkg/pluginsdk/conformance`](../pkg/pluginsdk/conformance) checks a built
plugin against the contract. Run it from a test with a few step calls that
work against a fake or test server you start:

```go
func TestConformance(t *testing.T) {
	addr := startTestServer(t)
	conformance.Run(t, conformance.Build(t, ".", "greeter"), conformance.Options{
		Cases: []conformance.Case{
			{Step: "greet", Config: map[string]any{"name": "Ada", "server": addr}},
			{Step: "greet", Config: map[string]any{"name": "", "server": addr}, WantClass: "greeter refused"},
		},
	})
}
```

It starts the executable the way Stampede does and checks that it
describes itself validly (and that the executable's name matches); that
the cases fit their schemas; the session lifecycle (open, execute, close,
close again, a closed or unknown session fails cleanly); that unknown
steps and bad configs (not an object, unknown settings, not JSON) fail as
steps with a short one-line class instead of an RPC error; that a 1 ms
timeout comes back promptly; that 50 users (`VUs`) running the cases at
once all succeed; and that the process is still running at the end.
`SkipBadInput` exempts a step from the bad-config calls. Every first-party
plugin runs it in its tests.

### Packaging

- Build a static executable named `stampede-plugin-<name>` for each OS and
  architecture your workers run; users install it with
  `stampede plugin install ./stampede-plugin-<name>` or put it on `PATH`.
- Or publish the module: `stampede plugin install example.com/you/plugin@v1.0.0`
  runs `go install` and installs the result under the name it describes.
- The plugin protocol is versioned by go-plugin's handshake
  (`pluginsdk.Handshake`, protocol version 1, the contract in
  [proto/stampede/plugin/v1](../proto/stampede/plugin/v1/plugin.proto)).
  A plugin built for another protocol version is refused at start.
- The SDK lives in the main Stampede module, so a plugin depends on
  `github.com/Ivan825/Stampede`; only the packages it imports are built
  into it. First-party plugins use a `replace` directive to build against
  the SDK in the same checkout.

### The protocol

Plugins written in other languages can implement
[`stampede.plugin.v1.PluginService`](../proto/stampede/plugin/v1/plugin.proto)
over gRPC behind hashicorp/go-plugin's handshake and health service, as
described in go-plugin's guide to plugins in other languages, with the
cookie in `pluginsdk.Handshake`. Only Go plugins built with the SDK have
been tried:

| RPC | |
|---|---|
| `Describe` | name, version, description, and each step's name, description and config schema |
| `Open` | a session for a virtual user (`vu`, `run_id`), returning its id |
| `Execute` | one step: session, step name, the rendered config as JSON, timeout, trace context, iteration; returns `ok`, `error_class`, `error`, `latency_ns`, `phases_ns`, `bytes_in`, `bytes_out`, `values` (a JSON object), `events`, `skipped` |
| `Close` | ends a session; closing an unknown session is not an error |

A failed step is a successful `Execute` with `ok = false`; an RPC error
means the plugin could not run the step at all.
