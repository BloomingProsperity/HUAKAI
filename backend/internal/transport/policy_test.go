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
		{name: "Anthropic + mimicry 允许", provider: ProviderAnthropic, mode: TransportModeMimicryClaudeCode},
		{name: "Anthropic + diagnostics 允许", provider: ProviderAnthropic, mode: TransportModeDiagnosticsOnly},
		{name: "OpenAI + standard 允许", provider: ProviderOpenAI, mode: TransportModeStandard},
		{name: "OpenAI + mimicry 拒绝", provider: ProviderOpenAI, mode: TransportModeMimicryClaudeCode, wantErrIs: ErrModeNotAllowedForProvider},
		{name: "Vertex + mimicry 拒绝", provider: ProviderVertex, mode: TransportModeMimicryClaudeCode, wantErrIs: ErrModeNotAllowedForProvider},
		{name: "Bedrock + mimicry 拒绝", provider: ProviderBedrock, mode: TransportModeMimicryClaudeCode, wantErrIs: ErrModeNotAllowedForProvider},
		{name: "OpenRouter + mimicry 拒绝", provider: ProviderOpenRouter, mode: TransportModeMimicryClaudeCode, wantErrIs: ErrModeNotAllowedForProvider},
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
	got := AllowedModesForProvider(ProviderAnthropic)
	if len(got) != 3 {
		t.Errorf("Anthropic 应允许 3 种 mode，得到 %d: %v", len(got), got)
	}
	got = AllowedModesForProvider(ProviderOpenAI)
	if len(got) != 2 {
		t.Errorf("OpenAI 应允许 2 种 mode，得到 %d: %v", len(got), got)
	}
	got = AllowedModesForProvider(ProviderCode("acme"))
	if got != nil {
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
