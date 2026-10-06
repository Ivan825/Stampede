## stampede

Self-hosted, distributed load testing

### Synopsis

Stampede describes how your users behave, validates each journey with a
single user, then runs thousands of them across distributed workers and
reports where your product breaks.

```
stampede [flags]
```

### Options

```
  -h, --help   help for stampede
```

### SEE ALSO

* [stampede compare](stampede_compare.md)	 - Compare two versions using repeated runs of each
* [stampede completion](stampede_completion.md)	 - Generate the autocompletion script for the specified shell
* [stampede doctor](stampede_doctor.md)	 - Check the server, its database, workers and a target
* [stampede down](stampede_down.md)	 - Stop the local Docker Compose stack
* [stampede generate](stampede_generate.md)	 - Draft a scenario with an AI model and dry-run every journey (optional, bring your own key)
* [stampede init](stampede_init.md)	 - Detect what kind of product a target is and set up the matching pack
* [stampede keygen](stampede_keygen.md)	 - Print a new random master key for STAMPEDE_MASTER_KEY
* [stampede kill](stampede_kill.md)	 - Kill switch: stop a run's load immediately
* [stampede login](stampede_login.md)	 - Sign in to a Stampede server and store an API token for this CLI
* [stampede pack](stampede_pack.md)	 - List, install and test product packs
* [stampede push](stampede_push.md)	 - Save local scenario files to the server as new versions
* [stampede report](stampede_report.md)	 - Render a saved JSON report as HTML, JUnit, Markdown or a summary
* [stampede run](stampede_run.md)	 - Run a scenario in-process and write a report (no server needed)
* [stampede runs](stampede_runs.md)	 - List recent runs on the server
* [stampede server](stampede_server.md)	 - Run the control plane: REST API, run manager and web UI
* [stampede start](stampede_start.md)	 - Start a run on a Stampede server and follow it live
* [stampede stop](stampede_stop.md)	 - Stop a run gracefully (in-flight iterations may finish)
* [stampede target](stampede_target.md)	 - Manage load test targets
* [stampede up](stampede_up.md)	 - Start the full local stack with Docker Compose (server, workers, database, ShopLab)
* [stampede validate](stampede_validate.md)	 - Check scenario files without running them
* [stampede version](stampede_version.md)	 - Print version information
* [stampede worker](stampede_worker.md)	 - Run a load-generating worker for a Stampede server
* [stampede workers](stampede_workers.md)	 - List workers connected to the server

