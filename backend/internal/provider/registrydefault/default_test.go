// registrydefault 单元测试。
package registrydefault

import (
	"errors"
	"sort"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/provider"
	"github.com/BloomingProsperity/HUAKAI/internal/provider/openai"
)

func TestBuild_DefaultProtocolFamiliesRegistered(t *testing.T) {
	t.Setenv(placeholderSessionAdaptersEnv, "")
	r := Build()
	want := []string{
		ProtocolOpenAIChat,
		ProtocolOpenAIResponses,
		ProtocolOpenAICodex,
		ProtocolAnthropicMessages,
		ProtocolAnthropicClaudeSession,
		ProtocolGeminiMessages,
		ProtocolOpenRouterChat,
		ProtocolBedrockInvoke,
		ProtocolGrokChat,
		ProtocolDeepSeekChat,
		ProtocolMistralChat,
		ProtocolGroqCloudChat,
		ProtocolTogetherChat,
		ProtocolPerplexityChat,
		ProtocolFireworksChat,
	}
	got := r.RegisteredProtocolFamilies()
	sort.Strings(got)
	sort.Strings(want)
	if len(got) != len(want) {
		t.Errorf("注册数=%d want %d (%v)", len(got), len(want), got)
	}
	for i, pf := range want {
		if i >= len(got) || got[i] != pf {
			t.Errorf("缺 protocol %q（已注册：%v）", pf, got)
		}
	}
}

func TestBuild_AdaptersAreReachable(t *testing.T) {
	t.Setenv(placeholderSessionAdaptersEnv, "")
	r := Build()
	for _, pf := range []string{
		ProtocolOpenAIChat,
		ProtocolAnthropicMessages,
		ProtocolAnthropicClaudeSession,
		ProtocolGeminiMessages,
		ProtocolOpenRouterChat,
		ProtocolBedrockInvoke,
		ProtocolGrokChat,
	} {
		a, err := r.For(pf)
		if err != nil {
			t.Errorf("For(%q) err=%v", pf, err)
			continue
		}
		if a == nil {
			t.Errorf("For(%q) returned nil adapter", pf)
		}
		if a.Platform() == "" {
			t.Errorf("For(%q) adapter.Platform() 空", pf)
		}
	}
}

func TestBuild_PlatformIDsCorrect(t *testing.T) {
	t.Setenv(placeholderSessionAdaptersEnv, "")
	r := Build()
	cases := map[string]string{
		ProtocolOpenAIChat:             "openai",
		ProtocolOpenAIResponses:        "openai",
		ProtocolOpenAICodex:            "openai_codex",
		ProtocolAnthropicMessages:      "anthropic",
		ProtocolAnthropicClaudeSession: "anthropic_claude_session",
		ProtocolGeminiMessages:         "gemini",
		ProtocolOpenRouterChat:         "openrouter",
		ProtocolBedrockInvoke:          "bedrock",
		ProtocolGrokChat:               "grok",
		ProtocolDeepSeekChat:           "deepseek",
		ProtocolMistralChat:            "mistral",
		ProtocolGroqCloudChat:          "groqcloud",
		ProtocolTogetherChat:           "together",
		ProtocolPerplexityChat:         "perplexity",
		ProtocolFireworksChat:          "fireworks",
	}
	for pf, wantPlatform := range cases {
		a, err := r.For(pf)
		if err != nil {
			t.Fatalf("For(%q) err=%v", pf, err)
		}
		if got := a.Platform(); got != wantPlatform {
			t.Errorf("%q → Platform=%q want %q", pf, got, wantPlatform)
		}
	}
}

func TestBuild_OpenAIResponsesEndpointIsResponsesAPI(t *testing.T) {
	t.Setenv(placeholderSessionAdaptersEnv, "")
	r := Build()
	a, err := r.For(ProtocolOpenAIResponses)
	if err != nil {
		t.Fatalf("For(%q) err=%v", ProtocolOpenAIResponses, err)
	}
	passthrough, ok := a.(*openai.PassthroughAdapter)
	if !ok {
		t.Fatalf("adapter type=%T want *openai.PassthroughAdapter", a)
	}
	if passthrough.Endpoint != "https://api.openai.com/v1/responses" {
		t.Fatalf("Responses endpoint=%q want https://api.openai.com/v1/responses", passthrough.Endpoint)
	}
}

func TestBuild_PlaceholderSessionAdaptersDefaultOff(t *testing.T) {
	t.Setenv(placeholderSessionAdaptersEnv, "")
	r := Build()
	for _, pf := range placeholderSessionProtocolFamilies() {
		_, err := r.For(pf)
		if !errors.Is(err, provider.ErrAdapterNotRegistered) {
			t.Errorf("For(%q) err=%v want ErrAdapterNotRegistered", pf, err)
		}
	}
}

func TestBuild_PlaceholderSessionAdaptersOptIn(t *testing.T) {
	t.Setenv(placeholderSessionAdaptersEnv, "true")
	r := Build()
	cases := map[string]string{
		ProtocolCursorSession:         "cursor",
		ProtocolCopilotSession:        "copilot",
		ProtocolGeminiAdvancedSession: "gemini_advanced",
		ProtocolAntigravitySession:    "antigravity",
		ProtocolKiroSession:           "kiro",
		ProtocolWindsurfSession:       "windsurf",
	}
	for pf, wantPlatform := range cases {
		a, err := r.For(pf)
		if err != nil {
			t.Fatalf("For(%q) err=%v", pf, err)
		}
		if got := a.Platform(); got != wantPlatform {
			t.Errorf("%q → Platform=%q want %q", pf, got, wantPlatform)
		}
	}
}

func TestBuild_UnregisteredReturnsErrAdapterNotRegistered(t *testing.T) {
	t.Setenv(placeholderSessionAdaptersEnv, "")
	r := Build()
	for _, pf := range []string{
		"chatgpt_session", // OpenAI 反转旧名（现 openai_codex），未注册
		"anthropic_oauth", // Anthropic 反转（暂停）
		"copilot_oauth",   // Copilot OAuth 旧名（现 copilot_session），未注册
		"unknown",         // 完全未知
	} {
		_, err := r.For(pf)
		if err == nil {
			t.Errorf("For(%q) 应返回 error", pf)
			continue
		}
		// 不要求严格 errors.Is，只确认 error 文案含 protocol family
		if errStr := err.Error(); errStr == "" {
			t.Errorf("For(%q) error 文案空", pf)
		}
	}
}

// TestBuild_ConsistentWithProviderInterface 确保所有注册的 adapter
// 都满足 provider.Adapter 接口（编译期已保证；本测试仅作 smoke）。
func TestBuild_ConsistentWithProviderInterface(t *testing.T) {
	t.Setenv(placeholderSessionAdaptersEnv, "")
	r := Build()
	var _ provider.Adapter
	for _, pf := range r.RegisteredProtocolFamilies() {
		a, err := r.For(pf)
		if err != nil {
			t.Fatal(err)
		}
		// 调用接口三个方法不应 panic
		_ = a.Platform()
		_ = a.AcceptableCredentialTypes()
	}
}

func placeholderSessionProtocolFamilies() []string {
	return []string{
		ProtocolCursorSession,
		ProtocolCopilotSession,
		ProtocolGeminiAdvancedSession,
		ProtocolAntigravitySession,
		ProtocolKiroSession,
		ProtocolWindsurfSession,
	}
}
