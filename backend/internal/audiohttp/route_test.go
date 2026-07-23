package audiohttp

import (
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/modality"

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

// TestAudioEndpointModalityMapping 锁定端点→媒体能力域映射:speech=语音合成,
// transcriptions/translations 共用转写域。判别性:换错任一映射即转红。
func TestAudioEndpointModalityMapping(t *testing.T) {
	if audioEndpointSpeech.Modality() != modality.AudioSpeech {
		t.Fatal("speech 端点应映射语音合成能力域")
	}
	if audioEndpointTranscriptions.Modality() != modality.AudioTranscription {
		t.Fatal("transcriptions 端点应映射转写能力域")
	}
	if audioEndpointTranslations.Modality() != modality.AudioTranscription {
		t.Fatal("translations 端点与转写共用能力域")
	}
}
