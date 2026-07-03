package clientid

import "testing"

// TestDetect_CodexCLI_UA 验证 Codex CLI 官方 UA(codex_cli_rs/... 及变体)被识别为
// IdentityCodexCLI。变异证伪:删 detectFromUserAgent 的 codex 分支 → 落到 unknown/script → 红。
func TestDetect_CodexCLI_UA(t *testing.T) {
	for _, ua := range []string{
		"codex_cli_rs/0.125.0 (Ubuntu 22.4.0; x86_64) xterm-256color",
		"codex-cli/1.2.3",
		"Codex/0.1 (macOS)",
	} {
		if id, conf := Detect(Signal{UserAgent: ua}); id != IdentityCodexCLI {
			t.Fatalf("UA %q 应识别为 Codex CLI,得 %q(conf=%.1f)", ua, id, conf)
		}
	}
}

// TestDetect_CodexCLI_XClient 验证 X-Client-Name 含 codex → IdentityCodexCLI(最强信号 1.0)。
func TestDetect_CodexCLI_XClient(t *testing.T) {
	if id, conf := Detect(Signal{XClient: map[string]string{"x-client-name": "codex"}}); id != IdentityCodexCLI || conf != 1.0 {
		t.Fatalf("X-Client-Name=codex 应识别 Codex CLI conf=1.0,得 %q conf=%.1f", id, conf)
	}
}

// TestDetect_CodexCLI_不误伤邻近身份 判别:codex 分支不能误伤 Claude Code / Cody
// (子串相近,cody vs codex)。
func TestDetect_CodexCLI_不误伤邻近身份(t *testing.T) {
	if id, _ := Detect(Signal{UserAgent: "claude-cli/2.1.78 (external, cli)"}); id != IdentityClaudeCode {
		t.Fatalf("Claude Code UA 应仍是 Claude Code,得 %q(codex 分支误伤?)", id)
	}
	if id, _ := Detect(Signal{UserAgent: "cody-cli/1.0"}); id != IdentityCody {
		t.Fatalf("Cody UA 应仍是 Cody,得 %q(codex 分支误伤?)", id)
	}
}
