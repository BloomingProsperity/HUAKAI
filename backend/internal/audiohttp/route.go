package audiohttp

import (
	"github.com/BloomingProsperity/HUAKAI/internal/bindingfallback"
	"github.com/BloomingProsperity/HUAKAI/internal/modality"
	"github.com/BloomingProsperity/HUAKAI/internal/registry"
	"github.com/BloomingProsperity/HUAKAI/internal/router"
)

type audioEndpoint string

const (
	audioEndpointSpeech         audioEndpoint = "speech"
	audioEndpointTranscriptions audioEndpoint = "transcriptions"
	audioEndpointTranslations   audioEndpoint = "translations"
)

func (e audioEndpoint) Path() string {
	switch e {
	case audioEndpointTranscriptions:
		return "/v1/audio/transcriptions"
	case audioEndpointTranslations:
		return "/v1/audio/translations"
	default:
		return "/v1/audio/speech"
	}
}

// Modality 返回端点对应的媒体能力域:speech=语音合成,transcriptions/translations=转写。
func (e audioEndpoint) Modality() modality.Modality {
	if e == audioEndpointSpeech {
		return modality.AudioSpeech
	}
	return modality.AudioTranscription
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
