package rerankhttp

import (
	"github.com/BloomingProsperity/HUAKAI/internal/bindingfallback"
	"github.com/BloomingProsperity/HUAKAI/internal/registry"
	"github.com/BloomingProsperity/HUAKAI/internal/router"
)

const rerankCapability = "rerank"

func hasRerankModelCapability(capabilities []string) bool {
	if len(capabilities) == 0 {
		return true
	}
	for _, capability := range capabilities {
		if capability == rerankCapability {
			return true
		}
	}
	return false
}

func requireRerankCapability(plan *router.RoutePlan) {
	if plan == nil {
		return
	}
	for index := range plan.Attempts {
		plan.Attempts[index].RequiredCapabilities = appendRerankCapability(
			plan.Attempts[index].RequiredCapabilities,
			rerankCapability,
		)
	}
	for phaseIndex := range plan.FallbackPhases {
		for attemptIndex := range plan.FallbackPhases[phaseIndex].Attempts {
			attempt := &plan.FallbackPhases[phaseIndex].Attempts[attemptIndex]
			attempt.RequiredCapabilities = appendRerankCapability(attempt.RequiredCapabilities, rerankCapability)
		}
	}
}

func appendRerankCapability(capabilities []string, required string) []string {
	for _, capability := range capabilities {
		if capability == required {
			return capabilities
		}
	}
	return append(capabilities, required)
}

func routerResolvedModel(resolved registry.Resolved) router.ResolvedModel {
	out := router.ResolvedModel{
		PublicAlias:     resolved.PublicAlias,
		InternalModelID: resolved.CanonicalModelID,
		ProviderModelID: resolved.ProviderModelID,
		ContextWindow:   resolved.ContextWindow,
		Capabilities:    resolved.Capabilities,
		PricingClass:    resolved.PricingClass,
		ProtocolFamily:  resolved.ProtocolFamily,
		PoolCandidates:  resolved.PoolCandidates,
		SnapshotVersion: resolved.SnapshotVersion,
	}
	for _, binding := range resolved.BindingMetadata {
		providerModelID := resolved.DefaultProviderModelID
		if providerModelID == "" {
			providerModelID = resolved.ProviderModelID
		}
		if binding.ProviderModelIDOverride != nil && *binding.ProviderModelIDOverride != "" {
			providerModelID = *binding.ProviderModelIDOverride
		}
		out.PoolMetadata = append(out.PoolMetadata, router.PoolCandidateMeta{
			PoolGroupID:         binding.PoolGroupID,
			ProviderModelID:     providerModelID,
			BindingID:           binding.BindingID,
			BindingRPMLimit:     bindingMaxParallelRequests(binding.RPMLimit),
			BindingTPMLimit:     bindingMaxParallelRequests(binding.TPMLimit),
			MaxParallelRequests: bindingMaxParallelRequests(binding.MaxParallelRequests),
			Priority:            binding.Priority,
			Weight:              binding.Weight,
			SelectionMode:       binding.SelectionMode,
			FallbackClass:       bindingfallback.NormalizeClass(binding.FallbackClass),
		})
	}
	return out
}

func bindingMaxParallelRequests(v *int32) int64 {
	if v == nil {
		return 0
	}
	return int64(*v)
}
