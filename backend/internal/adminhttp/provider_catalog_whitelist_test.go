package adminhttp

import (
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/provider/registrydefault"
)

// TestEveryRegisteredFamilyIsCatalogProtocol 族集对称第 9 站:registrydefault
// 注册的每个出站 protocol family 都必须在 knownProviderCatalogProtocols 白名单
// 里——否则管理端渠道目录 CRUD 对该族返回 400 invalid_upstream_protocol,
// 运行时八站全通也无法在配置面申报该 provider(渠道建不出来=族整体不可运营)。
// 开全部 env-gate 以覆盖 env-gated 族(6 个 placeholder session + gemini_code_assist)。
//
// Mutation:从白名单删任一注册族 → 对应子断言红;往 registrydefault 加新族而
// 漏补白名单 → 本测试红(把配置面纳入新族落地的强制 checklist,防再漂移——
// kimi/qwen/.../ollama 12 兼容族 + dify/ollama_native/replicate/vertex×2 此前
// 正是这样漂掉的)。
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
		if _, ok := knownProviderCatalogProtocols[fam]; !ok {
			t.Errorf("registrydefault 注册族 %q 不在 provider-catalog 白名单(管理端渠道 CRUD 会 400 invalid_upstream_protocol)", fam)
		}
	}
}
