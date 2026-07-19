package servermonitor

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	DefaultInterval        = 30 * time.Second
	DefaultOfflineAfter    = 90 * time.Second
	DefaultRetention       = 30 * 24 * time.Hour
	DefaultCleanupInterval = time.Hour
	DefaultCleanupBatch    = 5000
)

type Config struct {
	Enabled         bool
	NodeID          string
	DisplayName     string
	Interval        time.Duration
	OfflineAfter    time.Duration
	Retention       time.Duration
	CleanupInterval time.Duration
	CleanupBatch    int
}

func LoadConfigFromEnv() (Config, error) {
	enabled, err := boolEnvDefault("HUAKAI_SERVER_MONITOR_ENABLED", true)
	if err != nil {
		return Config{}, err
	}
	interval, err := durationEnvDefault("HUAKAI_SERVER_MONITOR_INTERVAL", DefaultInterval)
	if err != nil {
		return Config{}, err
	}
	offlineAfter, err := durationEnvDefault("HUAKAI_SERVER_MONITOR_OFFLINE_AFTER", DefaultOfflineAfter)
	if err != nil {
		return Config{}, err
	}
	cfg := Config{
		Enabled:         enabled,
		NodeID:          strings.TrimSpace(os.Getenv("HUAKAI_SERVER_MONITOR_NODE_ID")),
		DisplayName:     strings.TrimSpace(os.Getenv("HUAKAI_SERVER_MONITOR_DISPLAY_NAME")),
		Interval:        interval,
		OfflineAfter:    offlineAfter,
		Retention:       DefaultRetention,
		CleanupInterval: DefaultCleanupInterval,
		CleanupBatch:    DefaultCleanupBatch,
	}
	if cfg.Interval < 5*time.Second || cfg.Interval > 10*time.Minute {
		return Config{}, fmt.Errorf("HUAKAI_SERVER_MONITOR_INTERVAL must be between 5s and 10m")
	}
	if cfg.OfflineAfter < 2*cfg.Interval || cfg.OfflineAfter > time.Hour {
		return Config{}, fmt.Errorf("HUAKAI_SERVER_MONITOR_OFFLINE_AFTER must be between two collection intervals and 1h")
	}
	return cfg, nil
}

func boolEnvDefault(key string, fallback bool) (bool, error) {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.ParseBool(raw)
	if err != nil {
		return false, fmt.Errorf("%s must be a boolean: %w", key, err)
	}
	return value, nil
}

func durationEnvDefault(key string, fallback time.Duration) (time.Duration, error) {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback, nil
	}
	value, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("%s must be a duration: %w", key, err)
	}
	return value, nil
}
