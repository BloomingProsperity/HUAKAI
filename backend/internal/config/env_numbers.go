package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

func envPositiveIntDefault(name string, fallback int) (int, error) {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value <= 0 {
		if err != nil {
			return 0, fmt.Errorf("%s must be positive integer, got %q: %w", name, raw, err)
		}
		return 0, fmt.Errorf("%s must be positive integer, got %q", name, raw)
	}
	return value, nil
}

func envNonNegativeDurationDefault(name string, fallback time.Duration) (time.Duration, error) {
	if strings.TrimSpace(os.Getenv(name)) == "" {
		return fallback, nil
	}
	return envOptionalDuration(name)
}

// envOptionalInt32 从环境变量解析非负 int32；空值表示沿用包默认值。
func envOptionalInt32(name string) (int32, error) {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return 0, nil
	}
	value, err := strconv.ParseInt(raw, 10, 32)
	if err != nil {
		return 0, fmt.Errorf("%s must be an integer, got %q: %w", name, raw, err)
	}
	if value < 0 {
		return 0, fmt.Errorf("%s must be non-negative, got %d", name, value)
	}
	return int32(value), nil
}
