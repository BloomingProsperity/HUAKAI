// 包 transport — provider × mode 策略矩阵 + factory 行为测试。
package transport

import (
	"errors"
	"net/http"
	"testing"
)

func TestValidateModeForProvider_Matrix(t *testing.T) {
	cases := []struct {
		name      string
		provider  ProviderCode
		mode      TransportMode
		wantErrIs error
	}{
		{name: "Anthropic + standard 允许", provider: ProviderAnthropic, mode: TransportModeStandard},
		{name: "Anthropic + mimicry_claude_code 允许（mode 保留供未来重启用）", provider: ProviderAnthropic, mode: TransportModeMimicryClaudeCode},
		{name: "Anthropic + diagnostics 允许", provider: ProviderAnthropic, mode: TransportModeDiagnosticsOnly},
		{name: "Anthropic + mimicry_chatgpt 跨 vendor 拒绝", provider: ProviderAnthropic, mode: TransportModeMimicryChatGPT, wantErrIs: ErrModeNotAllowedForProvider},
		{name: "OpenAI + standard 允许", provider: ProviderOpenAI, mode: TransportModeStandard},
		{name: "OpenAI + mimicry_chatgpt 允许（ChatGPT/Codex 反转）", provider: ProviderOpenAI, mode: TransportModeMimicryChatGPT},
		{name: "OpenAI + mimicry_claude_code 跨 vendor 拒绝", provider: ProviderOpenAI, mode: TransportModeMimicryClaudeCode, wantErrIs: ErrModeNotAllowedForProvider},
		{name: "OpenAICodex + standard 允许", provider: ProviderOpenAICodex, mode: TransportModeStandard},
		{name: "OpenAICodex + mimicry_chatgpt 允许（chatgpt.com 反转）", provider: ProviderOpenAICodex, mode: TransportModeMimicryChatGPT},
		{name: "OpenAICodex + mimicry_claude_code 跨 vendor 拒绝", provider: ProviderOpenAICodex, mode: TransportModeMimicryClaudeCode, wantErrIs: ErrModeNotAllowedForProvider},
		{name: "Vertex + mimicry_gemini_advanced 允许", provider: ProviderVertex, mode: TransportModeMimicryGeminiAdvanced},
		{name: "Vertex + mimicry_antigravity 允许", provider: ProviderVertex, mode: TransportModeMimicryAntigravity},
		{name: "Vertex + mimicry_claude_code 跨 vendor 拒绝", provider: ProviderVertex, mode: TransportModeMimicryClaudeCode, wantErrIs: ErrModeNotAllowedForProvider},
		{name: "Cursor + mimicry_cursor 允许", provider: ProviderCursor, mode: TransportModeMimicryCursor},
		{name: "Copilot + mimicry_copilot 允许", provider: ProviderCopilot, mode: TransportModeMimicryCopilot},
		{name: "Kiro + mimicry_kiro 允许", provider: ProviderKiro, mode: TransportModeMimicryKiro},
		{name: "Windsurf + mimicry_windsurf 允许", provider: ProviderWindsurf, mode: TransportModeMimicryWindsurf},
		{name: "GeminiAdvanced + standard 允许", provider: ProviderGeminiAdvanced, mode: TransportModeStandard},
		{name: "GeminiAdvanced + mimicry_gemini_advanced 允许", provider: ProviderGeminiAdvanced, mode: TransportModeMimicryGeminiAdvanced},
		{name: "GeminiAdvanced + mimicry_chatgpt 跨 vendor 拒绝", provider: ProviderGeminiAdvanced, mode: TransportModeMimicryChatGPT, wantErrIs: ErrModeNotAllowedForProvider},
		{name: "OpenRouter + 任意 mimicry 拒绝（meta-aggregator 不反转）", provider: ProviderOpenRouter, mode: TransportModeMimicryChatGPT, wantErrIs: ErrModeNotAllowedForProvider},
		{name: "Grok + mimicry 拒绝", provider: ProviderGrok, mode: TransportModeMimicryChatGPT, wantErrIs: ErrModeNotAllowedForProvider},
		// 6 家 OpenAI 兼容直通 — standard 允许
		{name: "DeepSeek + standard 允许", provider: ProviderDeepSeek, mode: TransportModeStandard},
		{name: "Mistral + standard 允许", provider: ProviderMistral, mode: TransportModeStandard},
		{name: "GroqCloud + standard 允许", provider: ProviderGroqCloud, mode: TransportModeStandard},
		{name: "Together + standard 允许", provider: ProviderTogether, mode: TransportModeStandard},
		{name: "Perplexity + standard 允许", provider: ProviderPerplexity, mode: TransportModeStandard},
		{name: "Fireworks + standard 允许", provider: ProviderFireworks, mode: TransportModeStandard},
		// 跨 vendor mimicry 拒绝
		{name: "DeepSeek + mimicry_chatgpt 跨 vendor 拒绝", provider: ProviderDeepSeek, mode: TransportModeMimicryChatGPT, wantErrIs: ErrModeNotAllowedForProvider},
		{name: "Fireworks + mimicry_cursor 跨 vendor 拒绝", provider: ProviderFireworks, mode: TransportModeMimicryCursor, wantErrIs: ErrModeNotAllowedForProvider},
		{name: "未知 provider", provider: ProviderCode("acme"), mode: TransportModeStandard, wantErrIs: ErrUnknownProvider},
		{name: "未知 mode", provider: ProviderAnthropic, mode: TransportMode("turbo"), wantErrIs: ErrUnknownMode},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateModeForProvider(tc.provider, tc.mode)
			switch {
			case tc.wantErrIs == nil && err != nil:
				t.Errorf("expected no error, got %v", err)
			case tc.wantErrIs != nil && err == nil:
				t.Errorf("expected %v, got nil", tc.wantErrIs)
			case tc.wantErrIs != nil && !errors.Is(err, tc.wantErrIs):
				t.Errorf("expected errors.Is %v, got %v", tc.wantErrIs, err)
			}
		})
	}
}

