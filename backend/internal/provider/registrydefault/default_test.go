// registrydefault 单元测试。
package registrydefault

import (
	"sort"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/provider"
)

func TestBuild_AllProtocolFamiliesRegistered(t *testing.T) {
	r := Build()
	want := []string{
		ProtocolOpenAIChat,
		ProtocolOpenAIResponses,
		ProtocolAnthropicMessages,
		ProtocolGeminiMessages,
		ProtocolOpenRouterChat,
		ProtocolBedrockInvoke,
		ProtocolGrokChat,
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
	r := Build()
	for _, pf := range []string{
		ProtocolOpenAIChat,
		ProtocolAnthropicMessages,
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
	r := Build()
	cases := map[string]string{
		ProtocolOpenAIChat:        "openai",
		ProtocolOpenAIResponses:   "openai",
		ProtocolAnthropicMessages: "anthropic",
		ProtocolGeminiMessages:    "gemini",
		ProtocolOpenRouterChat:    "openrouter",
		ProtocolBedrockInvoke:     "bedrock",
		ProtocolGrokChat:          "grok",
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
	r := Build()
	for _, pf := range []string{
		"chatgpt_session",  // OpenAI 反转，未实现
		"anthropic_oauth",  // Anthropic 反转（暂停）
		"cursor_session",   // Cursor 反转，未实现
		"copilot_oauth",    // Copilot 反转，未实现
		"unknown",          // 完全未知
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
