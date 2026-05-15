package config

import (
	"errors"
	"testing"
	"time"
)

func TestLoadL2CacheDefaultsOff(t *testing.T) {
	clearL2CacheEnv(t)
	cfg, err := LoadL2Cache()
	if err != nil {
		t.Fatalf("LoadL2Cache: %v", err)
	}
	if cfg.Enabled {
		t.Fatal("L2 cache must default off")
	}
	if cfg.SizeBytes != defaultL2CacheSizeBytes {
		t.Fatalf("SizeBytes=%d want %d", cfg.SizeBytes, defaultL2CacheSizeBytes)
	}
	if cfg.TTL != defaultL2CacheTTL {
		t.Fatalf("TTL=%s want %s", cfg.TTL, defaultL2CacheTTL)
	}
}

func TestLoadL2CacheEnvEnabled(t *testing.T) {
	clearL2CacheEnv(t)
	t.Setenv("HUAKAI_CACHE_L2_ENABLED", "1")
	t.Setenv("HUAKAI_CACHE_L2_SIZE_BYTES", "4096")
	t.Setenv("HUAKAI_CACHE_L2_TTL_SECONDS", "30")
	cfg, err := LoadL2Cache()
	if err != nil {
		t.Fatalf("LoadL2Cache: %v", err)
	}
	if !cfg.Enabled || cfg.SizeBytes != 4096 || cfg.TTL != 30*time.Second {
		t.Fatalf("cfg=%+v want enabled/4096/30s", cfg)
	}
}

func TestLoadL2CacheInvalidEnvFailsFast(t *testing.T) {
	cases := []struct {
		name string
		key  string
		val  string
		want error
	}{
		{"enabled", "HUAKAI_CACHE_L2_ENABLED", "sometimes", ErrInvalidL2CacheBool},
		{"size", "HUAKAI_CACHE_L2_SIZE_BYTES", "0", ErrInvalidL2CacheSize},
		{"ttl", "HUAKAI_CACHE_L2_TTL_SECONDS", "-1", ErrInvalidL2CacheTTL},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			clearL2CacheEnv(t)
			t.Setenv(tc.key, tc.val)
			_, err := LoadL2Cache()
			if !errors.Is(err, tc.want) {
				t.Fatalf("err=%v want %v", err, tc.want)
			}
		})
	}
}

func clearL2CacheEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{
		"HUAKAI_CACHE_L2_ENABLED",
		"HUAKAI_CACHE_L2_SIZE_BYTES",
		"HUAKAI_CACHE_L2_TTL_SECONDS",
	} {
		t.Setenv(key, "")
	}
}
