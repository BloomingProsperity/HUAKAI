package obsconfig

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/BloomingProsperity/HUAKAI/internal/platformsettings"
)

const UsageStatsEnabledKey = "usage_stats_enabled"

type SettingReader interface {
	Get(ctx context.Context, scope, key string) (platformsettings.StoredSetting, bool, error)
}

type UsageStatsProvider struct {
	store SettingReader
}

func NewUsageStatsProvider(store SettingReader) *UsageStatsProvider {
	return &UsageStatsProvider{store: store}
}

func TenantScope(tenantID int64) string {
	return fmt.Sprintf("tenant:%d", tenantID)
}

func (p *UsageStatsProvider) UsageStatsEnabled(ctx context.Context, tenantID int64) (bool, error) {
	if p == nil || p.store == nil || tenantID <= 0 {
		return true, nil
	}
	row, ok, err := p.store.Get(ctx, TenantScope(tenantID), UsageStatsEnabledKey)
	if err != nil || !ok {
		return true, err
	}
	raw := strings.TrimSpace(row.Value)
	if raw == "" {
		return true, nil
	}
	enabled, err := strconv.ParseBool(raw)
	if err != nil {
		return true, fmt.Errorf("usage stats enabled setting: parse %q: %w", raw, err)
	}
	return enabled, nil
}
