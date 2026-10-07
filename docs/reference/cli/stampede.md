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

* [stampede agent](stampede_agent.md)	 - Inject faults into dependencies during a load test
* [stampede ai](stampede_ai.md)	 - Manage the server's AI providers and journey generation jobs
* [stampede audit](stampede_audit.md)	 - Read the organisation's audit log, newest first (admin)
* [stampede backup](stampede_backup.md)	 - Write a logical backup of the server's database with pg_dump
* [stampede caps](stampede_caps.md)	 - Show or set the organisation's hard caps on every run
* [stampede compare](stampede_compare.md)	 - Compare two versions using repeated runs of each
* [stampede completion](stampede_completion.md)	 - Generate the autocompletion script for the specified shell
* [stampede coverage](stampede_coverage.md)	 - Show which API endpoints a scenario's journeys exercise and which none does
* [stampede doctor](stampede_doctor.md)	 - Check the server, its database, workers and a target
* [stampede down](stampede_down.md)	 - Stop the local Docker Compose stack
* [stampede drift](stampede_drift.md)	 - Find journeys an API change broke
* [stampede generate](stampede_generate.md)	 - Draft a scenario with an AI model and dry-run every journey (optional, bring your own key)
* [stampede init](stampede_init.md)	 - Detect what kind of product a target is and set up the matching pack
* [stampede integrations](stampede_integrations.md)	 - List, add and delete Prometheus, trace and agent integrations (admin)
* [stampede keygen](stampede_keygen.md)	 - Print a new random master key for STAMPEDE_MASTER_KEY
* [stampede kill](stampede_kill.md)	 - Kill switch: stop a run's load immediately
* [stampede login](stampede_login.md)	 - Sign in to a Stampede server and store an API token for this CLI
* [stampede logout](stampede_logout.md)	 - Revoke this CLI's API token and forget it
* [stampede narrative](stampede_narrative.md)	 - Have the server's AI provider write a summary of a finished run
* [stampede notify](stampede_notify.md)	 - Manage notification channels and read their delivery log (admin)
* [stampede pack](stampede_pack.md)	 - List, install, test and create product packs
* [stampede password](stampede_password.md)	 - Change your password
* [stampede plugin](stampede_plugin.md)	 - List, install, remove and create protocol plugins
* [stampede projects](stampede_projects.md)	 - List and manage projects, their settings and per-project roles
* [stampede push](stampede_push.md)	 - Save local scenario files to the server as new versions
* [stampede report](stampede_report.md)	 - Render a saved JSON report as HTML, PDF, CSV, JUnit, Markdown or a summary
* [stampede restore](stampede_restore.md)	 - Restore a backup from stampede backup into an empty database with pg_restore
* [stampede run](stampede_run.md)	 - Run a scenario in-process and write a report (no server needed)
* [stampede runs](stampede_runs.md)	 - List runs on the server, and show a run's details, events, timeline and workers
* [stampede scenarios](stampede_scenarios.md)	 - List, show and delete the scenarios saved on the server
* [stampede schedules](stampede_schedules.md)	 - List and manage scheduled runs on the server
* [stampede secrets](stampede_secrets.md)	 - List, set and delete a project's secrets
* [stampede server](stampede_server.md)	 - Run the control plane: REST API, run manager and web UI
* [stampede settings](stampede_settings.md)	 - Show the server's single sign-on configuration and run limits (admin)
* [stampede setup](stampede_setup.md)	 - Create the first organisation and owner account on a new server
* [stampede start](stampede_start.md)	 - Start a run on a Stampede server and follow it live
* [stampede stop](stampede_stop.md)	 - Stop a run gracefully (in-flight iterations may finish)
* [stampede targets](stampede_targets.md)	 - List and manage a project's targets, and prove you own them
* [stampede tokens](stampede_tokens.md)	 - List, create and revoke your API tokens
* [stampede up](stampede_up.md)	 - Start the full stack with Docker Compose: server and web UI, workers, database
* [stampede users](stampede_users.md)	 - List and manage the organisation's members and their roles
* [stampede validate](stampede_validate.md)	 - Check scenario files without running them
* [stampede version](stampede_version.md)	 - Print version information
* [stampede whoami](stampede_whoami.md)	 - Show who you are signed in as, and where
* [stampede worker](stampede_worker.md)	 - Run a load-generating worker for a Stampede server
* [stampede workers](stampede_workers.md)	 - List workers connected to the server

