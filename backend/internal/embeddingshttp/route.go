package embeddingshttp

import (
	"github.com/BloomingProsperity/HUAKAI/internal/bindingfallback"
	"github.com/BloomingProsperity/HUAKAI/internal/registry"
	"github.com/BloomingProsperity/HUAKAI/internal/router"
)

const embeddingsCapability = "embeddings"

func hasEmbeddingsModelCapability(capabilities []string) bool {
	// 空集合表示目录尚未完成能力探测，继续兼容既有人工模型；一旦目录给出
	// 明确集合，就必须证明它属于 embeddings 能力族。
	if len(capabilities) == 0 {
		return true
	}
	for _, capability := range capabilities {
		switch capability {
		case embeddingsCapability, "embedContent", "batchEmbedContents":
			return true
		}
	}
	return false
}

func requireEmbeddingsCapability(plan *router.RoutePlan) {
	if plan == nil {
		return
	}
	for index := range plan.Attempts {
		plan.Attempts[index].RequiredCapabilities = appendEmbeddingsCapability(
			plan.Attempts[index].RequiredCapabilities,
			embeddingsCapability,
		)
	}
	for phaseIndex := range plan.FallbackPhases {
		for attemptIndex := range plan.FallbackPhases[phaseIndex].Attempts {
			attempt := &plan.FallbackPhases[phaseIndex].Attempts[attemptIndex]
			attempt.RequiredCapabilities = appendEmbeddingsCapability(attempt.RequiredCapabilities, embeddingsCapability)
		}
	}
}

func appendEmbeddingsCapability(capabilities []string, required string) []string {
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
