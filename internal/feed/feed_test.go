package feed

import (
	"context"
	"os"
	"strings"
	"testing"
)

func TestGenerator(t *testing.T) {
	spec := map[string]string{
		"name": "name", "email": "email", "user": "username", "id": "uuid", "n": "seq",
		"qty": "int(1,3)", "price": "float(1,2)", "tier": "pick(free|pro)", "blob": "text(64KB)", "when": "date",
	}
	a, err := NewGenerator(spec, 0, 2)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := NewGenerator(spec, 1, 2)
	seen := map[string]bool{}
	for range 500 {
		for _, g := range []*Generator{a, b} {
			r := g.Row()
			e := r["email"].(string)
			if seen[e] {
				t.Fatalf("email %s repeated", e)
			}
			seen[e] = true
			first := strings.ToLower(strings.Fields(r["name"].(string))[0])
			if !strings.HasPrefix(e, first+".") || !strings.HasSuffix(e, "@example.test") {
				t.Fatalf("email %s does not match name %s", e, r["name"])
			}
			if q := r["qty"].(int64); q < 1 || q > 3 {
				t.Fatalf("qty %d", q)
			}
			if p := r["price"].(float64); p < 1 || p > 2 {
				t.Fatalf("price %v", p)
			}
			if tier := r["tier"]; tier != "free" && tier != "pro" {
				t.Fatalf("tier %v", tier)
			}
			if n := len(r["blob"].(string)); n != 64<<10 {
				t.Fatalf("blob %d bytes", n)
			}
			if _, ok := r["\x00first"]; ok {
				t.Fatal("helper field leaked into the row")
			}
		}
	}
	if a.Row()["n"].(int64)%2 != 0 || b.Row()["n"].(int64)%2 != 1 {
		t.Error("seq must interleave by worker")
	}
}

func TestCompileErrors(t *testing.T) {
	for _, k := range []string{"nope", "int(5,1)", "int(1)", "int(1.5,2)", "pick()", "text(0)", "text(100GB)", "email(1)"} {
		if _, err := Compile(k); err == nil {
			t.Errorf("%q compiled", k)
		}
	}
}

func TestExpandAndHost(t *testing.T) {
	dsn := Expand("postgres://u:${secret.PW}@${env.HOST}:5433/db", map[string]string{"HOST": "db.internal"}, map[string]string{"PW": "s"})
	if dsn != "postgres://u:s@db.internal:5433/db" {
		t.Fatal(dsn)
	}
	if h, err := SQLHost("postgres", dsn); err != nil || h != "db.internal:5433" {
		t.Fatalf("%s %v", h, err)
	}
	if h, err := SQLHost("mysql", "u:p@tcp(10.0.0.9:3306)/shop"); err != nil || h != "10.0.0.9:3306" {
		t.Fatalf("%s %v", h, err)
	}
	if _, err := SQLHost("mysql", "u:p@unix(/tmp/my.sock)/shop"); err == nil {
		t.Fatal("unix socket accepted")
	}
}

func TestSQLRows(t *testing.T) {
	dsn := os.Getenv("STAMPEDE_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("STAMPEDE_TEST_DATABASE_URL not set")
	}
	rows, err := SQLRows(context.Background(), "postgres", dsn,
		"SELECT g AS id, 'user' || g AS name, now() AS at FROM generate_series(1, 50) g", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 10 {
		t.Fatalf("%d rows", len(rows))
	}
	r := rows[3].(map[string]any)
	if r["id"] != int64(4) || r["name"] != "user4" || r["at"] == "" {
		t.Fatalf("row %#v", r)
	}
}
