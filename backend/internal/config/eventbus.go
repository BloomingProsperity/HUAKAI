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
	ErrInvalidEventBusStateCap = errors.New("config: invalid eventbus state cap")
	ErrUnsafeEventBusConfig    = errors.New("config: unsafe eventbus configuration")
)

const (
	EnvReleaseMode                     = "HUAKAI_RELEASE_MODE"
	EnvTrustLedgerAllowMissingMoneyRef = "HUAKAI_TRUST_LEDGER_ALLOW_MISSING_MONEY_REF"
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
	AllowMissingMoneyRef bool
	// MaxStates 限制 eventbus 每个 handler 状态账本的大小,使其在整个进程
	// 生命周期内不会无限增长。有限的默认值开箱即可堵住该泄漏;值 <= 0 时
	// 恢复历史上不设上限的行为,作为运维显式的应急出口。
	MaxStates int
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
		MaxStates:            4096,
	}
	if raw := strings.TrimSpace(os.Getenv("HUAKAI_EVENTBUS_ENABLED")); raw != "" {
		enabled, err := parseEventBusBool("HUAKAI_EVENTBUS_ENABLED", raw)
		if err != nil {
			return nil, err
		}
		cfg.Enabled = enabled
	}
	if raw := strings.TrimSpace(os.Getenv(EnvTrustLedgerAllowMissingMoneyRef)); raw != "" {
		allowMissing, err := parseEventBusBool(EnvTrustLedgerAllowMissingMoneyRef, raw)
		if err != nil {
			return nil, err
		}
		cfg.AllowMissingMoneyRef = allowMissing
	}
	if cfg.AllowMissingMoneyRef && eventBusProductionReleaseMode() {
		return nil, fmt.Errorf("%w: %s=true is forbidden when %s=production", ErrUnsafeEventBusConfig, EnvTrustLedgerAllowMissingMoneyRef, EnvReleaseMode)
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
	if cfg.MaxStates, err = envEventBusStateCap("HUAKAI_EVENTBUS_MAX_STATES", cfg.MaxStates); err != nil {
		return nil, err
	}
	return cfg, nil
}

func eventBusProductionReleaseMode() bool {
	return strings.EqualFold(strings.TrimSpace(os.Getenv(EnvReleaseMode)), "production")
}

func parseEventBusBool(name, raw string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "true", "t", "yes", "y", "on":
		return true, nil
	case "0", "false", "f", "no", "n", "off":
		return false, nil
	default:
		return false, fmt.Errorf("%w: %s=%q", ErrInvalidEventBusBool, name, raw)
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

// envEventBusStateCap 解析账本上限。与 worker/buffer 这类整数不同,它接受
// 非正值——非正值表示选择不设上限的应急出口;只有非数字输入才会被拒绝。
func envEventBusStateCap(name string, fallback int) (int, error) {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("%w: %s=%q", ErrInvalidEventBusStateCap, name, raw)
	}
	return n, nil
}
