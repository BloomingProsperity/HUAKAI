package servingcapability

import (
	"sort"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/provider/registrydefault"
)

const (
	envGeminiCodeAssist = "HUAKAI_ENABLE_GEMINI_CODE_ASSIST_ADAPTER"
	envCursorSession    = "HUAKAI_ENABLE_CURSOR_SESSION_ADAPTER"
	envCopilotSession   = "HUAKAI_ENABLE_COPILOT_SESSION_ADAPTER"
	envGeminiAdvanced   = "HUAKAI_ENABLE_GEMINI_ADVANCED_SESSION_ADAPTER"
	envAntigravity      = "HUAKAI_ENABLE_ANTIGRAVITY_SESSION_ADAPTER"
	envKiroSession      = "HUAKAI_ENABLE_KIRO_SESSION_ADAPTER"
	envWindsurfSession  = "HUAKAI_ENABLE_WINDSURF_SESSION_ADAPTER"
)

var gatedFamilyByEnv = map[string]string{
	envGeminiCodeAssist: registrydefault.ProtocolGeminiCodeAssist,
	envCursorSession:    registrydefault.ProtocolCursorSession,
	envCopilotSession:   registrydefault.ProtocolCopilotSession,
	envGeminiAdvanced:   registrydefault.ProtocolGeminiAdvancedSession,
	envAntigravity:      registrydefault.ProtocolAntigravitySession,
	envKiroSession:      registrydefault.ProtocolKiroSession,
	envWindsurfSession:  registrydefault.ProtocolWindsurfSession,
}

var defaultVisibleFamilies = []string{
	registrydefault.ProtocolOpenAIChat,
	registrydefault.ProtocolOpenAIResponses,
	registrydefault.ProtocolOpenAICodex,
	registrydefault.ProtocolAnthropicMessages,
	registrydefault.ProtocolGeminiMessages,
	registrydefault.ProtocolOpenRouterChat,
	registrydefault.ProtocolBedrockInvoke,
	registrydefault.ProtocolGrokChat,
	registrydefault.ProtocolKimiChat,
	registrydefault.ProtocolDeepSeekChat,
	registrydefault.ProtocolMistralChat,
	registrydefault.ProtocolGroqCloudChat,
	registrydefault.ProtocolTogetherChat,
	registrydefault.ProtocolPerplexityChat,
	registrydefault.ProtocolFireworksChat,
	registrydefault.ProtocolQwenChat,
	registrydefault.ProtocolGLMChat,
	registrydefault.ProtocolYiChat,
	registrydefault.ProtocolBaichuanChat,
	registrydefault.ProtocolDoubaoChat,
	registrydefault.ProtocolErnieChat,
	registrydefault.ProtocolStepChat,
	registrydefault.ProtocolHunyuanChat,
	registrydefault.ProtocolMinimaxChat,
	registrydefault.ProtocolCohereChat,
	registrydefault.ProtocolOllamaChat,
	registrydefault.ProtocolOllamaNative,
	registrydefault.ProtocolDifyChat,
	registrydefault.ProtocolReplicateImage,
	registrydefault.ProtocolVertexGemini,
	registrydefault.ProtocolVertexAnthropic,
}

var visibleButNotEnableable = map[string]bool{
	// 这些 family 虽有 adapter 注册路径，但产品意图仍是 scaffold。
	registrydefault.ProtocolMistralChat:    true,
	registrydefault.ProtocolGroqCloudChat:  true,
	registrydefault.ProtocolTogetherChat:   true,
	registrydefault.ProtocolPerplexityChat: true,
	registrydefault.ProtocolFireworksChat:  true,
	// 下列 session family 即使 env on，wire 仍未经验证。
	registrydefault.ProtocolCursorSession:         true,
	registrydefault.ProtocolCopilotSession:        true,
	registrydefault.ProtocolGeminiAdvancedSession: true,
	registrydefault.ProtocolAntigravitySession:    true,
	registrydefault.ProtocolKiroSession:           true,
	registrydefault.ProtocolWindsurfSession:       true,
}

