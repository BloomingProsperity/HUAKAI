package config

import (
	"errors"
	"testing"
	"time"
)

// TestLoadL2CacheDefaultsOn guards the F-CACHE-001 activation: with no env override the cache
// is ENABLED by default (size/ttl/scope at their defaults).
// MUTATION: revert the default to Enabled:false -> this assertion goes RED.
func TestLoadL2CacheDefaultsOn(t *testing.T) {
	clearL2CacheEnv(t)
	cfg, err := LoadL2Cache()
	if err != nil {
		t.Fatalf("LoadL2Cache: %v", err)
	}
	if !cfg.Enabled {
		t.Fatal("L2 cache must default ON after F-CACHE-001 activation")
	}
	if cfg.SizeBytes != defaultL2CacheSizeBytes {
		t.Fatalf("SizeBytes=%d want %d", cfg.SizeBytes, defaultL2CacheSizeBytes)
	}
	if cfg.TTL != defaultL2CacheTTL {
		t.Fatalf("TTL=%s want %s", cfg.TTL, defaultL2CacheTTL)
	}
	if cfg.Scope != defaultL2CacheScope {
		t.Fatalf("Scope=%q want %q", cfg.Scope, defaultL2CacheScope)
	}
}

// TestLoadL2CacheEnvOverrideOff guards that operators can still DISABLE the now-default-on cache
// via HUAKAI_CACHE_L2_ENABLED=0 (the env override must beat the new default).
// MUTATION: if the env-override branch stops applying, Enabled stays true -> RED. Discriminating
// only because the default is now ON — a false expected value the override must flip back to false.
func TestLoadL2CacheEnvOverrideOff(t *testing.T) {
	clearL2CacheEnv(t)
	for _, off := range []string{"0", "false", "off", "no"} {
		t.Run(off, func(t *testing.T) {
			t.Setenv("HUAKAI_CACHE_L2_ENABLED", off)
			cfg, err := LoadL2Cache()
			if err != nil {
				t.Fatalf("LoadL2Cache: %v", err)
			}
			if cfg.Enabled {
				t.Fatalf("HUAKAI_CACHE_L2_ENABLED=%q must disable the default-on cache", off)
			}
		})
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
