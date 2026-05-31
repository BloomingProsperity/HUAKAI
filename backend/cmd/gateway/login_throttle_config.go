// HUAKAI · iKun

package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/loginthrottle"
)

// 登录限流(S2-048)的 env 配置键。未设则用 loginthrottle.DefaultConfig 的温和默认值;设了但非法
// (无法解析 / 非正)则 fail-fast 返回错误,绝不静默禁用限流(否则等于把登录 DoS 防护关掉)。
const (
	loginThrottleInFlightEnv    = "HUAKAI_LOGIN_THROTTLE_INFLIGHT_LIMIT"
	loginThrottleWindowEnv      = "HUAKAI_LOGIN_THROTTLE_WINDOW"
	loginThrottleWindowLimitEnv = "HUAKAI_LOGIN_THROTTLE_WINDOW_LIMIT"
	loginThrottleBanWindowEnv   = "HUAKAI_LOGIN_THROTTLE_BAN_WINDOW"
	loginThrottleBanAfterEnv    = "HUAKAI_LOGIN_THROTTLE_BAN_AFTER"
	loginThrottleBanDurEnv      = "HUAKAI_LOGIN_THROTTLE_BAN_DURATION"
	loginThrottleMaxKeysEnv     = "HUAKAI_LOGIN_THROTTLE_MAX_KEYS"
)

// loginThrottleConfigFromEnv 从 env 读出登录限流配置(零 env = 默认值)。任一键非法即返回错误,
// 让进程启动失败,而不是带着「被静默关掉的限流」上线。拆出来便于单测断言每个键的解析与校验。
func loginThrottleConfigFromEnv() (loginthrottle.Config, error) {
	cfg := loginthrottle.DefaultConfig()
	var err error
	if cfg.InFlightLimit, err = loginThrottlePositiveIntEnv(loginThrottleInFlightEnv, cfg.InFlightLimit); err != nil {
		return loginthrottle.Config{}, err
	}
	if cfg.Window, err = loginThrottlePositiveDurationEnv(loginThrottleWindowEnv, cfg.Window); err != nil {
		return loginthrottle.Config{}, err
	}
	if cfg.WindowLimit, err = loginThrottlePositiveIntEnv(loginThrottleWindowLimitEnv, cfg.WindowLimit); err != nil {
		return loginthrottle.Config{}, err
	}
	if cfg.BanWindow, err = loginThrottlePositiveDurationEnv(loginThrottleBanWindowEnv, cfg.BanWindow); err != nil {
		return loginthrottle.Config{}, err
	}
	if cfg.BanAfter, err = loginThrottlePositiveIntEnv(loginThrottleBanAfterEnv, cfg.BanAfter); err != nil {
		return loginthrottle.Config{}, err
	}
	if cfg.BanDuration, err = loginThrottlePositiveDurationEnv(loginThrottleBanDurEnv, cfg.BanDuration); err != nil {
		return loginthrottle.Config{}, err
	}
	if cfg.MaxKeys, err = loginThrottlePositiveIntEnv(loginThrottleMaxKeysEnv, cfg.MaxKeys); err != nil {
		return loginthrottle.Config{}, err
	}
	return cfg, nil
}

// loadLoginThrottleFromEnv 构造生产用的登录限流器。装配期调用;非法配置直接让启动失败。
func loadLoginThrottleFromEnv() (*loginthrottle.Limiter, error) {
	cfg, err := loginThrottleConfigFromEnv()
	if err != nil {
		return nil, err
	}
	return loginthrottle.New(cfg), nil
}

func loginThrottlePositiveIntEnv(key string, def int) (int, error) {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return def, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("%s: invalid integer %q: %w", key, raw, err)
	}
	if n <= 0 {
		return 0, fmt.Errorf("%s: must be > 0, got %d", key, n)
	}
	return n, nil
}

func loginThrottlePositiveDurationEnv(key string, def time.Duration) (time.Duration, error) {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return def, nil
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("%s: invalid duration %q: %w", key, raw, err)
	}
	if d <= 0 {
		return 0, fmt.Errorf("%s: must be > 0, got %s", key, d)
	}
	return d, nil
}
