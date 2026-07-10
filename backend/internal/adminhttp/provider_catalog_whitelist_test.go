package adminhttp

import (
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/provider/registrydefault"
)

// TestEveryRegisteredFamilyIsCatalogProtocol 族集对称第 9 站:registrydefault
// 注册的每个出站 protocol family 都必须被管理端渠道目录 CRUD 放行,
// 否则运行时全通也无法在配置面申报该 provider(渠道建不出来=族整体不可运营)。
// 开全部 env-gate 以覆盖 env-gated 族(6 个 placeholder session + gemini_code_assist)。
//
// Mutation:把 isKnownProviderCatalogProtocol 改回旧手写集合并漏掉任一注册族
// → 对应子断言红;误把平台名当协议族放行 → negative 子断言红。
func TestEveryRegisteredFamilyIsCatalogProtocol(t *testing.T) {
	t.Setenv("HUAKAI_ENABLE_PLACEHOLDER_SESSION_ADAPTERS", "")
	for _, env := range []string{
		"HUAKAI_ENABLE_CURSOR_SESSION_ADAPTER",
		"HUAKAI_ENABLE_COPILOT_SESSION_ADAPTER",
		"HUAKAI_ENABLE_GEMINI_ADVANCED_SESSION_ADAPTER",
		"HUAKAI_ENABLE_ANTIGRAVITY_SESSION_ADAPTER",
		"HUAKAI_ENABLE_KIRO_SESSION_ADAPTER",
		"HUAKAI_ENABLE_WINDSURF_SESSION_ADAPTER",
		"HUAKAI_ENABLE_GEMINI_CODE_ASSIST_ADAPTER",
	} {
		t.Setenv(env, "true")
	}
	r := registrydefault.Build()
	for _, fam := range r.RegisteredProtocolFamilies() {
		if !isKnownProviderCatalogProtocol(fam) {
			t.Errorf("registrydefault 注册族 %q 未被 provider-catalog 放行(管理端渠道 CRUD 会 400 invalid_upstream_protocol)", fam)
		}
	}
	if !isKnownProviderCatalogProtocol(registrydefault.ProtocolAnthropicClaudeSession) {
		t.Errorf("provider-catalog 必须放行已默认注册的 protocol family %q", registrydefault.ProtocolAnthropicClaudeSession)
	}
	for _, stale := range []string{"gemini", "bedrock", "antigravity"} {
		if isKnownProviderCatalogProtocol(stale) {
			t.Errorf("provider-catalog 不应放行未注册 protocol family %q", stale)
		}
	}
}
