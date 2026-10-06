package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	pluginv1 "github.com/Ivan825/Stampede/gen/stampede/plugin/v1"
	"github.com/Ivan825/Stampede/internal/pluginhost/plugintest/runtest"
	"github.com/Ivan825/Stampede/internal/scenario"
	"github.com/Ivan825/Stampede/pkg/pluginsdk"
	"github.com/Ivan825/Stampede/pkg/pluginsdk/conformance"
)

// sqliteDSN is a fresh SQLite database file that tolerates concurrent
// users.
func sqliteDSN(t *testing.T) string {
	t.Helper()
	return "file:" + filepath.Join(t.TempDir(), "test.db") + "?_pragma=busy_timeout(10000)&_pragma=journal_mode(WAL)"
}

type harness struct {
	t   *testing.T
	srv pluginv1.PluginServiceServer
	ses string
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	srv, err := pluginsdk.NewServer(newPlugin())
	if err != nil {
		t.Fatal(err)
	}
	o, err := srv.Open(context.Background(), &pluginv1.OpenRequest{Vu: 1})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = srv.Close(context.Background(), &pluginv1.CloseRequest{Session: o.GetSession()}) })
	return &harness{t: t, srv: srv, ses: o.GetSession()}
}

func (h *harness) run(step string, cfg map[string]any) (*pluginv1.ExecuteResponse, map[string]any) {
	h.t.Helper()
	b, _ := json.Marshal(cfg)
	r, err := h.srv.Execute(context.Background(), &pluginv1.ExecuteRequest{Session: h.ses, Step: step, Config: b, TimeoutNs: int64(10 * time.Second)})
	if err != nil {
		h.t.Fatal(err)
	}
	var vals map[string]any
	_ = json.Unmarshal(r.GetValues(), &vals)
	return r, vals
}

// exercise runs the same checks against any database; ph renders the
// n-th parameter placeholder.
func exercise(t *testing.T, driver, dsn string, ph func(int) string) {
	h := newHarness(t)
	db := func(m map[string]any) map[string]any {
		m["driver"], m["dsn"] = driver, dsn
		return m
	}
	mustOK := func(step string, m map[string]any) map[string]any {
		t.Helper()
		r, v := h.run(step, db(m))
		if !r.GetOk() {
			t.Fatalf("%s %v: %s: %s", step, m["sql"], r.GetErrorClass(), r.GetError())
		}
		return v
	}
	mustOK("exec", map[string]any{"sql": "DROP TABLE IF EXISTS stampede_products"})
	mustOK("exec", map[string]any{"sql": "CREATE TABLE stampede_products (id INTEGER PRIMARY KEY, name VARCHAR(50) NOT NULL, price DOUBLE PRECISION)"})
	for i, name := range []string{"lamp", "desk", "chair"} {
		v := mustOK("exec", map[string]any{
			"sql":  "INSERT INTO stampede_products (id, name, price) VALUES (" + ph(1) + ", " + ph(2) + ", " + ph(3) + ")",
			"args": []any{i + 1, name, 9.5 + float64(i)},
		})
		if v["rowsAffected"] != float64(1) {
			t.Fatalf("insert: %v", v)
		}
	}
	r, v := h.run("query", db(map[string]any{"sql": "SELECT id, name, price FROM stampede_products WHERE id >= " + ph(1) + " ORDER BY id", "args": []any{1}, "rows": 2}))
	if !r.GetOk() || v["rowCount"] != float64(3) || r.GetPhasesNs()["wait"] == 0 {
		t.Fatalf("query: %+v %v", r, v)
	}
	first, _ := v["first"].(map[string]any)
	if first["name"] != "lamp" || first["price"] != 9.5 || first["id"] != float64(1) {
		t.Fatalf("first row %v", first)
	}
	if rows, _ := v["rows"].([]any); len(rows) != 2 {
		t.Fatalf("rows %v", v["rows"])
	}
	if r, v := h.run("query", db(map[string]any{"sql": "SELECT name FROM stampede_products WHERE id = " + ph(1), "args": []any{99}})); !r.GetOk() || v["rowCount"] != float64(0) || v["first"] != nil {
		t.Fatalf("no rows: %+v %v", r, v)
	}
	r, _ = h.run("exec", db(map[string]any{"sql": "INSERT INTO stampede_products (id, name) VALUES (1, 'dup')"}))
	if r.GetOk() || r.GetErrorClass() == "sql error" || r.GetErrorClass() == "" {
		t.Fatalf("a duplicate key should fail with the database's code: %+v", r)
	}
	t.Logf("duplicate key class: %s", r.GetErrorClass())
	if r, _ := h.run("query", db(map[string]any{"sql": "SELEKT 1"})); r.GetOk() {
		t.Fatal("a syntax error should fail")
	}
	// A shared pool works the same way.
	if r, v := h.run("query", db(map[string]any{"sql": "SELECT COUNT(*) AS n FROM stampede_products WHERE id > " + ph(1), "args": []any{0}, "pool": "shared", "maxConns": 2})); !r.GetOk() || v["first"].(map[string]any)["n"] != float64(3) {
		t.Fatalf("shared pool: %+v %v", r, v)
	}
	mustOK("exec", map[string]any{"sql": "DROP TABLE stampede_products"})
}

