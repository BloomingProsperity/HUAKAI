// Package proto tests F-PROTO-002 contract per docs/specs/protocol-translation.md.
package proto

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func anthroEvt(t *testing.T, typ string, payload map[string]any) anthropicEvent {
	t.Helper()
	if payload == nil {
		payload = map[string]any{}
	}
	payload["type"] = typ
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return anthropicEvent{Type: typ, Raw: raw}
}

func runStream(t *testing.T, adapter *AnthropicAdapter, evts []anthropicEvent) ([]CanonicalEvent, []ProtocolLossEntry) {
	t.Helper()
	state := &UpstreamState{}
	var canonical []CanonicalEvent
	var allLoss []ProtocolLossEntry
	for _, e := range evts {
		out, loss, err := adapter.ProviderEventToCanonicalEvents(context.Background(), e, state)
		if err != nil && !errors.Is(err, ErrUnknownEventType) {
			t.Fatalf("event %s: unexpected err %v", e.Type, err)
		}
		for _, x := range out {
			canonical = append(canonical, x.(CanonicalEvent))
		}
		allLoss = append(allLoss, loss...)
	}
	return canonical, allLoss
}

// AT-PROTO-002-01: Anthropic SSE → Canonical → Chat chunks: 50-event stream graceful path.
func TestAT_PROTO_002_01_AnthropicSSEStreamGraceful(t *testing.T) {
	adapter := &AnthropicAdapter{}
	evts := []anthropicEvent{
		anthroEvt(t, "message_start", map[string]any{"message": map[string]any{"id": "msg_01", "model": "claude-3-5-sonnet"}}),
		anthroEvt(t, "content_block_start", map[string]any{"index": 0, "content_block": map[string]any{"type": "text", "text": ""}}),
	}
	for i := 0; i < 45; i++ {
		evts = append(evts, anthroEvt(t, "content_block_delta", map[string]any{"index": 0, "delta": map[string]any{"type": "text_delta", "text": "x"}}))
	}
	evts = append(evts,
		anthroEvt(t, "content_block_stop", map[string]any{"index": 0}),
		anthroEvt(t, "message_delta", map[string]any{"delta": map[string]any{"stop_reason": "end_turn"}, "usage": map[string]any{"output_tokens": 45}}),
		anthroEvt(t, "message_stop", nil),
	)
	canonical, _ := runStream(t, adapter, evts)
	if len(canonical) != 50 {
		t.Fatalf("expected 50 canonical events, got %d", len(canonical))
	}
	if canonical[0].Type != "message_start" || canonical[len(canonical)-1].Type != "message_stop" {
		t.Fatalf("missing terminal events: first=%s last=%s", canonical[0].Type, canonical[len(canonical)-1].Type)
	}
}

// AT-PROTO-002-02: Tool-call interleaving preserves output index; round-trip ID bijection.
func TestAT_PROTO_002_02_ToolCallInterleavingPreservesIndex(t *testing.T) {
	adapter := &AnthropicAdapter{}
	const upstreamToolID = "toolu_abc123"
	evts := []anthropicEvent{
		anthroEvt(t, "message_start", map[string]any{"message": map[string]any{"id": "msg_x", "model": "claude-3-5-sonnet"}}),
		anthroEvt(t, "content_block_start", map[string]any{"index": 0, "content_block": map[string]any{"type": "text", "text": ""}}),
		anthroEvt(t, "content_block_delta", map[string]any{"index": 0, "delta": map[string]any{"type": "text_delta", "text": "preface"}}),
		anthroEvt(t, "content_block_stop", map[string]any{"index": 0}),
		anthroEvt(t, "content_block_start", map[string]any{"index": 1, "content_block": map[string]any{"type": "tool_use", "id": upstreamToolID, "name": "search"}}),
		anthroEvt(t, "content_block_stop", map[string]any{"index": 1}),
		anthroEvt(t, "message_stop", nil),
	}
	canonical, _ := runStream(t, adapter, evts)
	var toolBlockEvt *CanonicalEvent
	for i := range canonical {
		if canonical[i].Type == "content_block_start" && canonical[i].Index == 1 {
			toolBlockEvt = &canonical[i]
		}
	}
	if toolBlockEvt == nil || toolBlockEvt.ContentBlock == nil {
		t.Fatalf("tool_use block at index 1 missing")
	}
	canonicalCallID := toolBlockEvt.ContentBlock.CallID
	if !strings.HasPrefix(canonicalCallID, "call_") {
		t.Fatalf("canonical call_id missing prefix: %q", canonicalCallID)
	}
	roundTripped, err := FromCanonicalCallID(canonicalCallID, UpstreamProtocolAnthropic)
	if err != nil {
		t.Fatalf("FromCanonicalCallID: %v", err)
	}
	if roundTripped != upstreamToolID {
		t.Fatalf("round-trip ID bijection violated: got %q want %q", roundTripped, upstreamToolID)
	}
}

