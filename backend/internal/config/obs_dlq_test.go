package config

import (
	"errors"
	"testing"
)

func TestLoadObsDLQRejectsDisablingCoreRecoveryWorker(t *testing.T) {
	t.Setenv("HUAKAI_OBS_DLQ_ENABLED", "false")

	if _, err := LoadObsDLQ(); !errors.Is(err, ErrObsDLQRequired) {
		t.Fatalf("关闭核心恢复 worker err=%v，期望 ErrObsDLQRequired", err)
	}
}

func TestLoadObsDLQAcceptsEnabledOrUnsetLegacySetting(t *testing.T) {
	for _, value := range []string{"", "true", "1", "on"} {
		t.Run("value="+value, func(t *testing.T) {
			t.Setenv("HUAKAI_OBS_DLQ_ENABLED", value)
			if _, err := LoadObsDLQ(); err != nil {
				t.Fatalf("LoadObsDLQ(%q): %v", value, err)
			}
		})
	}
}
