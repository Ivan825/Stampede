# Troubleshooting

**`invalid scenario: ... is "token" extracted in an earlier step?`**
A template uses a variable before any step extracts it, or the name is
misspelled. Variables only flow forward within an iteration.

**`... is a public host whose ownership is not verified, so load is capped`**
Verify the target (see [safety](safety.md)) or test a private address.

**Every request fails with `blocked by safety`**
The request goes to a public host other than the target. Allow it with
`--allow-host` (CLI) or the target's allowed hosts (server).

**`dropped` iterations in an open-model run**
No virtual user was free when an iteration was due. Raise `maxVUs`, or the
target is so slow that the planned rate cannot be served. Dropped iterations
show the generator could not keep the schedule; they are not target errors.

**The verdict is `generator-limited`**
A worker was saturated (CPU, scheduling lag, GC, file descriptors). Add
workers, give them more CPU, or lower the load per worker.

**`data.users reads a file, which needs the server to be started with --data-dir`**
On a server, CSV and JSON feeders must live in its data directory. In the
Compose stack ShopLab's `data/` folder is mounted there.

**`too many open files` / `local resource limit` errors at high concurrency**
Raise the file-descriptor limit (`ulimit -n`) on the generating machine.
Each open connection uses one.

**Workers do not appear in `stampede workers`**
Check that the worker's `--token` matches the server's `--join-token`, that
it can reach the server's worker port (8081), and `--insecure` versus TLS
matches the server.

**`stampede compare` says not comparable**
The runs differ in scenario, load plan, duration or worker count, or one was
generator-limited. Compare like with like.
