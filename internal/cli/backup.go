package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/spf13/cobra"
)

func newBackupCmd() *cobra.Command {
	var dbURL string
	var force bool
	cmd := &cobra.Command{
		Use:   "backup <file>",
		Short: "Write a logical backup of the server's database with pg_dump",
		Long: `Runs pg_dump against the server's database and writes a custom-format
archive (pg_dump -Fc) that stampede restore or pg_restore reads. pg_dump
must be on PATH, at the database server's major version or newer.

The backup holds runs, scenarios, reports and encrypted secrets. Back up
the master key as well, and keep it apart from the dump: without it the
secrets cannot be decrypted, and the two together unlock them. See
docs/deploy/upgrades.md.`,
		Example: `  export STAMPEDE_DATABASE_URL=postgres://stampede@db.internal:5432/stampede PGPASSWORD=...
  stampede backup stampede-$(date +%F).dump`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if dbURL == "" {
				return errors.New("set --database-url or STAMPEDE_DATABASE_URL")
			}
			if _, err := os.Stat(args[0]); err == nil && !force {
				return fmt.Errorf("%s already exists (use --force to overwrite it)", args[0])
			}
			pgDump, err := pgTool("pg_dump")
			if err != nil {
				return err
			}
			conn, env, err := pgConnArgs(dbURL)
			if err != nil {
				return err
			}
			c := exec.CommandContext(cmd.Context(), pgDump, "--format=custom", "--no-owner", "--file="+args[0], "--dbname="+conn) //nolint:gosec // runs pg_dump on the operator's own database
			c.Env, c.Stdout, c.Stderr = env, cmd.ErrOrStderr(), cmd.ErrOrStderr()
			if err := c.Run(); err != nil {
				return fmt.Errorf("pg_dump: %w (its messages are above)", err)
			}
			st, err := os.Stat(args[0])
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Wrote %s (%s). Back up the master key too, kept apart from the dump.\n", args[0], byteSize(st.Size()))
			return nil
		},
	}
	cmd.Flags().StringVar(&dbURL, "database-url", os.Getenv("STAMPEDE_DATABASE_URL"), "PostgreSQL URL of the server's database")
	cmd.Flags().BoolVar(&force, "force", false, "overwrite the file if it exists")
	return cmd
}

func newRestoreCmd() *cobra.Command {
	var dbURL string
	cmd := &cobra.Command{
		Use:   "restore <file>",
		Short: "Restore a backup from stampede backup into an empty database with pg_restore",
		Long: `Restores an archive written by stampede backup (or pg_dump -Fc) with
pg_restore, which must be on PATH. The database named by --database-url
must exist and be empty: create a new one, or drop and recreate the old
one, and stop the server and workers first. A backup of a TimescaleDB
database is restored with TimescaleDB's pre- and post-restore steps, into
a database with the same TimescaleDB version.

Start the server afterwards with the master key that matches the backup;
it applies any migrations the backup is missing.`,
		Example: `  createdb -h db.internal -U stampede stampede
  export STAMPEDE_DATABASE_URL=postgres://stampede@db.internal:5432/stampede PGPASSWORD=...
  stampede restore stampede-2026-10-01.dump`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if dbURL == "" {
				return errors.New("set --database-url or STAMPEDE_DATABASE_URL")
			}
			return restore(cmd.Context(), cmd.OutOrStdout(), cmd.ErrOrStderr(), args[0], dbURL)
		},
	}
	cmd.Flags().StringVar(&dbURL, "database-url", os.Getenv("STAMPEDE_DATABASE_URL"), "PostgreSQL URL of the empty database to restore into")
	return cmd
}

func restore(ctx context.Context, stdout, stderr io.Writer, file, dbURL string) error {
	if _, err := os.Stat(file); err != nil {
		return err
	}
	pgRestore, err := pgTool("pg_restore")
	if err != nil {
		return err
	}
	conn, env, err := pgConnArgs(dbURL)
	if err != nil {
		return err
	}
	// The archive's table of contents says whether it is TimescaleDB's.
	list := exec.CommandContext(ctx, pgRestore, "--list", file) //nolint:gosec // reads the operator's own backup
	list.Stderr = stderr
	toc, err := list.Output()
	if err != nil {
		return fmt.Errorf("%s is not a backup pg_restore can read: %w", file, err)
	}
	timescale := strings.Contains(string(toc), "EXTENSION - timescaledb")

	db, err := pgx.Connect(ctx, dbURL)
	if err != nil {
		return fmt.Errorf("connect to the database: %w", err)
	}
	defer db.Close(context.Background())
	var tables int
	if err := db.QueryRow(ctx, `SELECT count(*) FROM pg_tables WHERE schemaname = 'public'`).Scan(&tables); err != nil {
		return err
	}
	if tables > 0 {
		return fmt.Errorf("the database already has %d tables; restore into a new, empty database (see docs/deploy/upgrades.md)", tables)
	}
	if timescale {
		for _, q := range []string{`CREATE EXTENSION IF NOT EXISTS timescaledb`, `SELECT timescaledb_pre_restore()`} {
			if _, err := db.Exec(ctx, q); err != nil {
				return fmt.Errorf("the backup is of a TimescaleDB database: %s: %w", q, err)
			}
		}
	}
	c := exec.CommandContext(ctx, pgRestore, "--no-owner", "--dbname="+conn, file) //nolint:gosec // restores the operator's own backup
	c.Env, c.Stdout, c.Stderr = env, stderr, stderr
	restoreErr := c.Run()
	if timescale {
		if _, err := db.Exec(context.Background(), `SELECT timescaledb_post_restore()`); err != nil {
			return errors.Join(restoreErr, fmt.Errorf("timescaledb_post_restore: %w", err))
		}
	}
	if restoreErr != nil {
		return fmt.Errorf("pg_restore: %w (its messages are above; check them before starting the server)", restoreErr)
	}
	fmt.Fprintf(stdout, "Restored %s. Start the server with the master key that matches this backup.\n", file)
	return nil
}

// pgTool finds a PostgreSQL client program on PATH.
func pgTool(name string) (string, error) {
	p, err := exec.LookPath(name)
	if err != nil {
		return "", fmt.Errorf("%s is not on PATH: install the PostgreSQL client tools (at the database's major version or newer), or run %s inside the database container as docs/deploy/upgrades.md shows", name, name)
	}
	return p, nil
}

// pgConnArgs moves the password out of a database URL into PGPASSWORD,
// so it does not appear in the process list, and returns the URL to pass
// on the command line with the environment to run with.
func pgConnArgs(dbURL string) (string, []string, error) {
	u, err := url.Parse(dbURL)
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") {
		return "", nil, errors.New("the database URL must look like postgres://user:password@host:5432/stampede")
	}
	env := os.Environ()
	if u.User != nil {
		if pw, ok := u.User.Password(); ok {
			env = append(env, "PGPASSWORD="+pw)
			u.User = url.User(u.User.Username())
		}
	}
	return u.String(), env, nil
}

func byteSize(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1f GiB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MiB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KiB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d bytes", n)
}
