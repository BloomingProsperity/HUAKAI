package mimicry

import "testing"

func TestSidecarProfileCatalogCoversEveryMimicryMode(t *testing.T) {
	cases := map[TransportMode]string{
		ModeMimicryClaudeCode:     SidecarProfileAnthropicCLIMimicryV1,
		ModeMimicryChatGPT:        SidecarProfileOpenAICodexCLIV1,
		ModeMimicryGeminiAdvanced: SidecarProfileGeminiCLIV1,
		ModeMimicryAntigravity:    SidecarProfileAntigravitySafeV1,
		ModeMimicryCursor:         SidecarProfileCursorSafeV1,
		ModeMimicryCopilot:        SidecarProfileCopilotSafeV1,
		ModeMimicryKiro:           SidecarProfileKiroCLIV1,
		ModeMimicryWindsurf:       SidecarProfileWindsurfSafeV1,
	}
	seenProfiles := make(map[string]struct{}, len(cases))
	for mode, want := range cases {
		got, ok := SidecarProfileForMode(mode)
		if !ok || got != want {
			t.Fatalf("mode=%s profile=%q ok=%v，期望 %q/true", mode, got, ok, want)
		}
		if _, duplicate := seenProfiles[got]; duplicate {
			t.Fatalf("mode=%s 复用了 profile=%q，无法独立诊断", mode, got)
		}
		seenProfiles[got] = struct{}{}
	}
	for _, profileID := range requiredSidecarProfiles {
		if profileID == SidecarProfileOperatorSourceSafeV1 {
			continue
		}
		if _, ok := seenProfiles[profileID]; !ok {
			t.Fatalf("启动门包含未映射 profile=%q", profileID)
		}
	}
	if !containsString(requiredSidecarProfiles, SidecarProfileOperatorSourceSafeV1) {
		t.Fatal("启动门必须覆盖受控运营端点 profile")
	}
}

func TestSidecarTransportPoolTuning(t *testing.T) {
	rt := NewSidecarRoundTripper(NewSidecarClient("/run/huakai/tls-sidecar.sock"), SidecarProfileAnthropicCLIMimicryV1)
	transport, ok := rt.(*sidecarTransport)
	if !ok {
		t.Fatalf("sidecar transport 类型=%T，期望 *sidecarTransport", rt)
	}
	if transport.MaxIdleConnsPerHost != 64 || transport.MaxIdleConns != 256 {
		t.Fatalf("sidecar 连接池=%d/%d，期望 64/256", transport.MaxIdleConnsPerHost, transport.MaxIdleConns)
	}
}
