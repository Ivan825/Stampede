package config

import (
	"log/slog"
	"testing"
	"time"
)

func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestTruthy(t *testing.T) {
	for _, s := range []string{"1", "true", "TRUE", "yes", "Yes", " on "} {
		if !Truthy(s) {
			t.Errorf("Truthy(%q) = false, want true", s)
		}
	}
	for _, s := range []string{"", "0", "false", "no", "off", "nope"} {
		if Truthy(s) {
			t.Errorf("Truthy(%q) = true, want false", s)
		}
	}
}

func TestLoadDefaults(t *testing.T) {
	cfg, err := Load(env(nil))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Addr != ":8090" || cfg.DatabaseURL != DefaultDatabaseURL || cfg.RedisURL != DefaultRedisURL {
		t.Fatalf("unexpected defaults: %+v", cfg)
	}
	if cfg.Fixes != (Fixes{}) {
		t.Fatalf("all fixes should default to off, got %+v", cfg.Fixes)
	}
	if cfg.Admin {
		t.Fatal("admin should default to off")
	}
	if cfg.CacheTTL != 10*time.Second || cfg.SessionTTL != 15*time.Minute {
		t.Fatalf("unexpected durations: %v %v", cfg.CacheTTL, cfg.SessionTTL)
	}
	if cfg.LogLevel != slog.LevelInfo {
		t.Fatalf("log level = %v", cfg.LogLevel)
	}
}

func TestLoadIndividualFixes(t *testing.T) {
	cfg, err := Load(env(map[string]string{
		"SHOPLAB_FIX_N1":   "1",
		"SHOPLAB_FIX_RACE": "yes",
		"SHOPLAB_FIX_LEAK": "false",
		"SHOPLAB_ADMIN":    "true",
		"SHOPLAB_ADDR":     ":9999",
	}))
	if err != nil {
		t.Fatal(err)
	}
	want := Fixes{N1: true, Race: true}
	if cfg.Fixes != want {
		t.Fatalf("fixes = %+v, want %+v", cfg.Fixes, want)
	}
	if !cfg.Admin || cfg.Addr != ":9999" {
		t.Fatalf("cfg = %+v", cfg)
	}
}

func TestLoadFixAll(t *testing.T) {
	cfg, err := Load(env(map[string]string{"SHOPLAB_FIX_ALL": "1"}))
	if err != nil {
		t.Fatal(err)
	}
	want := Fixes{true, true, true, true, true, true}
	if cfg.Fixes != want {
		t.Fatalf("fixes = %+v, want all on", cfg.Fixes)
	}
	for name, on := range cfg.Fixes.Map() {
		if !on {
			t.Errorf("fix %s off", name)
		}
	}
}

func TestLoadRejectsBadValues(t *testing.T) {
	for _, m := range []map[string]string{
		{"SHOPLAB_CACHE_TTL": "soon"},
		{"SHOPLAB_SESSION_TTL": "-1s"},
		{"SHOPLAB_LOG_LEVEL": "chatty"},
	} {
		if _, err := Load(env(m)); err == nil {
			t.Errorf("Load(%v) succeeded, want error", m)
		}
	}
}
