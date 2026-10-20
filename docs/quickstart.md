# Quick start

There are two routes. **Try it** needs only the `stampede` CLI and a demo
app: no server, no account. **Add the server and workers** puts the web UI
and distributed runs on top, when you want them.

## Try it

You need Docker (for the demo app) and Git.

### 1. Start ShopLab and install the CLI

**ShopLab** is a small shop API with six performance problems planted on
purpose (see [examples/shoplab](../examples/shoplab/README.md)). This
starts it on <http://localhost:8090> with its database and cache, and
nothing else:

```sh
git clone https://github.com/Ivan825/Stampede
cd Stampede
docker compose up -d --build shoplab
```

Install the CLI:

```sh
curl -fsSL https://raw.githubusercontent.com/Ivan825/Stampede/main/install.sh | sh
# or: brew install ivan825/tap/stampede  (Windows: Scoop, see installation)
```

### 2. Let Stampede recognise it

```sh
stampede init --target http://localhost:8090
```

It finds ShopLab's OpenAPI document, picks the e-commerce pack, copies it
into `./stampede/ecommerce`, runs every journey once to prove it works, and
prints the command to run next. Run the everyday shopping mix for a
minute:

```sh
stampede run stampede/ecommerce/journeys/shop-mix.yaml \
  -e TARGET_URL=http://localhost:8090 --duration 1m -o report.html
```

You get a live line per second, a summary with targets marked pass or fail,
and a self-contained HTML report in `report.html`.

### 3. Find the breaking point

```sh
stampede run examples/shoplab/scenarios/breakpoint.yaml -e TARGET_URL=http://localhost:8090
```

The breakpoint shape raises the arrival rate in steps and stops at the first
level where `p95 < 250ms` no longer holds. The summary names the last level
that held and where throughput stopped keeping up with load (the knee).

### 4. Fix it and run it again

ShopLab's problems each have a fix behind a flag. Turn them all on and rerun
the same scenario:

```sh
SHOPLAB_FIX_ALL=1 docker compose up -d shoplab
stampede run examples/shoplab/scenarios/breakpoint.yaml -e TARGET_URL=http://localhost:8090
```

On a laptop, with the steps shortened to 12 seconds, we measured the
breakpoint moving from about 480/s to about 1,570/s. Your numbers will
differ with your hardware.

### 5. Compare two versions properly

One run each is a hint, not proof. To judge a change, repeat each side
three times and let Stampede decide whether the difference is bigger than
the noise. With the same shopping mix as step 2, first with the bottlenecks
back on:

```sh
SHOPLAB_FIX_ALL=0 docker compose up -d shoplab
stampede run stampede/ecommerce/journeys/shop-mix.yaml -e TARGET_URL=http://localhost:8090 \
  --duration 30s --repeat 3 --json before.json        # writes before-1.json .. before-3.json

SHOPLAB_FIX_ALL=1 docker compose up -d shoplab
stampede run stampede/ecommerce/journeys/shop-mix.yaml -e TARGET_URL=http://localhost:8090 \
  --duration 30s --repeat 3 --json after.json

stampede compare --a before-1.json,before-2.json,before-3.json \
                 --b after-1.json,after-2.json,after-3.json
```

`compare` reports each metric's change with a confidence interval and says
whether it is an improvement, a regression or within the noise
([comparing](guides/comparing.md)).

Next: [your first real target](guides/first-target.md).

## Add the server and workers

The full stack adds TimescaleDB, the Stampede server with its web UI on
<http://localhost:8080> and two load-generating workers. It needs about
4 GB of memory for Docker. From the clone:

```sh
docker compose up -d --build
```

(Without a clone, `stampede up` starts the same stack from the released
images, without ShopLab.)

Then create the organisation and its owner account, which also signs the
CLI in:

```sh
stampede setup
```

Until this has run, <http://localhost:8080> shows a page saying to run
`stampede setup`. Afterwards it shows the sign-in page. Everything is done
with the `stampede` CLI; the web UI is for watching runs and reading
reports ([using Stampede from the CLI](guides/cli.md)).

Create a project, and a target for `http://shoplab:8090` (the address the
workers see inside Compose), then start a run on the workers:

```sh
stampede projects create shop --use
stampede targets create shoplab --base-url http://shoplab:8090
stampede start --file stampede/ecommerce/journeys/shop-mix.yaml --target http://shoplab:8090
```

`start` saves the file as a new scenario version, splits the load across
the two workers, and streams the run until it finishes. The web UI shows
the same run live, with worker health and events, and its report when it
ends.

`stampede run --cluster` does the same with `run`'s flags and outputs:

```sh
stampede run stampede/ecommerce/journeys/shop-mix.yaml --cluster --target http://shoplab:8090 -o report.html
```