func TestAllowedModesForProvider(t *testing.T) {
	cases := []struct {
		provider ProviderCode
		want     int
	}{
		{ProviderAnthropic, 3},   // standard / mimicry_claude_code / diagnostics
		{ProviderOpenAI, 3},      // standard / mimicry_chatgpt / diagnostics
		{ProviderOpenAICodex, 3}, // standard / mimicry_chatgpt / diagnostics
		{ProviderVertex, 4},      // standard / mimicry_gemini_advanced / mimicry_antigravity / diagnostics
		{ProviderBedrock, 3},    // standard / mimicry_kiro / diagnostics
		{ProviderOpenRouter, 2}, // standard / diagnostics（无反转）
		{ProviderGrok, 2},       // standard / diagnostics
		{ProviderCursor, 2},     // standard / mimicry_cursor
		{ProviderCopilot, 2},
		{ProviderKiro, 2},
		{ProviderWindsurf, 2},
		{ProviderAntigravity, 2},
		{ProviderGeminiAdvanced, 3}, // standard / mimicry_gemini_advanced / diagnostics
		// 6 家 OpenAI 兼容直通：standard + diagnostics = 2 种 mode
		{ProviderDeepSeek, 2},
		{ProviderMistral, 2},
		{ProviderGroqCloud, 2},
		{ProviderTogether, 2},
		{ProviderPerplexity, 2},
		{ProviderFireworks, 2},
	}
	for _, c := range cases {
		got := AllowedModesForProvider(c.provider)
		if len(got) != c.want {
			t.Errorf("%s 应允许 %d 种 mode，得到 %d: %v", c.provider, c.want, len(got), got)
		}
	}
	if got := AllowedModesForProvider(ProviderCode("acme")); got != nil {
		t.Errorf("未知 provider 应返回 nil，得到 %v", got)
	}
}

func TestFactory_For_StandardDefault(t *testing.T) {
	f := NewFactory()
	rt, err := f.For(ProviderOpenAI, TransportModeStandard)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rt == nil {
		t.Fatal("standard mode 必须返回非 nil RoundTripper")
	}
	if rt != http.DefaultTransport {
		t.Errorf("未注入时 standard 应回落到 http.DefaultTransport")
	}
}

func TestFactory_For_StandardInjected(t *testing.T) {
	custom := &http.Transport{}
	f := NewFactory()
	f.SetStandard(custom)
	rt, err := f.For(ProviderOpenAI, TransportModeStandard)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rt != custom {
		t.Errorf("注入的 standard 未被使用")
	}
}

func TestFactory_For_MimicryNotImplemented(t *testing.T) {
	f := NewFactory()
	_, err := f.For(ProviderAnthropic, TransportModeMimicryClaudeCode)
	if !errors.Is(err, ErrTransportNotImplemented) {
		t.Errorf("未注入 mimicry 应返回 ErrTransportNotImplemented，得到 %v", err)
	}
}

