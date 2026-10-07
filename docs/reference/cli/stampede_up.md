## stampede up

Start the full stack with Docker Compose: server and web UI, workers, database

### Synopsis

Start the full stack with Docker Compose. Inside a clone of the repository
it runs the repository's docker-compose.yml, including the ShopLab demo
app. Anywhere else it runs the released images of this version (server and
web UI on :8080, two workers, TimescaleDB), with no clone needed.

```
stampede up [flags]
```

### Options

```
      --build   build images from this clone instead of using existing ones
  -h, --help    help for up
```

### SEE ALSO

* [stampede](stampede.md)	 - Self-hosted, distributed load testing

