# Quick start

You need Docker (with about 4 GB of memory for it) and Git.

## 1. Start the stack

```sh
git clone https://github.com/Ivan825/Stampede
cd Stampede
docker compose up -d --build
```

This starts TimescaleDB, the Stampede server with its web UI on
<http://localhost:8080>, two load-generating workers, and **ShopLab** on
<http://localhost:8090>: a small shop API with six performance problems
planted on purpose (see [examples/shoplab](../examples/shoplab/README.md)).

## 2. Create your account

Open <http://localhost:8080>. The first visit asks for an organisation name
and an owner account.

## 3. Run something from the command line

The quickest path needs no server at all. Build or install the binary:

```sh
go install github.com/Ivan825/Stampede/cmd/stampede@latest
```

Let Stampede recognise ShopLab and set up the matching pack:

```sh
stampede init --target http://localhost:8090
```

It finds ShopLab's OpenAPI document, picks the e-commerce pack, copies it
into `./stampede/ecommerce` and runs every journey once to prove it works.
Now run the everyday shopping mix for a minute:

```sh
stampede run stampede/ecommerce/journeys/shop-mix.yaml \
  -e TARGET_URL=http://localhost:8090 --duration 1m -o report.html
```

You get a live line per second, a summary with targets marked pass or fail,
and a self-contained HTML report.

## 4. Find the breaking point

```sh
stampede run examples/shoplab/scenarios/breakpoint.yaml -e TARGET_URL=http://localhost:8090
```

The breakpoint shape raises the arrival rate in steps and stops at the first
level where `p95 < 250ms` no longer holds. The summary names the last level
that held and where throughput stopped keeping up with load (the knee).

## 5. Fix it and compare

ShopLab's problems each have a fix behind a flag. Turn them all on and rerun
the same scenario:

```sh
SHOPLAB_FIX_ALL=1 docker compose up -d shoplab
stampede run examples/shoplab/scenarios/breakpoint.yaml -e TARGET_URL=http://localhost:8090
```

On a laptop, with the steps shortened to 12 seconds, we measured the breakpoint moving from about 480/s to about
1,570/s. Your numbers will differ with your hardware.

To compare versions properly, repeat each run three times and let Stampede
judge whether the difference is bigger than the noise:

```sh
stampede run checkout.yaml --repeat 3 --json before.json   # writes before-1..3.json
# change the system
stampede run checkout.yaml --repeat 3 --json after.json
stampede compare --a before-1.json,before-2.json,before-3.json \
                 --b after-1.json,after-2.json,after-3.json
```

## 6. Run it through the server and workers

```sh
stampede login --server http://localhost:8080
stampede start --file examples/shoplab/scenarios/shop-mix.yaml --target http://shoplab:8090
```

The first time, create a project and a target (`http://shoplab:8090`, the
address the workers see inside Compose) in the web UI. `start` saves the file
as a new scenario version, splits the load across the two workers, and
streams the run until it finishes. The web UI shows the same run live.

Next: [your first real target](guides/first-target.md).
