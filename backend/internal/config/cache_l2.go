package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	defaultL2CacheSizeBytes = int64(64 << 20)
	defaultL2CacheTTL       = 60 * time.Second
	defaultL2CacheScope     = "apikey"
)

var (
	ErrInvalidL2CacheBool  = errors.New("config: invalid L2 cache enabled flag")
	ErrInvalidL2CacheSize  = errors.New("config: invalid L2 cache size bytes")
	ErrInvalidL2CacheTTL   = errors.New("config: invalid L2 cache ttl seconds")
	ErrInvalidL2CacheScope = errors.New("config: invalid L2 cache scope")
)

type L2CacheConfig struct {
	Enabled   bool
	SizeBytes int64
	TTL       time.Duration
	// Scope 决定缓存键 principal 隔离粒度: tenant|apikey|user (默认 apikey, 安全)。
	Scope string
}

func LoadL2Cache() (*L2CacheConfig, error) {
	cfg := &L2CacheConfig{
		// Enabled defaults ON (F-CACHE-001 activated 2026-06-19, Owner-authorized): the
		// exact-key non-streaming response cache serves repeat requests at $0 settlement
		// (a hit commits the claim at zero cost with a full audit/usage row). Operators
		// disable per deployment with HUAKAI_CACHE_L2_ENABLED=0. In-memory + per-instance,
		// scope=apikey isolation; streaming requests are never cached.
		Enabled:   true,
		SizeBytes: defaultL2CacheSizeBytes,
		TTL:       defaultL2CacheTTL,
		Scope:     defaultL2CacheScope,
	}
	if raw := strings.TrimSpace(os.Getenv("HUAKAI_CACHE_L2_ENABLED")); raw != "" {
		enabled, err := parseL2Enabled(raw)
		if err != nil {
			return nil, err
		}
		cfg.Enabled = enabled
	}
	if raw := strings.TrimSpace(os.Getenv("HUAKAI_CACHE_L2_SIZE_BYTES")); raw != "" {
		size, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || size <= 0 {
			return nil, fmt.Errorf("%w: HUAKAI_CACHE_L2_SIZE_BYTES=%q", ErrInvalidL2CacheSize, raw)
		}
		cfg.SizeBytes = size
	}
	if raw := strings.TrimSpace(os.Getenv("HUAKAI_CACHE_L2_TTL_SECONDS")); raw != "" {
		seconds, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || seconds <= 0 {
			return nil, fmt.Errorf("%w: HUAKAI_CACHE_L2_TTL_SECONDS=%q", ErrInvalidL2CacheTTL, raw)
		}
		cfg.TTL = time.Duration(seconds) * time.Second
	}
	if raw := strings.TrimSpace(os.Getenv("HUAKAI_CACHE_L2_SCOPE")); raw != "" {
		switch strings.ToLower(raw) {
		case "tenant", "apikey", "user":
			cfg.Scope = strings.ToLower(raw)
		default:
			return nil, fmt.Errorf("%w: HUAKAI_CACHE_L2_SCOPE=%q", ErrInvalidL2CacheScope, raw)
		}
	}
	return cfg, nil
}

func parseL2Enabled(raw string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "true", "t", "yes", "y", "on":
		return true, nil
	case "0", "false", "f", "no", "n", "off":
		return false, nil
	default:
		return false, fmt.Errorf("%w: HUAKAI_CACHE_L2_ENABLED=%q", ErrInvalidL2CacheBool, raw)
	}
}
