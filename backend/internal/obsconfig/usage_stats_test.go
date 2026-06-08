package obsconfig

import (
	"context"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/platformsettings"
)

func TestUsageStatsEnabledDefaultsTrue(t *testing.T) {
	provider := NewUsageStatsProvider(fakeSettingsStore{})

	enabled, err := provider.UsageStatsEnabled(context.Background(), 42)
	if err != nil {
		t.Fatalf("UsageStatsEnabled() error = %v", err)
	}
	if !enabled {
		t.Fatalf("UsageStatsEnabled() = false, want default true")
	}
}

func TestUsageStatsEnabledFalseFromTenantScope(t *testing.T) {
	store := fakeSettingsStore{
		rows: map[string]platformsettings.StoredSetting{
			TenantScope(42) + "/" + UsageStatsEnabledKey: {Value: "false"},
		},
	}
	provider := NewUsageStatsProvider(store)

	enabled, err := provider.UsageStatsEnabled(context.Background(), 42)
	if err != nil {
		t.Fatalf("UsageStatsEnabled() error = %v", err)
	}
	if enabled {
		t.Fatalf("UsageStatsEnabled() = true, want tenant override false")
	}
}

type fakeSettingsStore struct {
	rows map[string]platformsettings.StoredSetting
	err  error
}

func (s fakeSettingsStore) Get(_ context.Context, scope string, key string) (platformsettings.StoredSetting, bool, error) {
	if s.err != nil {
		return platformsettings.StoredSetting{}, false, s.err
	}
	row, ok := s.rows[scope+"/"+key]
	return row, ok, nil
}
