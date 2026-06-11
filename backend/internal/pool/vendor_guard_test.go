package pool_test

import (
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/pool"
	"github.com/BloomingProsperity/HUAKAI/internal/provider/registrydefault"
)

// TestVendorCoversEveryRegisteredProtocolFamily 注册表驱动守卫(族集对称第 9
// 站,vendor metric/计价切片):registrydefault 注册的每个 protocol family 必须
// 在 pool.VendorFromProtocolFamily 有非空映射,且除 4-vendor 历史锁定标签外
// vendor 必须等于注册 adapter 的 Platform()——与选号后 accInfo.Platform 同值,
// 计价 providers.<vendor> 节点与 metric 切片才不分叉。
// 外部测试包(pool_test):registrydefault 依赖树含 internal/pool,内部测试包
// 会成环。
// MUTATION: 新增族注册后漏配 vendor → 非空断言红;vendor 拼错(≠ 注册
// platform 且非锁定标签)→ 等值断言红。
func TestVendorCoversEveryRegisteredProtocolFamily(t *testing.T) {
	// 打开全部 env-gated 占位 adapter,守卫覆盖完整注册面。
	for _, env := range []string{
		"HUAKAI_ENABLE_CURSOR_SESSION_ADAPTER",
		"HUAKAI_ENABLE_COPILOT_SESSION_ADAPTER",
		"HUAKAI_ENABLE_GEMINI_CODE_ASSIST_ADAPTER",
		"HUAKAI_ENABLE_GEMINI_ADVANCED_SESSION_ADAPTER",
		"HUAKAI_ENABLE_ANTIGRAVITY_SESSION_ADAPTER",
		"HUAKAI_ENABLE_KIRO_SESSION_ADAPTER",
		"HUAKAI_ENABLE_WINDSURF_SESSION_ADAPTER",
	} {
		t.Setenv(env, "true")
	}
	reg := registrydefault.Build()

	// 4-vendor 真实账号集合的历史标签(锁定,允许 vendor ≠ platform)。
	locked := map[string]string{
		"anthropic_messages":      "anthropic",
		"openai_chat":             "openai",
		"openai_responses":        "openai",
		"openai_codex":            "codex",
		"gemini_messages":         "gemini",
		"gemini_advanced_session": "gemini",
	}

	families := reg.RegisteredProtocolFamilies()
	if len(families) < 30 {
		t.Fatalf("registered families=%d want >=30(枚举面异常缩水)", len(families))
	}
	for _, fam := range families {
		vendor := pool.VendorFromProtocolFamily(fam)
		if vendor == "" {
			t.Errorf("family %q 缺 vendor 映射(metric 切片缺片 + 选号前计价节点不可达)", fam)
			continue
		}
		if want, ok := locked[fam]; ok {
			if vendor != want {
				t.Errorf("family %q vendor=%q want 锁定标签 %q", fam, vendor, want)
			}
			continue
		}
		adapter, err := reg.For(fam)
		if err != nil {
			t.Fatalf("registry.For(%q): %v", fam, err)
		}
		if got := adapter.Platform(); vendor != got {
			t.Errorf("family %q vendor=%q != 注册 platform %q(计价/metric 切片将分叉)", fam, vendor, got)
		}
	}
}
