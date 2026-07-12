package codexclientaccess

import "testing"

func TestParseAllowedClientEntries(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		wantLen int
		wantErr bool
	}{
		{name: "空串为空清单", raw: "", wantLen: 0},
		{name: "空数组为空清单", raw: "[]", wantLen: 0},
		{name: "合法数组", raw: `[{"originator":"codex-cli","ua_contains":["Codex","CLI"],"skip_engine_fingerprint":true}]`, wantLen: 1},
		{name: "非法 JSON 拒绝", raw: `[{"originator":`, wantErr: true},
		{name: "未知字段拒绝", raw: `[{"originator":"x","ua_contains":["y"],"unknown":true}]`, wantErr: true},
		{name: "非数组拒绝", raw: `{"originator":"x"}`, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseAllowedClientEntries(tt.raw)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("期望错误，实际为 nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("不期望错误: %v", err)
			}
			if len(got) != tt.wantLen {
				t.Fatalf("条目数量 = %d, want %d", len(got), tt.wantLen)
			}
		})
	}
}

func TestValidateWhitelistEntries(t *testing.T) {
	tests := []struct {
		name    string
		entries []AllowedClientEntry
		wantErr bool
	}{
		{name: "缺 originator 拒绝", entries: []AllowedClientEntry{{UAContains: []string{"Codex"}}}, wantErr: true},
		{name: "缺 UA 子串拒绝", entries: []AllowedClientEntry{{Originator: "codex-cli"}}, wantErr: true},
		{name: "空白 UA 子串拒绝", entries: []AllowedClientEntry{{Originator: "codex-cli", UAContains: []string{"Codex", " "}}}, wantErr: true},
		{name: "双因子合法", entries: []AllowedClientEntry{{Originator: "codex-cli", UAContains: []string{"Codex"}}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateWhitelistEntries(tt.entries)
			if tt.wantErr && err == nil {
				t.Fatalf("期望错误，实际为 nil")
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("不期望错误: %v", err)
			}
		})
	}
}

func TestValidateBlacklistEntries(t *testing.T) {
	tests := []struct {
		name    string
		entries []AllowedClientEntry
		wantErr bool
	}{
		{name: "全空条目拒绝", entries: []AllowedClientEntry{{}}, wantErr: true},
		{name: "空白 UA 条目拒绝", entries: []AllowedClientEntry{{UAContains: []string{" "}}}, wantErr: true},
		{name: "仅 originator 合法", entries: []AllowedClientEntry{{Originator: "blocked-client"}}},
		{name: "仅 UA 子串合法", entries: []AllowedClientEntry{{UAContains: []string{"BadUA"}}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateBlacklistEntries(tt.entries)
			if tt.wantErr && err == nil {
				t.Fatalf("期望错误，实际为 nil")
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("不期望错误: %v", err)
			}
		})
	}
}

func TestValidateVersionString(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		wantErr bool
	}{
		{name: "空串合法", value: ""},
		{name: "单段数字合法", value: "1"},
		{name: "两段数字合法", value: "0.141"},
		{name: "三段数字合法", value: "1.2.3"},
		{name: "四段拒绝", value: "1.2.3.4", wantErr: true},
		{name: "预发布拒绝", value: "1.2.3-beta.1", wantErr: true},
		{name: "构建号拒绝", value: "1.2.3+build.7", wantErr: true},
		{name: "乱串拒绝", value: "latest", wantErr: true},
		{name: "空段拒绝", value: "1..3", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateVersionString(tt.value)
			if tt.wantErr && err == nil {
				t.Fatalf("期望错误，实际为 nil")
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("不期望错误: %v", err)
			}
		})
	}
}

func TestParseEngineFingerprintSignals(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		wantLen int
		wantErr bool
	}{
		{name: "空串为空清单", raw: "", wantLen: 0},
		{name: "空数组为空清单", raw: "[]", wantLen: 0},
		{name: "合法 header 信号", raw: `[{"name":"engine-header","header":"x-engine","body_path":"","variants":["codex"],"required":true}]`, wantLen: 1},
		{name: "合法 body 信号", raw: `[{"name":"engine-body","header":"","body_path":"$.model","variants":["codex"],"required":false}]`, wantLen: 1},
		{name: "未知字段拒绝", raw: `[{"name":"x","header":"h","variants":["v"],"extra":1}]`, wantErr: true},
		{name: "缺 name 拒绝", raw: `[{"header":"h","variants":["v"]}]`, wantErr: true},
		{name: "缺信号来源拒绝", raw: `[{"name":"x","variants":["v"]}]`, wantErr: true},
		{name: "缺 variants 拒绝", raw: `[{"name":"x","header":"h"}]`, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseEngineFingerprintSignals(tt.raw)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("期望错误，实际为 nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("不期望错误: %v", err)
			}
			if len(got) != tt.wantLen {
				t.Fatalf("信号数量 = %d, want %d", len(got), tt.wantLen)
			}
		})
	}
}
