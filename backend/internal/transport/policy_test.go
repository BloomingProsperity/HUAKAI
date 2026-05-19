// 包 transport — provider × mode 策略矩阵 + factory 行为测试。
package transport

import (
	"errors"
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
		{name: "Gemini + standard 允许（标准 API key 路径）", provider: ProviderGemini, mode: TransportModeStandard},
		{name: "Gemini + diagnostics 允许", provider: ProviderGemini, mode: TransportModeDiagnosticsOnly},
		{name: "Gemini + mimicry_gemini_advanced 拒绝（mimicry 留给 GeminiAdvanced）", provider: ProviderGemini, mode: TransportModeMimicryGeminiAdvanced, wantErrIs: ErrModeNotAllowedForProvider},
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
		{ProviderBedrock, 3},     // standard / mimicry_kiro / diagnostics
		{ProviderOpenRouter, 2},  // standard / diagnostics（无反转）
		{ProviderGrok, 2},        // standard / diagnostics
		{ProviderCursor, 2},      // standard / mimicry_cursor
		{ProviderCopilot, 2},
		{ProviderKiro, 2},
		{ProviderWindsurf, 2},
		{ProviderAntigravity, 2},
		{ProviderGemini, 2},         // standard / diagnostics（标准 API key，不反转）
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