func TestProductionRegistryEnvironmentMatrices(t *testing.T) {
	tests := []struct {
		name       string
		enabledEnv []string
	}{
		{name: "default env"},
		{name: "single env on", enabledEnv: []string{envGeminiCodeAssist}},
		{name: "all env on", enabledEnv: sortedEnvironmentNames()},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			setAdapterEnvironment(t, tc.enabledEnv)
			registry := registrydefault.Build()
			evaluator := NewEvaluator(nil, productionRuntimeSources(registry))

			expectedVisible := stringSet(defaultVisibleFamilies)
			for _, env := range tc.enabledEnv {
				expectedVisible[gatedFamilyByEnv[env]] = true
			}
			actualVisible := stringSet(registry.RegisteredProtocolFamilies())
			assertSameStringSet(t, "可见 family", actualVisible, expectedVisible)

			expectedEnableable := make(map[string]bool, len(expectedVisible))
			for family := range expectedVisible {
				if !visibleButNotEnableable[family] {
					expectedEnableable[family] = true
				}
			}
			actualEnableable := map[string]bool{}
			actualServing := map[string]bool{}
			for _, contract := range DefaultContractRegistry().All() {
				result := evaluator.EvaluateProviderConfig(ProviderConfigInput{Family: contract.Family, Enabled: true})
				if result.Allowed {
					actualEnableable[contract.Family] = true
				}
				if result.TrafficAllowed {
					actualServing[contract.Family] = true
				}
			}
			assertSameStringSet(t, "可 enable family", actualEnableable, expectedEnableable)
			assertSameStringSet(t, "可 serving family", actualServing, expectedEnableable)
		})
	}
}

func TestCurrentClaudeAndAntigravityVerdictsArePinned(t *testing.T) {
	setAdapterEnvironment(t, sortedEnvironmentNames())
	registry := registrydefault.Build()
	evaluator := NewEvaluator(nil, productionRuntimeSources(registry))

	claude := evaluator.EvaluateProviderConfig(ProviderConfigInput{
		Family: registrydefault.ProtocolAnthropicClaudeSession, Enabled: true,
	})
	if claude.Ready || claude.Allowed || claude.TrafficAllowed ||
		claude.Status != StatusCollectableNotServing || claude.Reason != ReasonCollectableNotServing ||
		claude.Action != ActionHideReadOnly {
		t.Fatalf("Claude OAuth 当前结论漂移: %+v", claude)
	}
	if _, err := registry.For(registrydefault.ProtocolAnthropicClaudeSession); err == nil {
		t.Fatal("Claude OAuth session 不得因全 env on 被注册为当前进程 adapter")
	}

	antigravity := evaluator.EvaluateProviderConfig(ProviderConfigInput{
		Family: registrydefault.ProtocolAntigravitySession, Enabled: true,
	})
	if antigravity.Ready || antigravity.Allowed || antigravity.TrafficAllowed ||
		antigravity.Status != StatusExperimentalWireUnverified || antigravity.Reason != ReasonExperimentalWireUnverified ||
		antigravity.Action != ActionMarkRed {
		t.Fatalf("Antigravity 当前结论漂移: %+v", antigravity)
	}
	if _, err := registry.For(registrydefault.ProtocolAntigravitySession); err != nil {
		t.Fatalf("全 env on 应只让 Antigravity adapter 可见，实际未注册: %v", err)
	}
}

func setAdapterEnvironment(t *testing.T, enabled []string) {
	t.Helper()
	enabledSet := stringSet(enabled)
	for env := range gatedFamilyByEnv {
		value := "false"
		if enabledSet[env] {
			value = "true"
		}
		t.Setenv(env, value)
	}
}

func sortedEnvironmentNames() []string {
	out := make([]string, 0, len(gatedFamilyByEnv))
	for env := range gatedFamilyByEnv {
		out = append(out, env)
	}
	sort.Strings(out)
	return out
}

func stringSet(values []string) map[string]bool {
	out := make(map[string]bool, len(values))
	for _, value := range values {
		out[value] = true
	}
	return out
}

func assertSameStringSet(t *testing.T, label string, got, want map[string]bool) {
	t.Helper()
	missing := make([]string, 0)
	extra := make([]string, 0)
	for value := range want {
		if !got[value] {
			missing = append(missing, value)
		}
	}
	for value := range got {
		if !want[value] {
			extra = append(extra, value)
		}
	}
	if len(missing) == 0 && len(extra) == 0 {
		return
	}
	sort.Strings(missing)
	sort.Strings(extra)
	t.Fatalf("%s 集合不一致: missing=%v extra=%v", label, missing, extra)
}
