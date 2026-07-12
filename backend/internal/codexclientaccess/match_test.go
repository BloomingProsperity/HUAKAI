package codexclientaccess

import "testing"

func TestIsOfficialCodexUserAgent(t *testing.T) {
	tests := []struct {
		name string
		ua   string
		want bool
	}{
		{name: "严格前缀命中", ua: "Codex_CLI_RS/0.41.0", want: true},
		{name: "空格家族命中", ua: "Codex Desktop/1.0", want: true},
		{name: "尾部括号兜底命中", ua: "client/1.0 (linux; x64) vt100 (codex_cli_rs; 0.41.0)", want: true},
		{name: "裸 codex 不命中", ua: "codex", want: false},
		{name: "中段伪造不命中", ua: "curl/8 codex_cli_rs/0.41.0", want: false},
		{name: "空值不命中", ua: " ", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsOfficialCodexUserAgent(tt.ua); got != tt.want {
				t.Fatalf("IsOfficialCodexUserAgent(%q) = %v, want %v", tt.ua, got, tt.want)
			}
		})
	}
}

func TestIsOfficialCodexOriginator(t *testing.T) {
	tests := []struct {
		name       string
		originator string
		want       bool
	}{
		{name: "精确集合命中", originator: "codex_cli_rs", want: true},
		{name: "大小写归一命中", originator: "CODEX-TUI", want: true},
		{name: "空格家族命中", originator: "Codex Desktop", want: true},
		{name: "裸 codex 不命中", originator: "codex", want: false},
		{name: "前缀伪造不命中", originator: "evil-codex_cli_rs", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsOfficialCodexOriginator(tt.originator); got != tt.want {
				t.Fatalf("IsOfficialCodexOriginator(%q) = %v, want %v", tt.originator, got, tt.want)
			}
		})
	}
}

func TestMatchClientEntryRequiresOriginatorAndAllUAMarkers(t *testing.T) {
	entries := []AllowedClientEntry{{
		Originator: "codex_app",
		UAContains: []string{"desktop", "stable"},
	}}

	got, ok := matchClientEntry("Codex Desktop Stable", "CODEX_APP", entries)
	if !ok {
		t.Fatal("双因子齐全时应命中白名单")
	}
	if got.Originator != "codex_app" {
		t.Fatalf("返回条目 Originator = %q, want %q", got.Originator, "codex_app")
	}

	if _, ok := matchClientEntry("Codex Desktop", "codex_app", entries); ok {
		t.Fatal("缺少 UA 因子时不应命中白名单")
	}
	if _, ok := matchClientEntry("Codex Desktop Stable", "other", entries); ok {
		t.Fatal("originator 不匹配时不应命中白名单")
	}

	blankMarker := []AllowedClientEntry{{
		Originator: "codex_app",
		UAContains: []string{"desktop", " "},
	}}
	if _, ok := matchClientEntry("Codex Desktop Stable", "codex_app", blankMarker); ok {
		t.Fatal("白名单空白 marker 应安全失败")
	}

	originatorOnly := []AllowedClientEntry{{Originator: "codex_app"}}
	if _, ok := matchClientEntry("Codex Desktop Stable", "codex_app", originatorOnly); ok {
		t.Fatal("白名单缺少 UA 因子时不应命中")
	}
}

func TestMatchDenyEntriesUsesWideOr(t *testing.T) {
	originatorOnly := []AllowedClientEntry{{Originator: "blocked_origin"}}
	if !matchDenyEntries("curl/8", "blocked_origin", originatorOnly) {
		t.Fatal("黑名单 originator-only 应命中")
	}

	uaOnly := []AllowedClientEntry{{UAContains: []string{"blocked-marker"}}}
	if !matchDenyEntries("client blocked-marker", "", uaOnly) {
		t.Fatal("黑名单 ua-only 应命中")
	}

	emptyEntry := []AllowedClientEntry{{}}
	if matchDenyEntries("client blocked-marker", "blocked_origin", emptyEntry) {
		t.Fatal("黑名单全空条目不应命中")
	}
}
