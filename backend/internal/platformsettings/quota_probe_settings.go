package platformsettings

import (
	"fmt"
	"strconv"
)

const (
	KeyQuotaProbeEnabled         SettingKey = "quota_probe.enabled"
	KeyQuotaProbeIntervalMinutes SettingKey = "quota_probe.interval_minutes"

	MinQuotaProbeIntervalMinutes = 5
	MaxQuotaProbeIntervalMinutes = 1440
)

func validateQuotaProbeIntervalMinutes(key SettingKey, value string) (string, error) {
	minutes, err := strconv.Atoi(value)
	if err != nil {
		return "", fmt.Errorf("%w: %s 必须是整数分钟", ErrInvalidValue, key)
	}
	if minutes < MinQuotaProbeIntervalMinutes {
		minutes = MinQuotaProbeIntervalMinutes
	}
	if minutes > MaxQuotaProbeIntervalMinutes {
		minutes = MaxQuotaProbeIntervalMinutes
	}
	return strconv.Itoa(minutes), nil
}