func TestFactory_For_MimicryInjected(t *testing.T) {
	custom := &http.Transport{}
	f := NewFactory()
	f.SetMimicry(custom)
	rt, err := f.For(ProviderAnthropic, TransportModeMimicryClaudeCode)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rt != custom {
		t.Errorf("注入的 mimicry 未被使用")
	}
}

func TestFactory_For_RejectCrossProviderMimicry(t *testing.T) {
	custom := &http.Transport{}
	f := NewFactory()
	f.SetMimicry(custom)
	// 即使 mimicry RoundTripper 已注入，OpenAI provider 仍应被 policy 拒绝
	_, err := f.For(ProviderOpenAI, TransportModeMimicryClaudeCode)
	if !errors.Is(err, ErrModeNotAllowedForProvider) {
		t.Errorf("OpenAI 路径请求 mimicry 应被拒绝，得到 %v", err)
	}
}

func TestFactory_For_DiagnosticsNotImplemented(t *testing.T) {
	f := NewFactory()
	_, err := f.For(ProviderOpenAI, TransportModeDiagnosticsOnly)
	if !errors.Is(err, ErrTransportNotImplemented) {
		t.Errorf("未注入 diagnostics 应返回 ErrTransportNotImplemented，得到 %v", err)
	}
}

// TestFactory_For_AllMimicryModesNotImplemented 验证 R3 暂停期间所有
// mimicry mode（含 ChatGPT / GeminiAdvanced / Antigravity / Cursor /
// Copilot / Kiro / Windsurf）未注入时一律 fail-loud 为
// ErrTransportNotImplemented，而不是 ErrUnknownMode（旧行为会让 admin
// 误以为 mode 拼写错）。
func TestFactory_For_AllMimicryModesNotImplemented(t *testing.T) {
	cases := []struct {
		provider ProviderCode
		mode     TransportMode
	}{
		{ProviderAnthropic, TransportModeMimicryClaudeCode},
		{ProviderOpenAI, TransportModeMimicryChatGPT},
		{ProviderOpenAICodex, TransportModeMimicryChatGPT},
		{ProviderVertex, TransportModeMimicryGeminiAdvanced},
		{ProviderVertex, TransportModeMimicryAntigravity},
		{ProviderGeminiAdvanced, TransportModeMimicryGeminiAdvanced},
		{ProviderCursor, TransportModeMimicryCursor},
		{ProviderCopilot, TransportModeMimicryCopilot},
		{ProviderKiro, TransportModeMimicryKiro},
		{ProviderBedrock, TransportModeMimicryKiro},
		{ProviderWindsurf, TransportModeMimicryWindsurf},
		{ProviderAntigravity, TransportModeMimicryAntigravity},
	}
	f := NewFactory() // 不注入 mimicry
	for _, tc := range cases {
		_, err := f.For(tc.provider, tc.mode)
		if !errors.Is(err, ErrTransportNotImplemented) {
			t.Errorf("%s+%s 未注入 mimicry 应返回 ErrTransportNotImplemented，得到 %v", tc.provider, tc.mode, err)
		}
		if errors.Is(err, ErrUnknownMode) {
			t.Errorf("%s+%s 不应返回 ErrUnknownMode（mode 在枚举中）", tc.provider, tc.mode)
		}
	}
}

// TestFactory_For_AllMimicryModesUseInjected 验证注入 mimicry 后所有
// mimicry mode 都拿到同一 RoundTripper（R3 实施时通过 SetMimicry 注入
// per-mode 路由 RoundTripper）。
func TestFactory_For_AllMimicryModesUseInjected(t *testing.T) {
	custom := &http.Transport{}
	f := NewFactory()
	f.SetMimicry(custom)
	cases := []struct {
		provider ProviderCode
		mode     TransportMode
	}{
		{ProviderAnthropic, TransportModeMimicryClaudeCode},
		{ProviderOpenAI, TransportModeMimicryChatGPT},
		{ProviderCursor, TransportModeMimicryCursor},
		{ProviderCopilot, TransportModeMimicryCopilot},
		{ProviderKiro, TransportModeMimicryKiro},
		{ProviderWindsurf, TransportModeMimicryWindsurf},
	}
	for _, tc := range cases {
		rt, err := f.For(tc.provider, tc.mode)
		if err != nil {
			t.Errorf("%s+%s 注入 mimicry 后应返回 RoundTripper，得到 err=%v", tc.provider, tc.mode, err)
			continue
		}
		if rt != custom {
			t.Errorf("%s+%s RoundTripper 不是注入实例", tc.provider, tc.mode)
		}
	}
}
