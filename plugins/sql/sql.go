package main

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/go-sql-driver/mysql"
	"github.com/jackc/pgx/v5/pgconn"
	_ "github.com/jackc/pgx/v5/stdlib" // registers the pgx driver
	"modernc.org/sqlite"

	"github.com/Ivan825/Stampede/pkg/pluginsdk"
)

const version = "0.1.0"

// drivers maps the driver names scenarios use to database/sql drivers.
var drivers = map[string]string{"postgres": "pgx", "mysql": "mysql", "sqlite": "sqlite"}

const common = `
    "driver": {"enum": ["postgres", "mysql", "sqlite"]},
    "dsn": {"type": "string", "minLength": 1, "x-stampede-target": true, "description": "postgres://user@host:5432/db or host=... key=value pairs, user@tcp(host:3306)/db for MySQL, file:path.db for SQLite."},
    "sql": {"type": "string", "minLength": 1},
    "args": {"type": "array", "description": "Positional parameters: $1, $2 for PostgreSQL and SQLite; ? for MySQL and SQLite."},
    "pool": {"enum": ["per-vu", "shared"], "description": "per-vu (default): each user holds one connection; shared: one pool for all users of this plugin process."},
    "maxConns": {"type": "integer", "minimum": 1, "description": "Size of the shared pool (default 10)."}`

const querySchema = `{
  "type": "object", "additionalProperties": false,
  "required": ["driver", "dsn", "sql"],
  "properties": {` + common + `,
    "rows": {"type": "integer", "minimum": 0, "description": "Also return up to this many rows as a list (default 0: only the first)."}
  }
}`

const execSchema = `{
  "type": "object", "additionalProperties": false,
  "required": ["driver", "dsn", "sql"],
  "properties": {` + common + `}
}`

func newPlugin() *pluginsdk.Plugin {
	p := &plugin{shared: map[string]*sql.DB{}}
	return &pluginsdk.Plugin{
		Name:        "sql",
		Version:     version,
		Description: "Runs SQL queries and statements against PostgreSQL, MySQL and SQLite.",
		NewSession: func(context.Context, pluginsdk.SessionInfo) (any, error) {
			return &session{p: p, dbs: map[string]*sql.DB{}}, nil
		},
		Steps: []pluginsdk.Step{
			{Name: "query", Description: "Run a query; returns the row count and the first row.", Schema: querySchema, Run: query},
			{Name: "exec", Description: "Run a statement; returns the rows affected.", Schema: execSchema, Run: execStep},
		},
	}
}

// plugin holds the shared pools.
type plugin struct {
	mu     sync.Mutex
	shared map[string]*sql.DB
}

type session struct {
	p   *plugin
	dbs map[string]*sql.DB
}

func (s *session) Close() error {
	var errs []error
	for _, db := range s.dbs {
		errs = append(errs, db.Close())
	}
	return errors.Join(errs...)
}

type config struct {
	Driver   string `json:"driver"`
	DSN      string `json:"dsn"`
	SQL      string `json:"sql"`
	Args     []any  `json:"args"`
	Pool     string `json:"pool"`
	MaxConns int    `json:"maxConns"`
	Rows     int    `json:"rows"`
}

// args turns JSON numbers that are whole into integers, so integer
// columns accept them.
func (cfg *config) args() []any {
	out := make([]any, len(cfg.Args))
	for i, a := range cfg.Args {
		if f, ok := a.(float64); ok && f == float64(int64(f)) {
			out[i] = int64(f)
		} else {
			out[i] = a
		}
	}
	return out
}

func (s *session) db(cfg *config) (*sql.DB, error) {
	key := cfg.Driver + "|" + cfg.DSN
	if cfg.Pool == "shared" {
		s.p.mu.Lock()
		defer s.p.mu.Unlock()
		if db := s.p.shared[key]; db != nil {
			return db, nil
		}
		db, err := open(cfg, max(cfg.MaxConns, 0))
		if err != nil {
			return nil, err
		}
		s.p.shared[key] = db
		return db, nil
	}
	if db := s.dbs[key]; db != nil {
		return db, nil
	}
	db, err := open(cfg, 1)
	if err != nil {
		return nil, err
	}
	s.dbs[key] = db
	return db, nil
}

func open(cfg *config, conns int) (*sql.DB, error) {
	if conns == 0 {
		conns = 10
	}
	db, err := sql.Open(drivers[cfg.Driver], cfg.DSN)
	if err != nil {
		return nil, pluginsdk.Fail("invalid config", err)
	}
	db.SetMaxOpenConns(conns)
	db.SetMaxIdleConns(conns)
	db.SetConnMaxIdleTime(5 * time.Minute)
	return db, nil
}

