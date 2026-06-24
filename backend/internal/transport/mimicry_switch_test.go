package transport

import (
	"net/http"
	"testing"
)

// TestMimicryEnabled 锁定开关语义:仅显式小写 "false" 关闭,其余(含未设/空串)
// 一律默认开。变异验证:把判定改成 == "true" / != "" / == "false",空串或
// "false" 用例必转红。
func TestMimicryEnabled(t *testing.T) {
	cases := []struct {
		val  string
		want bool
	}{
		{"", true},       // 空串(等价未显式设)默认开
		{"false", false}, // 唯一的关闭值
		{"true", true},
		{"1", true},
		{"FALSE", true}, // 大小写敏感:非小写 false 仍视为开
		{"off", true},
	}
	for _, c := range cases {
		t.Setenv(mimicryEnvVar, c.val)
		if got := MimicryEnabled(); got != c.want {
			t.Fatalf("MimicryEnabled(env=%q)=%v want %v", c.val, got, c.want)
		}
	}
}

// TestTransportModeIsMimicry 锁定 8 个 mimicry_* 判真、standard/diagnostics/未知判假。
// 变异验证:isMimicry 漏掉任一 case,对应 mode 用例转红。
func TestTransportModeIsMimicry(t *testing.T) {
	mimicryModes := []TransportMode{
		TransportModeMimicryClaudeCode,
		TransportModeMimicryChatGPT,
		TransportModeMimicryGeminiAdvanced,
		TransportModeMimicryAntigravity,
		TransportModeMimicryCursor,
		TransportModeMimicryCopilot,
		TransportModeMimicryKiro,
		TransportModeMimicryWindsurf,
	}
	for _, m := range mimicryModes {
		if !m.isMimicry() {
			t.Errorf("%s 应判为 mimicry", m)
		}
	}
	for _, m := range []TransportMode{TransportModeStandard, TransportModeDiagnosticsOnly, TransportMode("unknown")} {
		if m.isMimicry() {
			t.Errorf("%s 不应判为 mimicry", m)
		}
	}
}

// TestForMimicryDisabledFallsBackToStandard 是本切片的核心判别测试:
// 同一个 mimicry mode,开关默认开时返回 uTLS RT(非 *http.Transport),
// 显式关闭后降级为标准 *http.Transport。
// 变异验证:删 For 里的降级 guard,关闭分支仍返回 uTLS RT,!ok 断言转红。
func TestForMimicryDisabledFallsBackToStandard(t *testing.T) {
	// mimicry 默认 fail-closed(无 template registry 注入),opt-in Phase A 回退
	// 让默认开分支能真正构造出 uTLS RT,作为关闭分支的对照。
	t.Setenv("HUAKAI_TRANSPORT_PHASE_A_FALLBACK", "true")
	f := NewFactory()

	// 默认开:mimicry mode 返回 uTLS RT,不暴露 *http.Transport。
	rtOn, err := f.For(ProviderAnthropic, TransportModeMimicryClaudeCode)
	if err != nil {
		t.Fatalf("默认开 For 失败: %v", err)
	}
	if _, ok := rtOn.(*http.Transport); ok {
		t.Fatal("默认开:mimicry mode 不应返回 *http.Transport(应是 uTLS RT)")
	}

	// 显式关闭:同一 mimicry mode 降级为标准 *http.Transport。
	t.Setenv(mimicryEnvVar, "false")
	rtOff, err := f.For(ProviderAnthropic, TransportModeMimicryClaudeCode)
	if err != nil {
		t.Fatalf("关闭 For 失败: %v", err)
	}
	if _, ok := rtOff.(*http.Transport); !ok {
		t.Fatalf("关闭:mimicry mode 应降级为 *http.Transport,got %T", rtOff)
	}
}

// TestForStandardModeUnaffectedByMimicrySwitch 确认开关只动 mimicry mode,
// standard mode 行为始终不变(关闭开关不应误伤本就标准的路径)。
func TestForStandardModeUnaffectedByMimicrySwitch(t *testing.T) {
	t.Setenv(mimicryEnvVar, "false")
	f := NewFactory()
	rt, err := f.For(ProviderOpenAI, TransportModeStandard)
	if err != nil {
		t.Fatalf("standard For 失败: %v", err)
	}
	if _, ok := rt.(*http.Transport); !ok {
		t.Fatalf("standard mode 应始终是 *http.Transport,got %T", rt)
	}
}