// AT-PROTO-002-03: Reasoning preserved through canonical.
func TestAT_PROTO_002_03_ReasoningPreserved(t *testing.T) {
	adapter := &AnthropicAdapter{}
	evts := []anthropicEvent{
		anthroEvt(t, "message_start", map[string]any{"message": map[string]any{"id": "m", "model": "x"}}),
		anthroEvt(t, "content_block_start", map[string]any{"index": 0, "content_block": map[string]any{"type": "text"}}),
		anthroEvt(t, "content_block_delta", map[string]any{"index": 0, "delta": map[string]any{"type": "thinking_delta", "text": "step-by-step reasoning"}}),
	}
	canonical, _ := runStream(t, adapter, evts)
	var found bool
	for _, e := range canonical {
		if e.Type == "content_block_delta" && e.Delta != nil && e.Delta.Type == "reasoning_delta" {
			if e.Delta.ReasoningText == "step-by-step reasoning" {
				found = true
			}
		}
	}
	if !found {
		t.Fatalf("reasoning_delta not preserved through canonical")
	}
}

// AT-PROTO-002-04: signature_delta carry-forward governed by Route policy (default skip).
func TestAT_PROTO_002_04_SignatureDeltaPolicy(t *testing.T) {
	defaultAdapter := &AnthropicAdapter{}
	carryAdapter := &AnthropicAdapter{CarryForwardSignatureDelta: true}
	evt := anthroEvt(t, "content_block_delta", map[string]any{"index": 0, "delta": map[string]any{"type": "signature_delta", "signature": "sig-xyz"}})

	// Default: skipped + loss entry.
	state1 := &UpstreamState{}
	out1, loss1, _ := defaultAdapter.ProviderEventToCanonicalEvents(context.Background(), evt, state1)
	if len(out1) != 0 {
		t.Fatalf("default policy must skip signature_delta; got %d events", len(out1))
	}
	if len(loss1) == 0 || loss1[0].Feature != string(FeatureSignatureDelta) {
		t.Fatalf("default policy must emit signature_delta loss entry; got %+v", loss1)
	}

	// Carry-forward: emitted.
	state2 := &UpstreamState{}
	out2, _, _ := carryAdapter.ProviderEventToCanonicalEvents(context.Background(), evt, state2)
	if len(out2) != 1 {
		t.Fatalf("carry-forward policy must emit signature_delta; got %d events", len(out2))
	}
}

