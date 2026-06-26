// Package proto 按 docs/specs/protocol-translation.md 测试 F-PROTO-002 共享契约。
package proto

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// AT-PROTO-002-12：tool-call ID 往返双射（扩展版；覆盖多上游）。
func TestAT_PROTO_002_12_ToolCallIDBijection(t *testing.T) {
	type caseItem struct {
		name     string
		upstream UpstreamProtocol
		raw      string
	}

	// 若把校验退回仅 hex，下面这些真实 provider ID 会失败，本测试即红。
	cases := []caseItem{
		{
			name:     "anthropic-real-id",
			upstream: UpstreamProtocolAnthropic,
			raw:      "toolu_011MDRpaZRMRRjtFkJizD6nS",
		},
		{
			name:     "openai-real-id",
			upstream: UpstreamProtocolOpenAI,
			raw:      "call_1zsDThBu0VSK7KuY7eCcJBnq",
		},
		{
			name:     "gemini-real-id",
			upstream: UpstreamProtocolGemini,
			raw:      "func_012ZTYKWD4VqrXGXyE7kEnAK",
		},
		{
			name:     "bedrock-real-id",
			upstream: UpstreamProtocolBedrock,
			raw:      "tool_4SjsMeA6DUHwGKaE87ZojgOF",
		},
	}

	// 非 hex 字母也必须能往返（因真实 ID 含 f 之外的 A-Z / a-z）。
	for _, tc := range []caseItem{
		{
			name:     "lowercase-g",
			upstream: UpstreamProtocolOpenAI,
			raw:      "call_id_with_g_1g2",
		},
		{
			name:     "lowercase-z",
			upstream: UpstreamProtocolAnthropic,
			raw:      "toolu_id_with_z_7z1",
		},
		{
			name:     "uppercase-Z",
			upstream: UpstreamProtocolGemini,
			raw:      "func_id_with_Z_ABCDZ",
		},
		{
			name:     "uppercase-G",
			upstream: UpstreamProtocolBedrock,
			raw:      "tool_id_with_G_G4",
		},
	} {
		cases = append(cases, tc)
	}

	for _, tc := range cases {
		canonical, err := ToCanonicalCallID(tc.raw, tc.upstream)
		if err != nil {
			t.Fatalf("%s (%s): ToCanonicalCallID(%q) err=%v", tc.name, tc.upstream, tc.raw, err)
		}
		if !strings.HasPrefix(canonical, "call_") {
			t.Fatalf("%s (%s): canonical missing call_ prefix: %q", tc.name, tc.upstream, canonical)
		}
		back, err := FromCanonicalCallID(canonical, tc.upstream)
		if err != nil {
			t.Fatalf("%s (%s): FromCanonicalCallID err=%v", tc.name, tc.upstream, err)
		}
		if back != tc.raw {
			t.Fatalf("%s (%s): round-trip mismatch: %q -> %q -> %q", tc.name, tc.upstream, tc.raw, canonical, back)
		}
	}

	if _, err := ToCanonicalCallID("garbage_xyz", UpstreamProtocolAnthropic); !errors.Is(err, ErrToolCallIDTranslationFail) {
		t.Fatalf("malformed prefix must produce ErrToolCallIDTranslationFail; got %v", err)
	}
	for _, tc := range []struct {
		name string
		raw  string
	}{
		{
			name: "empty suffix",
			raw:  "toolu_",
		},
		{
			name: "space in suffix",
			raw:  "toolu_abc de",
		},
		{
			name: "exclamation in suffix",
			raw:  "toolu_abc!de",
		},
		{
			name: "slash in suffix",
			raw:  "toolu_abc/de",
		},
	} {
		if _, err := ToCanonicalCallID(tc.raw, UpstreamProtocolAnthropic); !errors.Is(err, ErrToolCallIDTranslationFail) {
			t.Fatalf("%s must produce ErrToolCallIDTranslationFail; got %v", tc.name, err)
		}
	}
}

// AT-PROTO-002-15：capability 矩阵与现实一致（每个单元格用 property 测试断言）。
func TestAT_PROTO_002_15_CapabilityMatrixWellFormed(t *testing.T) {
	m := DefaultMatrix()
	clients := []ClientProtocol{ClientProtocolOpenAIChat, ClientProtocolOpenAIResponses, ClientProtocolAnthropicMessages}
	upstreams := []UpstreamProtocol{UpstreamProtocolAnthropic, UpstreamProtocolOpenAI, UpstreamProtocolGemini, UpstreamProtocolBedrock, UpstreamProtocolAntigravity}
	for _, c := range clients {
		for _, u := range upstreams {
			for _, f := range allFeatures {
				v := m.Lookup(c, u, f)
				if v != VerdictPreserved && v != VerdictLossy && v != VerdictUnsupported {
					t.Fatalf("matrix cell (%s,%s,%s) has invalid verdict %q", c, u, f, v)
				}
			}
		}
	}
	for _, c := range clients {
		for _, u := range upstreams {
			if v := m.Lookup(c, u, FeatureTextStreaming); v != VerdictPreserved {
				t.Fatalf("text_streaming should be PRESERVED for (%s,%s); got %q", c, u, v)
			}
		}
	}
}

// AT-PROTO-002-16：转换为 LOSSY 时 protocol_loss 字段必须被填充。
func TestAT_PROTO_002_16_ProtocolLossPopulatedOnLossy(t *testing.T) {
	m := DefaultMatrix()
	req := CanonicalRequest{
		Stream: true,
		Messages: []CanonicalMessage{
			{Role: "user", Content: []CanonicalContentBlock{{Type: "reasoning_summary", ReasoningSummary: "x"}}},
		},
	}
	losses, err := m.Validate(req, ClientProtocolAnthropicMessages, UpstreamProtocolOpenAI)
	if err != nil {
		t.Fatalf("LOSSY (not UNSUPPORTED) must NOT error; got %v", err)
	}
	var sawLossy bool
	for _, l := range losses {
		if l.Feature == string(FeatureReasoningSummary) && l.Verdict == VerdictLossy {
			sawLossy = true
		}
	}
	if !sawLossy {
		t.Fatalf("LOSSY reasoning_summary must populate protocol_loss; got %+v", losses)
	}

	req2 := CanonicalRequest{
		Messages: []CanonicalMessage{
			{Role: "user", Content: []CanonicalContentBlock{{Type: "image"}}},
		},
	}
	_, err2 := m.Validate(req2, ClientProtocolOpenAIChat, UpstreamProtocolGemini)
	if !errors.Is(err2, ErrUnsupportedFeature) {
		t.Fatalf("UNSUPPORTED feature must produce ErrUnsupportedFeature; got %v", err2)
	}
}

func TestPackageCompiles(t *testing.T) {
	if time.Now().Year() < 2026 {
		t.Fatalf("clock skew")
	}
}
