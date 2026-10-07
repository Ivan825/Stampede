## stampede backup

Write a logical backup of the server's database with pg_dump

### Synopsis

Runs pg_dump against the server's database and writes a custom-format
archive (pg_dump -Fc) that stampede restore or pg_restore reads. pg_dump
must be on PATH, at the database server's major version or newer.

The backup holds runs, scenarios, reports and encrypted secrets. Back up
the master key as well, and keep it apart from the dump: without it the
secrets cannot be decrypted, and the two together unlock them. See
docs/deploy/upgrades.md.

```
stampede backup <file> [flags]
```

### Examples

```
  export STAMPEDE_DATABASE_URL=postgres://stampede@db.internal:5432/stampede PGPASSWORD=...
  stampede backup stampede-$(date +%F).dump
```

### Options

```
      --database-url string   PostgreSQL URL of the server's database
      --force                 overwrite the file if it exists
  -h, --help                  help for backup
```

### SEE ALSO

* [stampede](stampede.md)	 - Self-hosted, distributed load testing

