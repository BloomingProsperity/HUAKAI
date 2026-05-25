package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

var (
	ErrInvalidObsDLQBool     = errors.New("config: invalid OBS DLQ enabled flag")
	ErrInvalidObsDLQDuration = errors.New("config: invalid OBS DLQ duration seconds")
	ErrInvalidObsDLQWorkers  = errors.New("config: invalid OBS DLQ worker count")
	ErrInvalidObsDLQAttempts = errors.New("config: invalid OBS DLQ max attempts")
)

type ObsDLQConfig struct {
	Enabled       bool
	ReplicaDSN    string
	BaseBackoff   time.Duration
	CapBackoff    time.Duration
	MaxAttempts   int
	DLQAfter      time.Duration
	LeaseTTL      time.Duration
	HighWorkers   int
	MediumWorkers int
	LowWorkers    int
}

func LoadObsDLQ() (*ObsDLQConfig, error) {
	cfg := &ObsDLQConfig{
		Enabled:       true,
		ReplicaDSN:    strings.TrimSpace(os.Getenv("HUAKAI_OBS_REPLICA_DSN")),
		BaseBackoff:   time.Second,
		CapBackoff:    5 * time.Minute,
		MaxAttempts:   10,
		DLQAfter:      15 * time.Minute,
		LeaseTTL:      30 * time.Second,
		HighWorkers:   2,
		MediumWorkers: 1,
		LowWorkers:    1,
	}
	if raw := strings.TrimSpace(os.Getenv("HUAKAI_OBS_DLQ_ENABLED")); raw != "" {
		enabled, err := parseObsDLQBool(raw)
		if err != nil {
			return nil, err
		}
		cfg.Enabled = enabled
	}
	var err error
	if cfg.BaseBackoff, err = envDurationSeconds("HUAKAI_OBS_DLQ_BASE_BACKOFF_SECONDS", cfg.BaseBackoff); err != nil {
		return nil, err
	}
	if cfg.CapBackoff, err = envDurationSeconds("HUAKAI_OBS_DLQ_CAP_BACKOFF_SECONDS", cfg.CapBackoff); err != nil {
		return nil, err
	}
	if cfg.DLQAfter, err = envDurationSeconds("HUAKAI_OBS_DLQ_AFTER_SECONDS", cfg.DLQAfter); err != nil {
		return nil, err
	}
	if cfg.LeaseTTL, err = envDurationSeconds("HUAKAI_OBS_DLQ_LEASE_SECONDS", cfg.LeaseTTL); err != nil {
		return nil, err
	}
	if cfg.MaxAttempts, err = envPositiveInt("HUAKAI_OBS_DLQ_MAX_ATTEMPTS", cfg.MaxAttempts, ErrInvalidObsDLQAttempts); err != nil {
		return nil, err
	}
	if cfg.HighWorkers, err = envPositiveInt("HUAKAI_OBS_DLQ_HIGH_WORKERS", cfg.HighWorkers, ErrInvalidObsDLQWorkers); err != nil {
		return nil, err
	}
	if cfg.MediumWorkers, err = envPositiveInt("HUAKAI_OBS_DLQ_MED_WORKERS", cfg.MediumWorkers, ErrInvalidObsDLQWorkers); err != nil {
		return nil, err
	}
	if cfg.LowWorkers, err = envPositiveInt("HUAKAI_OBS_DLQ_LOW_WORKERS", cfg.LowWorkers, ErrInvalidObsDLQWorkers); err != nil {
		return nil, err
	}
	return cfg, nil
}

func parseObsDLQBool(raw string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "true", "t", "yes", "y", "on":
		return true, nil
	case "0", "false", "f", "no", "n", "off":
		return false, nil
	default:
		return false, fmt.Errorf("%w: HUAKAI_OBS_DLQ_ENABLED=%q", ErrInvalidObsDLQBool, raw)
	}
}

func envDurationSeconds(name string, fallback time.Duration) (time.Duration, error) {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback, nil
	}
	seconds, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || seconds <= 0 {
		return 0, fmt.Errorf("%w: %s=%q", ErrInvalidObsDLQDuration, name, raw)
	}
	return time.Duration(seconds) * time.Second, nil
}

func envPositiveInt(name string, fallback int, sentinel error) (int, error) {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("%w: %s=%q", sentinel, name, raw)
	}
	return n, nil
}
