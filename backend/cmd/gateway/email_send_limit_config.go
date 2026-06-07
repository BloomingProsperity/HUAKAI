// HUAKAI · iKun

package main

import (
	"github.com/BloomingProsperity/HUAKAI/internal/emailsendlimit"
)

// Email-send per-IP limit env keys. Unset values use emailsendlimit.DefaultConfig.
const (
	emailSendLimitWindowEnv  = "HUAKAI_EMAIL_SEND_LIMIT_WINDOW"
	emailSendLimitLimitEnv   = "HUAKAI_EMAIL_SEND_LIMIT_LIMIT"
	emailSendLimitMaxKeysEnv = "HUAKAI_EMAIL_SEND_LIMIT_MAX_KEYS"
)

func emailSendLimitConfigFromEnv() (emailsendlimit.Config, error) {
	cfg := emailsendlimit.DefaultConfig()
	var err error
	if cfg.Window, err = loginThrottlePositiveDurationEnv(emailSendLimitWindowEnv, cfg.Window); err != nil {
		return emailsendlimit.Config{}, err
	}
	if cfg.Limit, err = loginThrottlePositiveIntEnv(emailSendLimitLimitEnv, cfg.Limit); err != nil {
		return emailsendlimit.Config{}, err
	}
	if cfg.MaxKeys, err = loginThrottlePositiveIntEnv(emailSendLimitMaxKeysEnv, cfg.MaxKeys); err != nil {
		return emailsendlimit.Config{}, err
	}
	return cfg, nil
}

func loadEmailSendLimitFromEnv() (*emailsendlimit.Limiter, error) {
	cfg, err := emailSendLimitConfigFromEnv()
	if err != nil {
		return nil, err
	}
	return emailsendlimit.New(cfg), nil
}
