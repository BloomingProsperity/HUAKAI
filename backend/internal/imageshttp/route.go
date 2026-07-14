package imageshttp

import (
	"github.com/BloomingProsperity/HUAKAI/internal/registry"
	"github.com/BloomingProsperity/HUAKAI/internal/router"
)

type imageEndpoint string

const (
	imageEndpointGenerations imageEndpoint = "generations"
	imageEndpointEdits       imageEndpoint = "edits"
	imageEndpointVariations  imageEndpoint = "variations"
)

func (e imageEndpoint) Path() string {
	switch e {
	case imageEndpointEdits:
		return "/v1/images/edits"
	case imageEndpointVariations:
		return "/v1/images/variations"
	default:
		return "/v1/images/generations"
	}
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
			MaxParallelRequests: bindingMaxParallelRequests(binding.MaxParallelRequests),
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
