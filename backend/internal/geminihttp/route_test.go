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
