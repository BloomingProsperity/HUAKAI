package rerankhttp

import (
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/registry"
	"github.com/BloomingProsperity/HUAKAI/internal/router"
)

func TestRouterResolvedModelPassesBindingFallbackMetadata(t *testing.T) {
	maxParallel := int32(7)
	got := routerResolvedModel(registry.Resolved{
		DefaultProviderModelID: "default-model",
		BindingMetadata: []registry.BindingMetadata{{
			PoolGroupID: 41, BindingID: 401, Priority: 11, Weight: 13,
			SelectionMode: "priority_weighted", FallbackClass: "quota",
			MaxParallelRequests: &maxParallel,
		}},
	})
	want := router.PoolCandidateMeta{
		PoolGroupID: 41, ProviderModelID: "default-model", BindingID: 401,
		MaxParallelRequests: 7, Priority: 11, Weight: 13,
		SelectionMode: "priority_weighted", FallbackClass: "quota",
	}
	if len(got.PoolMetadata) != 1 || got.PoolMetadata[0] != want {
		t.Fatalf("PoolMetadata=%+v，期望 [%+v]", got.PoolMetadata, want)
	}
}

func TestRequireRerankCapabilityCoversPrimaryAndFallbackAttempts(t *testing.T) {
	plan := router.RoutePlan{
		Attempts: []router.AttemptPlan{{RequiredCapabilities: []string{"json"}}},
		FallbackPhases: []router.FallbackPhasePlan{{
			Attempts: []router.AttemptPlan{{RequiredCapabilities: []string{rerankCapability}}},
		}},
	}

	requireRerankCapability(&plan)

	if got := plan.Attempts[0].RequiredCapabilities; len(got) != 2 || got[0] != "json" || got[1] != rerankCapability {
		t.Fatalf("主路能力=%v，期望保留 json 并追加 %s", got, rerankCapability)
	}
	if got := plan.FallbackPhases[0].Attempts[0].RequiredCapabilities; len(got) != 1 || got[0] != rerankCapability {
		t.Fatalf("降级路能力=%v，期望去重后的 [%s]", got, rerankCapability)
	}
}

func TestRerankModelCapabilityRejectsExplicitChatOnlyModel(t *testing.T) {
	if !hasRerankModelCapability(nil) || !hasRerankModelCapability([]string{rerankCapability}) {
		t.Fatal("未探测模型和明确 rerank 模型应保持可用")
	}
	if hasRerankModelCapability([]string{"chat", "tools"}) {
		t.Fatal("明确只有聊天能力的模型不得进入 rerank 链路")
	}
}
