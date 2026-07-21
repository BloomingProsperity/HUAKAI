package audiohttp

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

func TestRequireAudioEndpointCapabilityCoversPrimaryAndFallbackAttempts(t *testing.T) {
	plan := router.RoutePlan{
		Attempts: []router.AttemptPlan{{RequiredCapabilities: []string{"json"}}},
		FallbackPhases: []router.FallbackPhasePlan{{
			Attempts: []router.AttemptPlan{{RequiredCapabilities: []string{audioSpeechCapability}}},
		}},
	}

	requireAudioEndpointCapability(&plan, audioEndpointSpeech)

	if got := plan.Attempts[0].RequiredCapabilities; len(got) != 2 || got[0] != "json" || got[1] != audioSpeechCapability {
		t.Fatalf("主路能力=%v，期望 [json %s]", got, audioSpeechCapability)
	}
	if got := plan.FallbackPhases[0].Attempts[0].RequiredCapabilities; len(got) != 1 || got[0] != audioSpeechCapability {
		t.Fatalf("降级路能力=%v，期望去重后的 [%s]", got, audioSpeechCapability)
	}
}

func TestAudioEndpointCapabilitiesStaySeparated(t *testing.T) {
	if !hasAudioEndpointCapability([]string{audioSpeechCapability}, audioEndpointSpeech) {
		t.Fatal("语音合成模型应通过 speech 能力门")
	}
	if hasAudioEndpointCapability([]string{audioTranscriptionCapability}, audioEndpointSpeech) {
		t.Fatal("仅转写模型不得通过 speech 能力门")
	}
	if !hasAudioEndpointCapability([]string{audioTranscriptionCapability}, audioEndpointTranslations) {
		t.Fatal("音频翻译与转写共用 transcription 能力门")
	}
}
