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
		{name: "Vertex + mimicry_gemini_advanced 允许", provider: ProviderVertex, mode: TransportModeMimicryGeminiAdvanced},
		{name: "Vertex + mimicry_antigravity 允许", provider: ProviderVertex, mode: TransportModeMimicryAntigravity},
		{name: "Vertex + mimicry_claude_code 跨 vendor 拒绝", provider: ProviderVertex, mode: TransportModeMimicryClaudeCode, wantErrIs: ErrModeNotAllowedForProvider},
		{name: "Cursor + mimicry_cursor 允许", provider: ProviderCursor, mode: TransportModeMimicryCursor},
		{name: "Copilot + mimicry_copilot 允许", provider: ProviderCopilot, mode: TransportModeMimicryCopilot},
		{name: "Kiro + mimicry_kiro 允许", provider: ProviderKiro, mode: TransportModeMimicryKiro},
		{name: "Windsurf + mimicry_windsurf 允许", provider: ProviderWindsurf, mode: TransportModeMimicryWindsurf},
		{name: "OpenRouter + 任意 mimicry 拒绝（meta-aggregator 不反转）", provider: ProviderOpenRouter, mode: TransportModeMimicryChatGPT, wantErrIs: ErrModeNotAllowedForProvider},
		{name: "Grok + mimicry 拒绝", provider: ProviderGrok, mode: TransportModeMimicryChatGPT, wantErrIs: ErrModeNotAllowedForProvider},
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
		{ProviderAnthropic, 3},  // standard / mimicry_claude_code / diagnostics
		{ProviderOpenAI, 3},     // standard / mimicry_chatgpt / diagnostics
		{ProviderVertex, 4},     // standard / mimicry_gemini_advanced / mimicry_antigravity / diagnostics
		{ProviderBedrock, 3},    // standard / mimicry_kiro / diagnostics
		{ProviderOpenRouter, 2}, // standard / diagnostics（无反转）
		{ProviderGrok, 2},       // standard / diagnostics
		{ProviderCursor, 2},     // standard / mimicry_cursor
		{ProviderCopilot, 2},
		{ProviderKiro, 2},
		{ProviderWindsurf, 2},
		{ProviderAntigravity, 2},
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
