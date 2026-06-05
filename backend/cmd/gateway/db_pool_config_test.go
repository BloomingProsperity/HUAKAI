package main

import (
	"testing"
	"time"
)

func TestDBPoolConfigMapsOperatorOverrides(t *testing.T) {
	cfg := &Config{
		DatabaseURL:       "postgres://huakai:huakai@localhost:5432/huakai?sslmode=disable",
		DBMaxConns:        64,
		DBMinConns:        8,
		DBMaxConnLifetime: 45 * time.Minute,
		DBMaxConnIdleTime: 2 * time.Minute,
	}
	// Guards the wiring: operator pool overrides must reach db.PoolConfig.
	// Mutation (revert dbPoolConfig to only set DSN) makes these fail.
	got := dbPoolConfig(cfg)
	if got.DSN != cfg.DatabaseURL {
		t.Fatalf("DSN = %q, want %q", got.DSN, cfg.DatabaseURL)
	}
	if got.MaxConns != 64 {
		t.Fatalf("MaxConns = %d, want 64", got.MaxConns)
	}
	if got.MinConns != 8 {
		t.Fatalf("MinConns = %d, want 8", got.MinConns)
	}
	if got.MaxConnLifetime != 45*time.Minute {
		t.Fatalf("MaxConnLifetime = %s, want 45m", got.MaxConnLifetime)
	}
	if got.MaxConnIdleTime != 2*time.Minute {
		t.Fatalf("MaxConnIdleTime = %s, want 2m", got.MaxConnIdleTime)
	}
}

func TestDBPoolConfigZeroOverridesPreserveDefaults(t *testing.T) {
	cfg := &Config{DatabaseURL: "postgres://huakai:huakai@localhost:5432/huakai?sslmode=disable"}
	// Default-preserving: with no overrides only DSN is set; the db package then
	// applies its built-in defaults (16/2/30m/5m).
	got := dbPoolConfig(cfg)
	if got.MaxConns != 0 || got.MinConns != 0 || got.MaxConnLifetime != 0 || got.MaxConnIdleTime != 0 {
		t.Fatalf("expected zero overrides, got %d/%d/%s/%s", got.MaxConns, got.MinConns, got.MaxConnLifetime, got.MaxConnIdleTime)
	}
}
