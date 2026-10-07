package cli

import (
	"context"
	"io"
	"log/slog"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/Ivan825/Stampede/internal/store"
	"github.com/Ivan825/Stampede/internal/store/storetest"
)

// TestBackupRestore backs up a migrated database with a row in it and
// restores it into an empty one. It needs pg_dump and pg_restore at the
// test database's version or newer.
func TestBackupRestore(t *testing.T) {
	for _, tool := range []string{"pg_dump", "pg_restore"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s is not on PATH", tool)
		}
	}
	ctx := context.Background()
	src := storetest.URL(t)
	skipOlderPgDump(t, src)
	st, err := store.Open(ctx, src)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Migrate(ctx, slog.New(slog.NewTextHandler(io.Discard, nil)), false); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Pool.Exec(ctx, `INSERT INTO orgs (id, name) VALUES (gen_random_uuid(), 'Backed up')`); err != nil {
		t.Fatal(err)
	}
	st.Close()

	file := filepath.Join(t.TempDir(), "stampede.dump")
	out, err := runCLI(t, "backup", file, "--database-url", src)
	if err != nil || !strings.Contains(out, "Wrote "+file) {
		t.Fatalf("backup: %v\n%s", err, out)
	}
	if _, err := runCLI(t, "backup", file, "--database-url", src); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Errorf("backup over an existing file: %v", err)
	}

	dst := storetest.URL(t)
	out, err = runCLI(t, "restore", file, "--database-url", dst)
	if err != nil || !strings.Contains(out, "Restored "+file) {
		t.Fatalf("restore: %v\n%s", err, out)
	}
	conn, err := pgx.Connect(ctx, dst)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	var name string
	if err := conn.QueryRow(ctx, `SELECT name FROM orgs`).Scan(&name); err != nil || name != "Backed up" {
		t.Errorf("restored row: %q %v", name, err)
	}
	// The restored database is current: the server has nothing to migrate.
	rs, err := store.Open(ctx, dst)
	if err != nil {
		t.Fatal(err)
	}
	defer rs.Close()
	if err := rs.Migrate(ctx, slog.New(slog.NewTextHandler(io.Discard, nil)), false); err != nil {
		t.Errorf("migrate after restore: %v", err)
	}

	if _, err := runCLI(t, "restore", file, "--database-url", dst); err == nil || !strings.Contains(err.Error(), "already has") {
		t.Errorf("restore into a database with tables: %v", err)
	}
}

// skipOlderPgDump skips when pg_dump is older than the database server,
// which pg_dump refuses to dump.
func skipOlderPgDump(t *testing.T, dbURL string) {
	t.Helper()
	b, err := exec.Command("pg_dump", "--version").Output()
	if err != nil {
		t.Skipf("pg_dump --version: %v", err)
	}
	var client int
	if m := regexp.MustCompile(`(\d+)\.\d+`).FindSubmatch(b); m != nil {
		client, _ = strconv.Atoi(string(m[1]))
	}
	conn, err := pgx.Connect(context.Background(), dbURL)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(context.Background())
	var server int
	if err := conn.QueryRow(context.Background(), `SELECT current_setting('server_version_num')::int / 10000`).Scan(&server); err != nil {
		t.Fatal(err)
	}
	if client < server {
		t.Skipf("pg_dump %d is older than the database server (%d)", client, server)
	}
}

func TestBackupNeedsTools(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	t.Setenv("STAMPEDE_DATABASE_URL", "")
	if _, err := runCLI(t, "backup", "x.dump"); err == nil || !strings.Contains(err.Error(), "--database-url") {
		t.Errorf("no database URL: %v", err)
	}
	if _, err := runCLI(t, "backup", filepath.Join(t.TempDir(), "x.dump"), "--database-url", "postgres://u:p@localhost/db"); err == nil || !strings.Contains(err.Error(), "pg_dump is not on PATH") {
		t.Errorf("no pg_dump: %v", err)
	}
	if _, err := runCLI(t, "restore", "backup_test.go", "--database-url", "postgres://u:p@localhost/db"); err == nil || !strings.Contains(err.Error(), "pg_restore is not on PATH") {
		t.Errorf("no pg_restore: %v", err)
	}
}

func TestPgConnArgs(t *testing.T) {
	u, env, err := pgConnArgs("postgres://stampede:s3cret@db:5432/stampede?sslmode=disable")
	if err != nil || u != "postgres://stampede@db:5432/stampede?sslmode=disable" || env[len(env)-1] != "PGPASSWORD=s3cret" {
		t.Errorf("%s %v %v", u, env[len(env)-1], err)
	}
	if _, _, err := pgConnArgs("mysql://x"); err == nil {
		t.Error("a non-PostgreSQL URL was accepted")
	}
}
