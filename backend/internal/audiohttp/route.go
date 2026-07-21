package audiohttp

import (
	"github.com/BloomingProsperity/HUAKAI/internal/bindingfallback"
	"github.com/BloomingProsperity/HUAKAI/internal/registry"
	"github.com/BloomingProsperity/HUAKAI/internal/router"
)

type audioEndpoint string

const (
	audioEndpointSpeech         audioEndpoint = "speech"
	audioEndpointTranscriptions audioEndpoint = "transcriptions"
	audioEndpointTranslations   audioEndpoint = "translations"
)

const (
	audioSpeechCapability        = "audio_speech"
	audioTranscriptionCapability = "audio_transcription"
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

func (e audioEndpoint) RequiredCapability() string {
	if e == audioEndpointSpeech {
		return audioSpeechCapability
	}
	return audioTranscriptionCapability
}

func hasAudioEndpointCapability(capabilities []string, endpoint audioEndpoint) bool {
	required := endpoint.RequiredCapability()
	for _, capability := range capabilities {
		if capability == required {
			return true
		}
	}
	return false
}

func requireAudioEndpointCapability(plan *router.RoutePlan, endpoint audioEndpoint) {
	if plan == nil {
		return
	}
	required := endpoint.RequiredCapability()
	for index := range plan.Attempts {
		plan.Attempts[index].RequiredCapabilities = appendAudioCapability(
			plan.Attempts[index].RequiredCapabilities,
			required,
		)
	}
	for phaseIndex := range plan.FallbackPhases {
		for attemptIndex := range plan.FallbackPhases[phaseIndex].Attempts {
			attempt := &plan.FallbackPhases[phaseIndex].Attempts[attemptIndex]
			attempt.RequiredCapabilities = appendAudioCapability(attempt.RequiredCapabilities, required)
		}
	}
}

func appendAudioCapability(capabilities []string, required string) []string {
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
