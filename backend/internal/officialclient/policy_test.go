package officialclient

import (
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/clientid"
)

// TestAllowed_不设限恒放行 验证 officialOnly=false(如 apikey 号池)时任何客户端都放行。
//
// 变异证伪:把 !officialOnly 短路删掉 → apikey 池也走鉴真 → curl/unknown 被拒 → 变红。
func TestAllowed_不设限恒放行(t *testing.T) {
	for _, id := range []clientid.Identity{
		clientid.IdentityClaudeCode, clientid.IdentityCursor,
		clientid.IdentityCurlScript, clientid.IdentityUnknown,
	} {
		if ok, reason := Allowed(id, "anthropic", false); !ok || reason != ReasonNoRestriction {
			t.Fatalf("不设限应放行任何客户端(%q),得 ok=%v reason=%q", id, ok, reason)
		}
	}
}

// TestAllowed_官方门_只放官方拒其余 验证 officialOnly=true 时,仅对应官方客户端放行,
// 非官方 / unknown 一律拒。这是鉴真门的判别核心。
//
// 变异证伪:去掉 clientIdentity==required 判定(恒 return true, ok)→ 非官方也被放行 →
// "非官方拒"子断言变红。
func TestAllowed_官方门_只放官方拒其余(t *testing.T) {
	// 真 Claude Code → 放行。
	if ok, reason := Allowed(clientid.IdentityClaudeCode, "anthropic", true); !ok || reason != ReasonOfficialClientOK {
		t.Fatalf("真 Claude Code 应放行,得 ok=%v reason=%q", ok, reason)
	}
	// 非官方客户端 → 拒。
	for _, id := range []clientid.Identity{
		clientid.IdentityCursor, clientid.IdentityCurlScript,
		clientid.IdentityChatUI, clientid.IdentityCody,
	} {
		if ok, reason := Allowed(id, "anthropic", true); ok || reason != ReasonNonOfficialReject {
			t.Fatalf("非官方客户端(%q)应拒,得 ok=%v reason=%q", id, ok, reason)
		}
	}
	// unknown → 保守拒(与非官方分开原因,便于运维定位)。
	if ok, reason := Allowed(clientid.IdentityUnknown, "anthropic", true); ok || reason != ReasonUnknownClient {
		t.Fatalf("unknown 客户端应保守拒,得 ok=%v reason=%q", ok, reason)
	}
}

// TestAllowed_无官方客户端vendor开门_保守拒 验证:对没有官方客户端概念的 vendor 误开
// officialOnly=true(配置矛盾)时保守拒,不放行。
//
// 变异证伪:把 !has 分支改成 return true → 配置矛盾时误放行 → 变红。
func TestAllowed_无官方客户端vendor开门_保守拒(t *testing.T) {
	if ok, reason := Allowed(clientid.IdentityClaudeCode, "some_apikey_vendor", true); ok || reason != ReasonVendorNoOfficial {
		t.Fatalf("无官方客户端 vendor 误开门应保守拒,得 ok=%v reason=%q", ok, reason)
	}
}

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

// TestAllowed_Codex门 验证 openai 反转号开门时只放 Codex CLI,拒其余(含 Claude Code)。
//
// 变异证伪:RequiredIdentity 把 openai 也映射成 ClaudeCode → Codex CLI 被拒/Claude 被放 → 变红。
func TestAllowed_Codex门(t *testing.T) {
	if ok, reason := Allowed(clientid.IdentityCodexCLI, "openai", true); !ok || reason != ReasonOfficialClientOK {
		t.Fatalf("Codex CLI 访问 openai 反转号应放行,得 ok=%v reason=%q", ok, reason)
	}
	// Claude Code 不是 Codex → 访问 openai 反转号应拒。
	if ok, _ := Allowed(clientid.IdentityClaudeCode, "openai", true); ok {
		t.Fatalf("Claude Code 非 Codex,访问 openai 反转号应拒")
	}
}
