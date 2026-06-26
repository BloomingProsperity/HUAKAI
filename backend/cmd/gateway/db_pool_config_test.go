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
	// 守护接线：运维设置的连接池覆盖值必须传达到 db.PoolConfig。
	// 变异（把 dbPoolConfig 退回成只设置 DSN）会让这些断言失败。
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
	// 保留默认值：没有任何覆盖时只设置 DSN；随后 db 包会
	// 应用其内置默认值（16/2/30m/5m）。
	got := dbPoolConfig(cfg)
	if got.MaxConns != 0 || got.MinConns != 0 || got.MaxConnLifetime != 0 || got.MaxConnIdleTime != 0 {
		t.Fatalf("expected zero overrides, got %d/%d/%s/%s", got.MaxConns, got.MinConns, got.MaxConnLifetime, got.MaxConnIdleTime)
	}
}
