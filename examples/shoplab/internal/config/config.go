// Package config loads ShopLab's runtime configuration from environment
// variables. Every planted bottleneck has a matching SHOPLAB_FIX_* flag;
// SHOPLAB_FIX_ALL turns all of them on at once.
package config

import (
	"fmt"
	"log/slog"
	"strings"
	"time"
)

// Fixes records which planted bottlenecks are fixed. The zero value is the
// "broken" demo mode in which every bottleneck is present.
type Fixes struct {
	N1    bool // product listing uses one joined query instead of N+1
	Index bool // orders(user_id, created_at) index is created at startup
	Pool  bool // pgxpool MaxConns 40 instead of 5
	Cache bool // singleflight + jittered TTL + stale-while-revalidate
	Race  bool // atomic conditional stock decrement in checkout
	Leak  bool // session janitor and no per-session buffer
}

// Map returns the fixes keyed by their short name, in a stable form that is
// convenient for JSON output and metric labels.
func (f Fixes) Map() map[string]bool {
	return map[string]bool{
		"n1":    f.N1,
		"index": f.Index,
		"pool":  f.Pool,
		"cache": f.Cache,
		"race":  f.Race,
		"leak":  f.Leak,
	}
}

// Config is the full runtime configuration.
type Config struct {
	Addr        string
	DatabaseURL string
	RedisURL    string
	Admin       bool
	LogLevel    slog.Level
	Fixes       Fixes

	// CacheTTL is the product-detail cache TTL (fresh period).
	CacheTTL time.Duration
	// SessionTTL is how long a login session stays valid.
	SessionTTL time.Duration
}

// Defaults used when the corresponding variable is unset.
const (
	DefaultAddr        = ":8090"
	DefaultDatabaseURL = "postgres://shoplab:shoplab@localhost:5432/shoplab?sslmode=disable"
	DefaultRedisURL    = "redis://localhost:6379/0"
	DefaultCacheTTL    = 10 * time.Second
	DefaultSessionTTL  = 15 * time.Minute
)

// Truthy reports whether s is one of 1/true/yes/on (case-insensitive).
func Truthy(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "1", "true", "yes", "on", "y", "t":
		return true
	}
	return false
}

// Load builds a Config from getenv (normally os.Getenv).
func Load(getenv func(string) string) (Config, error) {
	str := func(key, def string) string {
		if v := strings.TrimSpace(getenv(key)); v != "" {
			return v
		}
		return def
	}
	dur := func(key string, def time.Duration) (time.Duration, error) {
		v := strings.TrimSpace(getenv(key))
		if v == "" {
			return def, nil
		}
		d, err := time.ParseDuration(v)
		if err != nil || d <= 0 {
			return 0, fmt.Errorf("config: %s must be a positive duration like 10s, got %q", key, v)
		}
		return d, nil
	}

	all := Truthy(getenv("SHOPLAB_FIX_ALL"))
	flag := func(key string) bool { return all || Truthy(getenv(key)) }

	cfg := Config{
		Addr:        str("SHOPLAB_ADDR", DefaultAddr),
		DatabaseURL: str("DATABASE_URL", DefaultDatabaseURL),
		RedisURL:    str("REDIS_URL", DefaultRedisURL),
		Admin:       Truthy(getenv("SHOPLAB_ADMIN")),
		Fixes: Fixes{
			N1:    flag("SHOPLAB_FIX_N1"),
			Index: flag("SHOPLAB_FIX_INDEX"),
			Pool:  flag("SHOPLAB_FIX_POOL"),
			Cache: flag("SHOPLAB_FIX_CACHE"),
			Race:  flag("SHOPLAB_FIX_RACE"),
			Leak:  flag("SHOPLAB_FIX_LEAK"),
		},
	}

	var err error
	if cfg.CacheTTL, err = dur("SHOPLAB_CACHE_TTL", DefaultCacheTTL); err != nil {
		return Config{}, err
	}
	if cfg.SessionTTL, err = dur("SHOPLAB_SESSION_TTL", DefaultSessionTTL); err != nil {
		return Config{}, err
	}

	switch strings.ToLower(str("SHOPLAB_LOG_LEVEL", "info")) {
	case "debug":
		cfg.LogLevel = slog.LevelDebug
	case "info":
		cfg.LogLevel = slog.LevelInfo
	case "warn", "warning":
		cfg.LogLevel = slog.LevelWarn
	case "error":
		cfg.LogLevel = slog.LevelError
	default:
		return Config{}, fmt.Errorf("config: SHOPLAB_LOG_LEVEL must be debug|info|warn|error, got %q", getenv("SHOPLAB_LOG_LEVEL"))
	}
	return cfg, nil
}
