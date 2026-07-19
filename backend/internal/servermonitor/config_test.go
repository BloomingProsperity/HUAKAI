package servermonitor

import (
	"testing"
	"time"
)

func TestLoadConfigDefaultsAndValidation(t *testing.T) {
	for _, key := range []string{
		"HUAKAI_SERVER_MONITOR_ENABLED",
		"HUAKAI_SERVER_MONITOR_NODE_ID",
		"HUAKAI_SERVER_MONITOR_DISPLAY_NAME",
		"HUAKAI_SERVER_MONITOR_INTERVAL",
		"HUAKAI_SERVER_MONITOR_OFFLINE_AFTER",
	} {
		t.Setenv(key, "")
	}
	cfg, err := LoadConfigFromEnv()
	if err != nil {
		t.Fatalf("load defaults: %v", err)
	}
	if !cfg.Enabled || cfg.Interval != 30*time.Second || cfg.OfflineAfter != 90*time.Second || cfg.Retention != 30*24*time.Hour {
		t.Fatalf("defaults=%+v", cfg)
	}

	t.Setenv("HUAKAI_SERVER_MONITOR_INTERVAL", "1s")
	if _, err := LoadConfigFromEnv(); err == nil {
		t.Fatal("过密采集间隔必须拒绝")
	}
	t.Setenv("HUAKAI_SERVER_MONITOR_INTERVAL", "30s")
	t.Setenv("HUAKAI_SERVER_MONITOR_OFFLINE_AFTER", "30s")
	if _, err := LoadConfigFromEnv(); err == nil {
		t.Fatal("离线窗口小于两个采集周期必须拒绝")
	}
}
