package codexclientaccess

import "testing"

func TestParseEngineVersion(t *testing.T) {
	tests := []struct {
		name string
		ua   string
		want string
		ok   bool
	}{
		{name: "cli 正常版本", ua: "codex_cli_rs/0.141.0 (Ubuntu 22.4.0; x86_64) xterm", want: "0.141.0", ok: true},
		{name: "预发布后缀只取三段数字", ua: "codex_cli_rs/0.143.0-alpha.2 (x)", want: "0.143.0", ok: true},
		{name: "非数字版本不可检测", ua: "codex_cli_rs/abc (x)", want: "", ok: false},
		{name: "无斜杠不可检测", ua: "codex_cli_rs 0.141.0 (x)", want: "", ok: false},
		{name: "两段版本不可检测", ua: "curl/8.0", want: "", ok: false},
		{name: "vscode 三段版本可解析", ua: "vscode/1.99.0 (darwin; arm64)", want: "1.99.0", ok: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := ParseEngineVersion(tt.ua)
			if got != tt.want || ok != tt.ok {
				t.Fatalf("ParseEngineVersion() = (%q, %v), want (%q, %v)", got, ok, tt.want, tt.ok)
			}
		})
	}
}

func TestCompareVersions(t *testing.T) {
	tests := []struct {
		name string
		a    string
		b    string
		want int
	}{
		{name: "完全相等", a: "1.2.3", b: "1.2.3", want: 0},
		{name: "缺段补零", a: "0.141.0", b: "0.141", want: 0},
		{name: "数字分段比较", a: "0.9.9", b: "0.10.0", want: -1},
		{name: "v 前缀忽略", a: "v1.2.3", b: "1.2.3", want: 0},
		{name: "预发布后缀截断", a: "1.2.3-alpha.2", b: "1.2.3", want: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := CompareVersions(tt.a, tt.b); got != tt.want {
				t.Fatalf("CompareVersions(%q, %q) = %d, want %d", tt.a, tt.b, got, tt.want)
			}
		})
	}
}
