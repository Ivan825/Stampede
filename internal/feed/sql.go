package feed

import (
	"context"
	"database/sql"
	"fmt"
	"net"
	"regexp"
	"strconv"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/jackc/pgx/v5"
	_ "github.com/jackc/pgx/v5/stdlib" // the pgx database/sql driver
)

// MaxSQLRows bounds a SQL feeder: rows are held in memory for the run.
const MaxSQLRows = 1_000_000

// SQLTimeout bounds the query.
const SQLTimeout = time.Minute

// Drivers maps the scenario's driver names to database/sql drivers.
var Drivers = map[string]string{"postgres": "pgx", "mysql": "mysql"}

var refRe = regexp.MustCompile(`\$\{(env|secret)\.([A-Za-z_][A-Za-z0-9_]*)\}`)

// Expand replaces ${env.X} and ${secret.X} in s.
func Expand(s string, env, secrets map[string]string) string {
	return refRe.ReplaceAllStringFunc(s, func(m string) string {
		p := refRe.FindStringSubmatch(m)
		if p[1] == "env" {
			return env[p[2]]
		}
		return secrets[p[2]]
	})
}

// SQLRows runs query and returns each row as a map of column to value.
// Byte columns become strings. limit caps the rows (0 means MaxSQLRows).
func SQLRows(ctx context.Context, driver, dsn, query string, limit int) ([]any, error) {
	name, ok := Drivers[driver]
	if !ok {
		return nil, fmt.Errorf("sql.driver %q: use postgres or mysql", driver)
	}
	if limit <= 0 || limit > MaxSQLRows {
		limit = MaxSQLRows
	}
	db, err := sql.Open(name, dsn)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(ctx, SQLTimeout)
	defer cancel()
	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("sql query: %w", err)
	}
	defer func() { _ = rows.Close() }()
	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	var out []any
	for rows.Next() {
		if len(out) >= limit {
			break
		}
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		row := make(map[string]any, len(cols))
		for i, c := range cols {
			switch v := vals[i].(type) {
			case []byte:
				row[c] = string(v)
			case time.Time:
				row[c] = v.Format(time.RFC3339Nano)
			default:
				row[c] = v
			}
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

// SQLHost returns the host:port a DSN connects to, so callers can vet it
// like any request host.
func SQLHost(driver, dsn string) (string, error) {
	switch driver {
	case "postgres":
		cfg, err := pgx.ParseConfig(dsn)
		if err != nil {
			return "", fmt.Errorf("sql.dsn: %w", err)
		}
		return net.JoinHostPort(cfg.Host, strconv.Itoa(int(cfg.Port))), nil
	case "mysql":
		cfg, err := mysql.ParseDSN(dsn)
		if err != nil {
			return "", fmt.Errorf("sql.dsn: %w", err)
		}
		if cfg.Net != "tcp" {
			return "", fmt.Errorf("sql.dsn: only tcp connections are supported, not %q", cfg.Net)
		}
		return cfg.Addr, nil
	}
	return "", fmt.Errorf("sql.driver %q: use postgres or mysql", driver)
}
