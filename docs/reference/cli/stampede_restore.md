## stampede restore

Restore a backup from stampede backup into an empty database with pg_restore

### Synopsis

Restores an archive written by stampede backup (or pg_dump -Fc) with
pg_restore, which must be on PATH. The database named by --database-url
must exist and be empty: create a new one, or drop and recreate the old
one, and stop the server and workers first. A backup of a TimescaleDB
database is restored with TimescaleDB's pre- and post-restore steps, into
a database with the same TimescaleDB version.

Start the server afterwards with the master key that matches the backup;
it applies any migrations the backup is missing.

```
stampede restore <file> [flags]
```

### Examples

```
  createdb -h db.internal -U stampede stampede
  export STAMPEDE_DATABASE_URL=postgres://stampede@db.internal:5432/stampede PGPASSWORD=...
  stampede restore stampede-2026-10-01.dump
```

### Options

```
      --database-url string   PostgreSQL URL of the empty database to restore into
  -h, --help                  help for restore
```

### SEE ALSO

* [stampede](stampede.md)	 - Self-hosted, distributed load testing