func question(int) string { return "?" }

func dollar(n int) string { return "$" + string(rune('0'+n)) }

func TestSQLite(t *testing.T) {
	exercise(t, "sqlite", sqliteDSN(t), question)
}

// TestPostgres and TestMySQL run in CI against service containers; they
// skip without a DSN.
func TestPostgres(t *testing.T) {
	dsn := os.Getenv("STAMPEDE_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set STAMPEDE_TEST_POSTGRES_DSN to run against PostgreSQL")
	}
	exercise(t, "postgres", dsn, dollar)
}

func TestMySQL(t *testing.T) {
	dsn := os.Getenv("STAMPEDE_TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("set STAMPEDE_TEST_MYSQL_DSN to run against MySQL")
	}
	exercise(t, "mysql", dsn, question)
}

func TestConnectionErrors(t *testing.T) {
	h := newHarness(t)
	r, _ := h.run("query", map[string]any{"driver": "postgres", "dsn": "postgres://u:p@127.0.0.1:1/db?connect_timeout=2", "sql": "SELECT 1"})
	if r.GetOk() || r.GetErrorClass() != "sql connection error" {
		t.Fatalf("postgres down: %+v", r)
	}
	r, _ = h.run("query", map[string]any{"driver": "mysql", "dsn": "u:p@tcp(127.0.0.1:1)/db?timeout=2s", "sql": "SELECT 1"})
	if r.GetOk() || r.GetErrorClass() != "sql connection error" {
		t.Fatalf("mysql down: %+v", r)
	}
}

func TestExample(t *testing.T) {
	dsn := sqliteDSN(t)
	h := newHarness(t)
	for _, q := range []string{
		"CREATE TABLE products (id INTEGER PRIMARY KEY, name TEXT, stock INTEGER)",
		"WITH RECURSIVE s(v) AS (SELECT 1 UNION ALL SELECT v + 1 FROM s WHERE v < 50) INSERT INTO products (id, name, stock) SELECT v, 'product ' || v, 1000000 FROM s",
	} {
		if r, _ := h.run("exec", map[string]any{"driver": "sqlite", "dsn": dsn, "sql": q}); !r.GetOk() {
			t.Fatalf("%s: %s", q, r.GetError())
		}
	}
	bin := conformance.Build(t, ".", "sql")
	res := runtest.Run(t, "examples/catalog.yaml", filepath.Dir(bin),
		map[string]string{"SQL_DRIVER": "sqlite", "SQL_DSN": dsn},
		&scenario.Load{VUs: 8, Iterations: 80})
	for _, name := range []string{"product page", "reserve stock"} {
		st := res.Step(t, name)
		if st.Requests != 80 || st.Failed != 0 || st.Protocols["sql"] != 80 {
			t.Errorf("%s: %d requests, %d failed (%v)", name, st.Requests, st.Failed, st.Errors)
		}
	}
}

func TestConformance(t *testing.T) {
	dsn := sqliteDSN(t)
	h := newHarness(t)
	if r, _ := h.run("exec", map[string]any{"driver": "sqlite", "dsn": dsn, "sql": "CREATE TABLE t (n INTEGER)"}); !r.GetOk() {
		t.Fatal(r.GetError())
	}
	conformance.Run(t, conformance.Build(t, ".", "sql"), conformance.Options{
		Cases: []conformance.Case{
			{Step: "exec", Config: map[string]any{"driver": "sqlite", "dsn": dsn, "sql": "INSERT INTO t (n) VALUES (?)", "args": []any{1}}},
			{Step: "query", Config: map[string]any{"driver": "sqlite", "dsn": dsn, "sql": "SELECT COUNT(*) FROM t"}},
			{Step: "query", Config: map[string]any{"driver": "sqlite", "dsn": dsn, "sql": "SELECT * FROM missing"}, WantClass: "sql SQLITE_ERROR"},
		},
		VUs: 20,
	})
}
