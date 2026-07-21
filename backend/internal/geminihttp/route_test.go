package geminihttp

import (
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/registry"
	"github.com/BloomingProsperity/HUAKAI/internal/router"
)

func TestRouterResolvedModelPassesBindingFallbackMetadata(t *testing.T) {
	maxParallel := int32(7)
	rpmLimit := int32(61)
	tpmLimit := int32(610)
	got := routerResolvedModel(registry.Resolved{
		DefaultProviderModelID: "default-model",
		BindingMetadata: []registry.BindingMetadata{{
			PoolGroupID: 41, BindingID: 401, Priority: 11, Weight: 13,
			SelectionMode: "priority_weighted", FallbackClass: "quota",
			RPMLimit: &rpmLimit, TPMLimit: &tpmLimit,
			MaxParallelRequests: &maxParallel,
		}},
	})
	want := router.PoolCandidateMeta{
		PoolGroupID: 41, ProviderModelID: "default-model", BindingID: 401,
		BindingRPMLimit: 61, BindingTPMLimit: 610,
		MaxParallelRequests: 7, Priority: 11, Weight: 13,
		SelectionMode: "priority_weighted", FallbackClass: "quota",
	}
	if len(got.PoolMetadata) != 1 || got.PoolMetadata[0] != want {
		t.Fatalf("PoolMetadata=%+v，期望 [%+v]", got.PoolMetadata, want)
	}
}

func TestRequireCountTokensCapabilityCoversPrimaryAndFallbackAttempts(t *testing.T) {
	plan := router.RoutePlan{
		Attempts: []router.AttemptPlan{{RequiredCapabilities: []string{"json"}}},
		FallbackPhases: []router.FallbackPhasePlan{{
			Attempts: []router.AttemptPlan{{RequiredCapabilities: []string{countTokensCapability}}},
		}},
	}

	requireCountTokensCapability(&plan)

	if got := plan.Attempts[0].RequiredCapabilities; len(got) != 2 || got[0] != "json" || got[1] != countTokensCapability {
		t.Fatalf("主路能力=%v，期望保留 json 并追加 %s", got, countTokensCapability)
	}
	if got := plan.FallbackPhases[0].Attempts[0].RequiredCapabilities; len(got) != 1 || got[0] != countTokensCapability {
		t.Fatalf("降级路能力=%v，期望去重后的 [%s]", got, countTokensCapability)
	}
}

func TestCountTokensModelCapabilityRejectsExplicitGenerateOnlyModel(t *testing.T) {
	if !hasCountTokensModelCapability(nil) || !hasCountTokensModelCapability([]string{countTokensCapability}) {
		t.Fatal("未探测模型和明确 countTokens 模型应保持可用")
	}
	if hasCountTokensModelCapability([]string{"generateContent"}) {
		t.Fatal("明确只支持生成的模型不得进入 countTokens 链路")
	}
}
