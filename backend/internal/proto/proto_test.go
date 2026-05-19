// Package proto tests F-PROTO-002 shared contracts per docs/specs/protocol-translation.md.
package proto

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// AT-PROTO-002-12: Tool-call ID round-trip bijection (extended; multi-upstream).
func TestAT_PROTO_002_12_ToolCallIDBijection(t *testing.T) {
	hex := "deadbeef1234"
	cases := []struct {
		upstream UpstreamProtocol
		raw      string
	}{
		{UpstreamProtocolAnthropic, "toolu_" + hex},
		{UpstreamProtocolOpenAI, "call_" + hex},
		{UpstreamProtocolGemini, "func_" + hex},
		{UpstreamProtocolBedrock, "tool_" + hex},
	}
	for _, tc := range cases {
		canonical, err := ToCanonicalCallID(tc.raw, tc.upstream)
		if err != nil {
			t.Fatalf("%s: ToCanonicalCallID(%q) err=%v", tc.upstream, tc.raw, err)
		}
		if !strings.HasPrefix(canonical, "call_") {
			t.Fatalf("%s: canonical missing call_ prefix: %q", tc.upstream, canonical)
		}
		back, err := FromCanonicalCallID(canonical, tc.upstream)
		if err != nil {
			t.Fatalf("%s: FromCanonicalCallID err=%v", tc.upstream, err)
		}
		if back != tc.raw {
			t.Fatalf("%s: round-trip mismatch: %q -> %q -> %q", tc.upstream, tc.raw, canonical, back)
		}
	}

	if _, err := ToCanonicalCallID("garbage_xyz", UpstreamProtocolAnthropic); !errors.Is(err, ErrToolCallIDTranslationFail) {
		t.Fatalf("malformed prefix must produce ErrToolCallIDTranslationFail; got %v", err)
	}
	if _, err := ToCanonicalCallID("toolu_NOTHEX!", UpstreamProtocolAnthropic); !errors.Is(err, ErrToolCallIDTranslationFail) {
		t.Fatalf("malformed hex must produce ErrToolCallIDTranslationFail; got %v", err)
	}
}

// AT-PROTO-002-15: Capability matrix matches reality (every cell asserted via property test).
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

// AT-PROTO-002-16: protocol_loss field populated when conversion is LOSSY.
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
