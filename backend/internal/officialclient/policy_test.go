package officialclient

import (
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/clientid"
)

// TestRequiredIdentity_覆盖 验证 vendor→官方客户端映射(含大小写不敏感、未覆盖 vendor)。
func TestRequiredIdentity_覆盖(t *testing.T) {
	if id, ok := RequiredIdentity("anthropic"); !ok || id != clientid.IdentityClaudeCode {
		t.Fatalf("anthropic 应要求 Claude Code,得 id=%q ok=%v", id, ok)
	}
	if id, ok := RequiredIdentity("  Claude  "); !ok || id != clientid.IdentityClaudeCode {
		t.Fatalf("大小写/空白不敏感 Claude 应要求 Claude Code,得 id=%q ok=%v", id, ok)
	}
	if id, ok := RequiredIdentity("openai"); !ok || id != clientid.IdentityCodexCLI {
		t.Fatalf("openai 应要求 Codex CLI,得 id=%q ok=%v", id, ok)
	}
	if _, ok := RequiredIdentity("some_apikey_vendor"); ok {
		t.Fatalf("未覆盖 vendor 应 ok=false")
	}
}

// TestIsReverseAccountType 验证真实 AuthMode 值分类:OAuth/session 类=反转号,
// 官方 API key / 云凭据类 + 空/未知=否(大小写空白不敏感)。
func TestIsReverseAccountType(t *testing.T) {
	for _, at := range []string{"claude_ai_oauth", "claude_code", "chatgpt_oauth", "codex_cli_oauth", "code_assist", "google_one", "CLAUDE_AI_OAUTH", " codex_cli_oauth "} {
		if !IsReverseAccountType(at) {
			t.Fatalf("%q 应为反转号", at)
		}
	}
	for _, at := range []string{"api_key", "aistudio_api_key", "bedrock", "vertex_anthropic", "azure", "", "something_new"} {
		if IsReverseAccountType(at) {
			t.Fatalf("%q 不应为反转号", at)
		}
	}
}

// TestGateDecision 验证:官方 key 号恒不拒;反转号 + 对应官方客户端放行;反转号 + 非官方拒;
// 反转号但 vendor 无官方客户端映射→不拒(用真实 AuthMode 值)。
//
// 变异证伪:GateDecision 去掉 IsReverseAccountType 短路 → api_key+非官方被拒 → 红。
func TestGateDecision(t *testing.T) {
	// 官方 API key 号:任意客户端都不拒。
	for _, id := range []clientid.Identity{clientid.IdentityCurlScript, clientid.IdentityUnknown, clientid.IdentityClaudeCode} {
		if reject, _ := GateDecision("api_key", "anthropic", id); reject {
			t.Fatalf("api_key 号不应拒任何客户端(%q)", id)
		}
	}
	// 反转号 + 对应官方客户端:放行。
	if reject, _ := GateDecision("claude_ai_oauth", "anthropic", clientid.IdentityClaudeCode); reject {
		t.Fatalf("反转号 anthropic + Claude Code 应放行")
	}
	if reject, _ := GateDecision("codex_cli_oauth", "openai", clientid.IdentityCodexCLI); reject {
		t.Fatalf("反转号 openai + Codex CLI 应放行")
	}
	// 反转号 + 非官方客户端:拒。
	if reject, reason := GateDecision("claude_ai_oauth", "anthropic", clientid.IdentityCurlScript); !reject || reason != ReasonNonOfficialReject {
		t.Fatalf("反转号 anthropic + curl 应拒,得 reject=%v reason=%q", reject, reason)
	}
	if reject, _ := GateDecision("codex_cli_oauth", "openai", clientid.IdentityClaudeCode); !reject {
		t.Fatalf("反转号 openai + Claude Code(非 Codex)应拒")
	}
	// 反转号 + 无官方客户端映射的 vendor(gemini):不拒(不知其官方客户端,不 gate)。
	if reject, reason := GateDecision("code_assist", "gemini", clientid.IdentityCurlScript); reject {
		t.Fatalf("gemini 反转号无映射应不拒,得 reject=%v reason=%q", reject, reason)
	}
}
