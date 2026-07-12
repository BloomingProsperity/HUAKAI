package platformsettings

import "testing"

// TestCodexClientAccessKeysRegisteredWithOpenDefaults 验证片2f-1 的 7 个全局加固层键都进了允许
// 清单,且默认值 = 全开(名单/信号空、版本无界、app-server 放行、force 关)。变异:漏加任一键到
// defaultSettingValueMap → IsAllowedKey 或 DefaultValue 断言红。
func TestCodexClientAccessKeysRegisteredWithOpenDefaults(t *testing.T) {
	want := map[SettingKey]string{
		KeyCodexClientAccessBlacklist:                "[]",
		KeyCodexClientAccessWhitelist:                "[]",
		KeyCodexClientAccessMinVersion:               "",
		KeyCodexClientAccessMaxVersion:               "",
		KeyCodexClientAccessAllowAppServer:           "false",
		KeyCodexClientAccessEngineFingerprintSignals: "[]",
		KeyCodexClientAccessForceAllow:               "false",
	}
	for key, def := range want {
		if !IsAllowedKey(key) {
			t.Fatalf("%s 应在允许清单内", key)
		}
		got, ok := DefaultValue(key)
		if !ok || got != def {
			t.Fatalf("%s 默认值 =(%q,%v), want (%q,true)", key, got, ok, def)
		}
	}
}

// TestCodexClientAccessNormalizeValue 验证各键的写入校验:合法值过、非法值拒、空 JSON 归一为
// "[]"、空版本合法。变异:JSON 键校验放行非数组 / 版本空串拒 → 对应断言红。
func TestCodexClientAccessNormalizeValue(t *testing.T) {
	cases := []struct {
		name    string
		key     SettingKey
		value   string
		want    string
		wantErr bool
	}{
		{"黑名单空归一", KeyCodexClientAccessBlacklist, "", "[]", false},
		{"黑名单合法 originator-only", KeyCodexClientAccessBlacklist, `[{"originator":"bad"}]`, `[{"originator":"bad"}]`, false},
		{"黑名单全空条目拒", KeyCodexClientAccessBlacklist, `[{}]`, "", true},
		{"白名单合法双因子", KeyCodexClientAccessWhitelist, `[{"originator":"x","ua_contains":["Codex"]}]`, `[{"originator":"x","ua_contains":["Codex"]}]`, false},
		{"白名单缺 UA 因子拒", KeyCodexClientAccessWhitelist, `[{"originator":"x"}]`, "", true},
		{"白名单非数组拒", KeyCodexClientAccessWhitelist, `{"originator":"x"}`, "", true},
		{"信号空归一", KeyCodexClientAccessEngineFingerprintSignals, "", "[]", false},
		{"信号缺 name 拒", KeyCodexClientAccessEngineFingerprintSignals, `[{"header":"h","variants":["v"]}]`, "", true},
		{"版本空合法", KeyCodexClientAccessMinVersion, "", "", false},
		{"版本合法", KeyCodexClientAccessMaxVersion, "1.2.3", "1.2.3", false},
		{"版本乱串拒", KeyCodexClientAccessMinVersion, "latest", "", true},
		{"allow_app_server 合法 bool", KeyCodexClientAccessAllowAppServer, "false", "false", false},
		{"force_allow 非 bool 拒", KeyCodexClientAccessForceAllow, "maybe", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ValidateValue(tc.key, tc.value)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("期望错误，实际过: got=%q", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("不期望错误: %v", err)
			}
			if got != tc.want {
				t.Fatalf("归一值 = %q, want %q", got, tc.want)
			}
		})
	}
}
