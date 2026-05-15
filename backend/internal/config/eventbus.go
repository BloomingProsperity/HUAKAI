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
	ErrInvalidEventBusBool     = errors.New("config: invalid eventbus enabled flag")
	ErrInvalidEventBusDuration = errors.New("config: invalid eventbus duration seconds")
	ErrInvalidEventBusWorkers  = errors.New("config: invalid eventbus worker count")
	ErrInvalidEventBusBuffer   = errors.New("config: invalid eventbus buffer size")
)

type EventBusConfig struct {
	Enabled              bool
	HighWorkers          int
	MediumWorkers        int
	LowWorkers           int
	HighBuffer           int
	MediumBuffer         int
	LowBuffer            int
	HandlerTimeout       time.Duration
	ShutdownDrainTimeout time.Duration
}

func LoadEventBus() (*EventBusConfig, error) {
	cfg := &EventBusConfig{
		Enabled:              true,
		HighWorkers:          2,
		MediumWorkers:        1,
		LowWorkers:           1,
		HighBuffer:           128,
		MediumBuffer:         256,
		LowBuffer:            512,
		HandlerTimeout:       3 * time.Second,
		ShutdownDrainTimeout: 5 * time.Second,
	}
	if raw := strings.TrimSpace(os.Getenv("HUAKAI_EVENTBUS_ENABLED")); raw != "" {
		enabled, err := parseEventBusBool(raw)
		if err != nil {
			return nil, err
		}
		cfg.Enabled = enabled
	}
	var err error
	if cfg.HandlerTimeout, err = envEventBusDurationSeconds("HUAKAI_EVENTBUS_HANDLER_TIMEOUT_SECONDS", cfg.HandlerTimeout); err != nil {
		return nil, err
	}
	if cfg.ShutdownDrainTimeout, err = envEventBusDurationSeconds("HUAKAI_EVENTBUS_SHUTDOWN_DRAIN_SECONDS", cfg.ShutdownDrainTimeout); err != nil {
		return nil, err
	}
	if cfg.HighWorkers, err = envEventBusPositiveInt("HUAKAI_EVENTBUS_HIGH_WORKERS", cfg.HighWorkers, ErrInvalidEventBusWorkers); err != nil {
		return nil, err
	}
	if cfg.MediumWorkers, err = envEventBusPositiveInt("HUAKAI_EVENTBUS_MED_WORKERS", cfg.MediumWorkers, ErrInvalidEventBusWorkers); err != nil {
		return nil, err
	}
	if cfg.LowWorkers, err = envEventBusPositiveInt("HUAKAI_EVENTBUS_LOW_WORKERS", cfg.LowWorkers, ErrInvalidEventBusWorkers); err != nil {
		return nil, err
	}
	if cfg.HighBuffer, err = envEventBusPositiveInt("HUAKAI_EVENTBUS_HIGH_BUFFER", cfg.HighBuffer, ErrInvalidEventBusBuffer); err != nil {
		return nil, err
	}
	if cfg.MediumBuffer, err = envEventBusPositiveInt("HUAKAI_EVENTBUS_MED_BUFFER", cfg.MediumBuffer, ErrInvalidEventBusBuffer); err != nil {
		return nil, err
	}
	if cfg.LowBuffer, err = envEventBusPositiveInt("HUAKAI_EVENTBUS_LOW_BUFFER", cfg.LowBuffer, ErrInvalidEventBusBuffer); err != nil {
		return nil, err
	}
	return cfg, nil
}

func parseEventBusBool(raw string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "true", "t", "yes", "y", "on":
		return true, nil
	case "0", "false", "f", "no", "n", "off":
		return false, nil
	default:
		return false, fmt.Errorf("%w: HUAKAI_EVENTBUS_ENABLED=%q", ErrInvalidEventBusBool, raw)
	}
}

func envEventBusDurationSeconds(name string, fallback time.Duration) (time.Duration, error) {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback, nil
	}
	seconds, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || seconds <= 0 {
		return 0, fmt.Errorf("%w: %s=%q", ErrInvalidEventBusDuration, name, raw)
	}
	return time.Duration(seconds) * time.Second, nil
}

func envEventBusPositiveInt(name string, fallback int, sentinel error) (int, error) {
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
