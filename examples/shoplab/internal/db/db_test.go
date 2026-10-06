package db

import (
	"strings"
	"testing"
	"testing/fstest"

	"github.com/Ivan825/Stampede/examples/shoplab"
)

func TestLoadMigrationsSortedAndFiltered(t *testing.T) {
	fsys := fstest.MapFS{
		"m/0002_b.sql":  {Data: []byte("SELECT 2;")},
		"m/0001_a.sql":  {Data: []byte("SELECT 1;")},
		"m/README.md":   {Data: []byte("ignored")},
		"m/0010_c.sql":  {Data: []byte("SELECT 10;")},
		"m/sub/x.sql":   {Data: []byte("nested files are ignored")},
		"other/9.sql":   {Data: []byte("wrong dir")},
		"m/0003_d.txt":  {Data: []byte("not sql")},
		"m/0000_z.sql~": {Data: []byte("editor backup")},
	}
	got, err := LoadMigrations(fsys, "m")
	if err != nil {
		t.Fatal(err)
	}
	var versions []string
	for _, m := range got {
		versions = append(versions, m.Version)
	}
	if strings.Join(versions, ",") != "0001_a,0002_b,0010_c" {
		t.Fatalf("versions = %v", versions)
	}
	if got[0].SQL != "SELECT 1;" {
		t.Fatalf("sql = %q", got[0].SQL)
	}
}

func TestEmbeddedMigrations(t *testing.T) {
	ms, err := LoadMigrations(shoplab.Migrations, "migrations")
	if err != nil {
		t.Fatal(err)
	}
	if len(ms) == 0 || ms[0].Version != "0001_init" {
		t.Fatalf("unexpected embedded migrations: %+v", ms)
	}
	// The planted bottlenecks rely on these omissions; guard against someone
	// "helpfully" adding them to the base schema.
	var code strings.Builder
	for _, m := range ms {
		for line := range strings.Lines(m.SQL) {
			if !strings.HasPrefix(strings.TrimSpace(line), "--") {
				code.WriteString(line)
			}
		}
	}
	all := code.String()
	if strings.Contains(all, OrdersIndexName) || strings.Contains(all, "ON orders") {
		t.Error("orders (user_id, created_at) index must not be created by migrations")
	}
	if strings.Contains(all, "CHECK (stock") {
		t.Error("products.stock must not carry a CHECK (stock >= 0); the race demo needs it")
	}
}