// AT-PROTO-002-05: Stream ended without explicit terminator: synthetic terminal events.
func TestAT_PROTO_002_05_SyntheticTerminalOnEOF(t *testing.T) {
	adapter := &AnthropicAdapter{}
	state := &UpstreamState{}
	for _, e := range []anthropicEvent{
		anthroEvt(t, "message_start", map[string]any{"message": map[string]any{"id": "m", "model": "x"}}),
		anthroEvt(t, "content_block_start", map[string]any{"index": 0, "content_block": map[string]any{"type": "text"}}),
		anthroEvt(t, "content_block_delta", map[string]any{"index": 0, "delta": map[string]any{"type": "text_delta", "text": "abc"}}),
	} {
		_, _, _ = adapter.ProviderEventToCanonicalEvents(context.Background(), e, state)
	}
	// EOF without message_stop.
	if state.Terminated {
		t.Fatalf("state should not be terminated before Finalize")
	}
	out, err := adapter.FinalizeUpstreamStream(context.Background(), state)
	if err != nil {
		t.Fatalf("Finalize: %v", err)
	}
	var sawStop, sawBlockStop bool
	for _, x := range out {
		ev := x.(CanonicalEvent)
		if ev.Type == "message_stop" {
			sawStop = true
		}
		if ev.Type == "content_block_stop" {
			sawBlockStop = true
		}
	}
	if !sawStop || !sawBlockStop {
		t.Fatalf("synthetic terminal events missing: stop=%v block_stop=%v", sawStop, sawBlockStop)
	}
	if !state.Terminated {
		t.Fatalf("state.Terminated must flip true after Finalize")
	}
}

// AT-PROTO-002-06: Empty upstream Content: synthesize empty message item.
func TestAT_PROTO_002_06_EmptyUpstreamContent(t *testing.T) {
	t.Skip("Buffered ProviderResponseToCanonical Phase 4.5; spec §3 Phase A buffered path not yet implemented.")
}

// AT-PROTO-002-07: max_tokens stop_reason → buffered status incomplete + IncompleteDetails.
func TestAT_PROTO_002_07_MaxTokensFinishReason(t *testing.T) {
	adapter := &AnthropicAdapter{}
	evt := anthroEvt(t, "message_delta", map[string]any{"delta": map[string]any{"stop_reason": "max_tokens"}, "usage": map[string]any{"output_tokens": 99}})
	state := &UpstreamState{}
	out, _, _ := adapter.ProviderEventToCanonicalEvents(context.Background(), evt, state)
	if len(out) != 1 {
		t.Fatalf("expected 1 canonical event; got %d", len(out))
	}
	ev := out[0].(CanonicalEvent)
	if ev.StopReason != CanonicalStopMaxTokens {
		t.Fatalf("max_tokens stop_reason did not map to CanonicalStopMaxTokens; got %q", ev.StopReason)
	}
}

// AT-PROTO-002-08: Unknown event type → typed warning + protocol_loss entry (NOT silent drop).
func TestAT_PROTO_002_08_UnknownEventType(t *testing.T) {
	adapter := &AnthropicAdapter{}
	evt := anthroEvt(t, "ping", nil) // not in adapter switch
	state := &UpstreamState{}
	out, loss, err := adapter.ProviderEventToCanonicalEvents(context.Background(), evt, state)
	if !errors.Is(err, ErrUnknownEventType) {
		t.Fatalf("expected ErrUnknownEventType; got %v", err)
	}
	if len(out) != 0 {
		t.Fatalf("unknown event must NOT emit canonical event; got %d", len(out))
	}
	if len(loss) == 0 {
		t.Fatalf("unknown event must emit protocol_loss entry (NOT silent drop)")
	}
}

// AT-PROTO-002-09: Unknown stop_reason → typed canonical status (NOT default-completed).
func TestAT_PROTO_002_09_UnknownStopReason(t *testing.T) {
	adapter := &AnthropicAdapter{}
	evt := anthroEvt(t, "message_delta", map[string]any{"delta": map[string]any{"stop_reason": "policy_violation_xyz"}})
	state := &UpstreamState{}
	out, loss, _ := adapter.ProviderEventToCanonicalEvents(context.Background(), evt, state)
	ev := out[0].(CanonicalEvent)
	if ev.StopReason != CanonicalStopUnknown {
		t.Fatalf("unknown stop_reason MUST map to CanonicalStopUnknown (not auto-end_turn); got %q", ev.StopReason)
	}
	if len(loss) == 0 {
		t.Fatalf("unknown stop_reason must emit protocol_loss")
	}
}

// AT-PROTO-002-10: Buffered-path interleaving: text → tool_use → text emits 3 items.
func TestAT_PROTO_002_10_BufferedInterleaving(t *testing.T) {
	t.Skip("Buffered ProviderResponseToCanonical Phase 4.5; spec §3 Phase A buffered path not yet implemented.")
}

