package platformsettings

import "testing"

// TestRuntimeSettingDefaultsMatchPreWiringReality 守住四个接线项的零默认行为：
// 删除任一现实默认对齐会让设置中心再次展示并下发旧值，从而使未改设置的部署发生行为翻转。
func TestRuntimeSettingDefaultsMatchPreWiringReality(t *testing.T) {
	tests := []struct {
		key  SettingKey
		want string
	}{
		{key: KeyStreamTimeoutSeconds, want: "600"},
		{key: KeyCooldown429Seconds, want: "300"},
		{key: KeyCooldown529Seconds, want: "300"},
		{key: KeyAdminNotificationEmail, want: ""},
	}
	for _, tt := range tests {
		got, ok := DefaultValue(tt.key)
		if !ok || got != tt.want {
			t.Fatalf("默认值 %s=%q, want %q（已注册=%v）", tt.key, got, tt.want, ok)
		}
	}
}
