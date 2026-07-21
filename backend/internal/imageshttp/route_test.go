package imageshttp

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

func TestRequireImageOutputCapabilityCoversPrimaryAndFallbackAttempts(t *testing.T) {
	plan := router.RoutePlan{
		Attempts: []router.AttemptPlan{{RequiredCapabilities: []string{"json"}}},
		FallbackPhases: []router.FallbackPhasePlan{{
			Attempts: []router.AttemptPlan{{RequiredCapabilities: []string{"image_output"}}},
		}},
	}

	requireImageOutputCapability(&plan)

	if got := plan.Attempts[0].RequiredCapabilities; len(got) != 2 || got[0] != "json" || got[1] != "image_output" {
		t.Fatalf("主尝试能力=%v，期望保留 json 并追加 image_output", got)
	}
	if got := plan.FallbackPhases[0].Attempts[0].RequiredCapabilities; len(got) != 1 || got[0] != "image_output" {
		t.Fatalf("回落尝试能力=%v，期望 image_output 不重复", got)
	}
}

func TestHasImageOutputCapabilityRejectsTextOnlyModels(t *testing.T) {
	if hasImageOutputCapability([]string{"chat", "tools"}) {
		t.Fatal("纯文本模型不应进入图片通道")
	}
	if !hasImageOutputCapability([]string{"chat", "image_output"}) {
		t.Fatal("image_output 模型应进入图片通道")
	}
	if !hasImageOutputCapability([]string{"images"}) {
		t.Fatal("模型同步兼容词 images 应进入图片通道")
	}
}