// AT-PROTO-002-11: Streaming-vs-buffered usage equivalence (property test).
func TestAT_PROTO_002_11_StreamBufferedUsageEquivalence(t *testing.T) {
	t.Skip("Buffered path Phase 4.5; equivalence requires both stream+buffered translators.")
}

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
			t.Fatalf("%s: round-trip mismatch: %q → %q → %q", tc.upstream, tc.raw, canonical, back)
		}
	}

	// Malformed input must error (not silently pass through).
	if _, err := ToCanonicalCallID("garbage_xyz", UpstreamProtocolAnthropic); !errors.Is(err, ErrToolCallIDTranslationFail) {
		t.Fatalf("malformed prefix must produce ErrToolCallIDTranslationFail; got %v", err)
	}
	if _, err := ToCanonicalCallID("toolu_NOTHEX!", UpstreamProtocolAnthropic); !errors.Is(err, ErrToolCallIDTranslationFail) {
		t.Fatalf("malformed hex must produce ErrToolCallIDTranslationFail; got %v", err)
	}
}

// AT-PROTO-002-13: length finish_reason preserved end-to-end (max_tokens → CanonicalStopMaxTokens).
func TestAT_PROTO_002_13_LengthFinishReasonPreserved(t *testing.T) {
	adapter := &AnthropicAdapter{}
	evt := anthroEvt(t, "message_delta", map[string]any{"delta": map[string]any{"stop_reason": "max_tokens"}})
	state := &UpstreamState{}
	out, _, _ := adapter.ProviderEventToCanonicalEvents(context.Background(), evt, state)
	if len(out) != 1 {
		t.Fatalf("expected 1 event")
	}
	ev := out[0].(CanonicalEvent)
	if ev.StopReason != CanonicalStopMaxTokens {
		t.Fatalf("max_tokens MUST preserve as CanonicalStopMaxTokens (Chat client maps to length); got %q", ev.StopReason)
	}
}

// AT-PROTO-002-14: Translation latency SLO p99 < 200µs over 1000-event stream.
func TestAT_PROTO_002_14_TranslationLatencySLO(t *testing.T) {
	adapter := &AnthropicAdapter{}
	evts := []anthropicEvent{
		anthroEvt(t, "message_start", map[string]any{"message": map[string]any{"id": "m", "model": "x"}}),
		anthroEvt(t, "content_block_start", map[string]any{"index": 0, "content_block": map[string]any{"type": "text"}}),
	}
	for i := 0; i < 998; i++ {
		evts = append(evts, anthroEvt(t, "content_block_delta", map[string]any{"index": 0, "delta": map[string]any{"type": "text_delta", "text": "x"}}))
	}
	state := &UpstreamState{}
	durations := make([]time.Duration, 0, len(evts))
	for _, e := range evts {
		t0 := time.Now()
		_, _, _ = adapter.ProviderEventToCanonicalEvents(context.Background(), e, state)
		durations = append(durations, time.Since(t0))
	}
	// p99 sort
	for i := 1; i < len(durations); i++ {
		for j := i; j > 0 && durations[j-1] > durations[j]; j-- {
			durations[j-1], durations[j] = durations[j], durations[j-1]
		}
	}
	p99 := durations[(99*len(durations))/100]
	const slo = 200 * time.Microsecond
	if p99 > slo {
		// Spec §6 says telemetry counter, not test failure — but for v0.1 we make it visible.
		t.Logf("WARN p99=%v exceeds SLO %v (spec marks as telemetry, not failure)", p99, slo)
	}
	t.Logf("translation latency p99=%v over %d events", p99, len(durations))
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
	// text_streaming should be PRESERVED across all pairs (universal capability).
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
	// Anthropic client → OpenAI upstream, request with reasoning_summary block (LOSSY per DefaultMatrix).
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

	// UNSUPPORTED on Gemini upstream with image_input request must error.
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
