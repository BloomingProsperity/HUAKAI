package servingcapability

import (
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/provider/registrydefault"
)

// 变异：把任一无凭据 handler 的契约改回 Released，会同时击穿发布态与
// releaseCanServe 断言；遗漏契约、改坏 wire shape 或图片车道也会独立转红。
func TestNoCredentialHandlerContractsRemainScaffolded(t *testing.T) {
	tests := []struct {
		family        string
		requestShape  string
		responseShape string
		stream        string
		lane          ServingLane
	}{
		{family: registrydefault.ProtocolOpenRouterChat, requestShape: registrydefault.ProtocolOpenAIChat, responseShape: registrydefault.ProtocolOpenAIChat, stream: streamFramingSSE, lane: ServingLaneChatHCSF},
		{family: registrydefault.ProtocolCohereChat, requestShape: registrydefault.ProtocolOpenAIChat, responseShape: registrydefault.ProtocolOpenAIChat, stream: streamFramingSSE, lane: ServingLaneChatHCSF},
		{family: registrydefault.ProtocolOllamaChat, requestShape: registrydefault.ProtocolOpenAIChat, responseShape: registrydefault.ProtocolOpenAIChat, stream: streamFramingSSE, lane: ServingLaneChatHCSF},
		{family: registrydefault.ProtocolOllamaNative, requestShape: registrydefault.ProtocolOllamaNative, responseShape: registrydefault.ProtocolOllamaNative, stream: streamFramingNDJSON, lane: ServingLaneChatHCSF},
		{family: registrydefault.ProtocolDifyChat, requestShape: registrydefault.ProtocolDifyChat, responseShape: registrydefault.ProtocolDifyChat, stream: streamFramingSSE, lane: ServingLaneChatHCSF},
		{family: registrydefault.ProtocolReplicateImage, requestShape: registrydefault.ProtocolReplicateImage, responseShape: registrydefault.ProtocolReplicateImage, stream: streamFramingNone, lane: ServingLaneImage},
	}

	registry := DefaultContractRegistry()
	evaluator := NewEvaluator(registry, productionRuntimeSources(registrydefault.Build()))
	for _, tc := range tests {
		t.Run(tc.family, func(t *testing.T) {
			if !HasContract(tc.family) {
				t.Fatalf("HasContract(%q)=false want true", tc.family)
			}
			contract, ok := registry.Lookup(tc.family)
			if !ok {
				t.Fatalf("Lookup(%q) 未找到契约", tc.family)
			}
			if contract.ReleaseState != ReleaseStateScaffold {
				t.Fatalf("ReleaseState=%q want %q", contract.ReleaseState, ReleaseStateScaffold)
			}
			if releaseCanServe(contract.ReleaseState) {
				t.Fatalf("releaseCanServe(%q)=true want false", contract.ReleaseState)
			}
			if contract.RequestMarshalShape != tc.requestShape || contract.ResponseParseShape != tc.responseShape ||
				contract.StreamFraming != tc.stream || contract.Lane != tc.lane {
				t.Fatalf("wire 契约漂移: got=%+v", contract)
			}
			if !contract.WireVerified || contract.ModelDiscoveryScope != ModelDiscoveryGlobal || contract.ReadinessReason != ReasonNoCredentialHandler {
				t.Fatalf("scaffold 元数据漂移: got=%+v", contract)
			}

			result := evaluator.EvaluateProviderConfig(ProviderConfigInput{Family: tc.family, Enabled: true})
			if result.Ready || result.Allowed || result.TrafficAllowed || result.StartupBlocking ||
				result.Status != StatusScaffold || result.Action != ActionHideReadOnly || result.Reason != ReasonNoCredentialHandler {
				t.Fatalf("scaffold serving 结论错误: %+v", result)
			}
		})
	}
}