func query(ctx context.Context, c *pluginsdk.Call) (*pluginsdk.Result, error) {
	var cfg config
	if err := c.Decode(&cfg); err != nil {
		return nil, err
	}
	db, err := c.Session.(*session).db(&cfg)
	if err != nil {
		return nil, err
	}
	start := time.Now()
	rows, err := db.QueryContext(ctx, cfg.SQL, cfg.args()...)
	if err != nil {
		return &pluginsdk.Result{Latency: time.Since(start)}, classify(ctx, err)
	}
	defer func() { _ = rows.Close() }()
	wait := time.Since(start)
	cols, err := rows.Columns()
	if err != nil {
		return &pluginsdk.Result{Latency: time.Since(start)}, classify(ctx, err)
	}
	var (
		count   int
		first   map[string]any
		list    []map[string]any
		bytesIn int64
	)
	vals := make([]any, len(cols))
	ptrs := make([]any, len(cols))
	for i := range vals {
		ptrs[i] = &vals[i]
	}
	for rows.Next() {
		if err := rows.Scan(ptrs...); err != nil {
			return &pluginsdk.Result{Latency: time.Since(start)}, classify(ctx, err)
		}
		count++
		if count == 1 || count <= cfg.Rows {
			row := make(map[string]any, len(cols))
			for i, col := range cols {
				row[col] = jsonable(vals[i])
			}
			if count == 1 {
				first = row
			}
			if count <= cfg.Rows {
				list = append(list, row)
			}
		}
		for _, v := range vals {
			bytesIn += size(v)
		}
	}
	if err := rows.Err(); err != nil {
		return &pluginsdk.Result{Latency: time.Since(start)}, classify(ctx, err)
	}
	lat := time.Since(start)
	out := map[string]any{"rowCount": count, "columns": cols, "first": first}
	if cfg.Rows > 0 {
		if list == nil {
			list = []map[string]any{}
		}
		out["rows"] = list
	}
	return &pluginsdk.Result{
		Latency: lat, Phases: pluginsdk.Phases{Wait: wait, Download: lat - wait},
		BytesIn: bytesIn, BytesOut: int64(len(cfg.SQL)), Values: out,
	}, nil
}

func execStep(ctx context.Context, c *pluginsdk.Call) (*pluginsdk.Result, error) {
	var cfg config
	if err := c.Decode(&cfg); err != nil {
		return nil, err
	}
	db, err := c.Session.(*session).db(&cfg)
	if err != nil {
		return nil, err
	}
	start := time.Now()
	r, err := db.ExecContext(ctx, cfg.SQL, cfg.args()...)
	lat := time.Since(start)
	if err != nil {
		return &pluginsdk.Result{Latency: lat}, classify(ctx, err)
	}
	out := map[string]any{}
	if n, err := r.RowsAffected(); err == nil {
		out["rowsAffected"] = n
	}
	if id, err := r.LastInsertId(); err == nil {
		out["lastInsertId"] = id
	}
	return &pluginsdk.Result{Latency: lat, BytesOut: int64(len(cfg.SQL)), Values: out}, nil
}

// jsonable converts a scanned value for the step's JSON values.
func jsonable(v any) any {
	switch x := v.(type) {
	case []byte:
		if utf8.Valid(x) {
			return string(x)
		}
		return base64.StdEncoding.EncodeToString(x)
	case time.Time:
		return x.Format(time.RFC3339Nano)
	case driver.Valuer:
		dv, err := x.Value()
		if err != nil {
			return fmt.Sprint(x)
		}
		return jsonable(dv)
	case nil, string, bool, int64, float64, int32, int, float32:
		return x
	}
	return fmt.Sprint(v)
}

func size(v any) int64 {
	switch x := v.(type) {
	case []byte:
		return int64(len(x))
	case string:
		return int64(len(x))
	case nil:
		return 0
	}
	return 8
}

// sqliteCode names an SQLite result code by its primary code, such as
// SQLITE_CONSTRAINT or SQLITE_BUSY.
func sqliteCode(code int) string {
	desc := sqlite.ErrorCodeString[code&0xff]
	open, end := strings.LastIndex(desc, "("), strings.LastIndex(desc, ")")
	if open < 0 || end < open {
		return ""
	}
	return desc[open+1 : end]
}

func classify(ctx context.Context, err error) error {
	var (
		pg *pgconn.PgError
		my *mysql.MySQLError
		lt *sqlite.Error
		ne net.Error
	)
	switch {
	case ctx.Err() != nil || errors.Is(err, context.DeadlineExceeded):
		return pluginsdk.Fail("sql timeout", err)
	case errors.As(err, &pg):
		// SQLSTATE codes are bounded: 23505 unique violation, 40001
		// serialization failure, 53300 too many connections...
		return pluginsdk.Fail("sql "+pg.Code, err)
	case errors.As(err, &my):
		if st := strings.TrimRight(string(my.SQLState[:]), "\x00"); st != "" {
			return pluginsdk.Fail("sql "+st, err)
		}
		return pluginsdk.Fail(fmt.Sprintf("sql mysql %d", my.Number), err)
	case errors.As(err, &lt):
		if name := sqliteCode(lt.Code()); name != "" {
			return pluginsdk.Fail("sql "+name, err)
		}
		return pluginsdk.Fail("sql error", err)
	case errors.As(err, &ne), errors.Is(err, driver.ErrBadConn), strings.Contains(err.Error(), "connect"):
		return pluginsdk.Fail("sql connection error", err)
	}
	return pluginsdk.Fail("sql error", err)
}
