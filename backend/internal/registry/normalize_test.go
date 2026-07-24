package registry

import "testing"

func TestAliasNormalize(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"empty", "", ""},
		{"whitespace_only", "   ", ""},
		// 测试输入覆盖不同供应商的真实模型 ID 形态：
		// claude-opus-4-7 / claude-haiku-4-5(Anthropic 2026-04-30T10:08Z)、
		// gpt-5.4-mini(OpenAI 2026-04-30T10:09Z)、
		// gemini-2.5-pro(Google 2026-04-30T10:08Z)。
		{"plain", "claude-opus-4-7", "claude-opus-4-7"},
		{"lowercases_uppercase", "GPT-5.4-Mini", "gpt-5.4-mini"},
		{"trims_outer_whitespace", "  claude-haiku-4-5\t", "claude-haiku-4-5"},
		{"collapses_mixed_case", "Claude-Opus-4-7", "claude-opus-4-7"},
		// NFC 归一化:预组合形式。fixture 中不生成分解序列,
		// 但仍验证该调用路径。
		{"ascii_unaffected_by_nfc", "gemini-2.5-pro", "gemini-2.5-pro"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := AliasNormalize(tc.in)
			if got != tc.want {
				t.Fatalf("AliasNormalize(%q) = %q; want %q", tc.in, got, tc.want)
			}
		})
	}
}
