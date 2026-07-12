package config

import (
	"errors"
	"testing"
	"time"
)

// TestLoadL2CacheDefaultsOn 守住 F-CACHE-001 的激活:在无 env 覆盖时,缓存默认开启
//(size/ttl/scope 均取各自默认值)。
// MUTATION:把默认值改回 Enabled:false → 本断言变红。
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

// TestLoadL2CacheEnvOverrideOff 守住运维仍可通过 HUAKAI_CACHE_L2_ENABLED=0 关闭这个现已默认
// 开启的缓存(env 覆盖必须压过新默认值)。
// MUTATION:若 env 覆盖分支不再生效, Enabled 会保持 true → 变红。之所以有区分力, 正因为现在默认
// 是开启的——这是一个 false 的期望值, 覆盖必须把它翻回 false。
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
