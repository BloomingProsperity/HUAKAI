package anthropic_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/proto"
	"github.com/BloomingProsperity/HUAKAI/internal/proto/anthropic"
)

func anthroEvt(t *testing.T, typ string, payload map[string]any) []byte {
	t.Helper()
	if payload == nil {
		payload = map[string]any{}
	}
	payload["type"] = typ
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return raw
}

func runStream(t *testing.T, adapter *anthropic.Adapter, evts [][]byte) ([]proto.CanonicalEvent, []proto.ProtocolLossEntry) {
	t.Helper()
	state := &anthropic.UpstreamState{}
	var canonical []proto.CanonicalEvent
	var allLoss []proto.ProtocolLossEntry
	for _, e := range evts {
		out, loss, err := adapter.ProviderEventToCanonicalEvents(context.Background(), e, state)
		if err != nil && !errors.Is(err, proto.ErrUnknownEventType) {
			t.Fatalf("event: unexpected err %v", err)
		}
		for _, x := range out {
			canonical = append(canonical, x.(proto.CanonicalEvent))
		}
		allLoss = append(allLoss, loss...)
	}
	return canonical, allLoss
}

func TestAT_PROTO_002_01_AnthropicSSEStreamGraceful(t *testing.T) {
	adapter := &anthropic.Adapter{}
	evts := [][]byte{
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

func TestAT_PROTO_002_02_ToolCallInterleavingPreservesIndex(t *testing.T) {
	adapter := &anthropic.Adapter{}
	const upstreamToolID = "toolu_abc123"
	evts := [][]byte{
		anthroEvt(t, "message_start", map[string]any{"message": map[string]any{"id": "msg_x", "model": "claude-3-5-sonnet"}}),
		anthroEvt(t, "content_block_start", map[string]any{"index": 0, "content_block": map[string]any{"type": "text", "text": ""}}),
		anthroEvt(t, "content_block_delta", map[string]any{"index": 0, "delta": map[string]any{"type": "text_delta", "text": "preface"}}),
		anthroEvt(t, "content_block_stop", map[string]any{"index": 0}),
		anthroEvt(t, "content_block_start", map[string]any{"index": 1, "content_block": map[string]any{"type": "tool_use", "id": upstreamToolID, "name": "search"}}),
		anthroEvt(t, "content_block_stop", map[string]any{"index": 1}),
		anthroEvt(t, "message_stop", nil),
	}
	canonical, _ := runStream(t, adapter, evts)
	var toolBlockEvt *proto.CanonicalEvent
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
	roundTripped, err := proto.FromCanonicalCallID(canonicalCallID, proto.UpstreamProtocolAnthropic)
	if err != nil {
		t.Fatalf("FromCanonicalCallID: %v", err)
	}
	if roundTripped != upstreamToolID {
		t.Fatalf("round-trip ID bijection violated: got %q want %q", roundTripped, upstreamToolID)
	}
}

func TestAT_PROTO_002_03_ReasoningPreserved(t *testing.T) {
	adapter := &anthropic.Adapter{}
	evts := [][]byte{
		anthroEvt(t, "message_start", map[string]any{"message": map[string]any{"id": "m", "model": "x"}}),
		anthroEvt(t, "content_block_start", map[string]any{"index": 0, "content_block": map[string]any{"type": "text"}}),
		anthroEvt(t, "content_block_delta", map[string]any{"index": 0, "delta": map[string]any{"type": "thinking_delta", "text": "step-by-step reasoning"}}),
	}
	canonical, _ := runStream(t, adapter, evts)
	var found bool
	for _, e := range canonical {
		if e.Type == "content_block_delta" && e.Delta != nil && e.Delta.Type == "reasoning_delta" && e.Delta.ReasoningText == "step-by-step reasoning" {
			found = true
		}
	}
	if !found {
		t.Fatalf("reasoning_delta not preserved through canonical")
	}
}

func TestAT_PROTO_002_04_SignatureDeltaPolicy(t *testing.T) {
	defaultAdapter := &anthropic.Adapter{}
	carryAdapter := &anthropic.Adapter{CarryForwardSignatureDelta: true}
	evt := anthroEvt(t, "content_block_delta", map[string]any{"index": 0, "delta": map[string]any{"type": "signature_delta", "signature": "sig-xyz"}})

	state1 := &anthropic.UpstreamState{}
	out1, loss1, _ := defaultAdapter.ProviderEventToCanonicalEvents(context.Background(), evt, state1)
	if len(out1) != 0 {
		t.Fatalf("default policy must skip signature_delta; got %d events", len(out1))
	}
	if len(loss1) == 0 || loss1[0].Feature != string(proto.FeatureSignatureDelta) {
		t.Fatalf("default policy must emit signature_delta loss entry; got %+v", loss1)
	}

	state2 := &anthropic.UpstreamState{}
	out2, _, _ := carryAdapter.ProviderEventToCanonicalEvents(context.Background(), evt, state2)
	if len(out2) != 1 {
		t.Fatalf("carry-forward policy must emit signature_delta; got %d events", len(out2))
	}
}

func TestAT_PROTO_002_05_SyntheticTerminalOnEOF(t *testing.T) {
	adapter := &anthropic.Adapter{}
	state := &anthropic.UpstreamState{}
	for _, e := range [][]byte{
		anthroEvt(t, "message_start", map[string]any{"message": map[string]any{"id": "m", "model": "x"}}),
		anthroEvt(t, "content_block_start", map[string]any{"index": 0, "content_block": map[string]any{"type": "text"}}),
		anthroEvt(t, "content_block_delta", map[string]any{"index": 0, "delta": map[string]any{"type": "text_delta", "text": "abc"}}),
	} {
		_, _, _ = adapter.ProviderEventToCanonicalEvents(context.Background(), e, state)
	}
	if state.Terminated {
		t.Fatalf("state should not be terminated before Finalize")
	}
	out, err := adapter.FinalizeUpstreamStream(context.Background(), state)
	if err != nil {
		t.Fatalf("Finalize: %v", err)
	}
	var sawStop, sawBlockStop bool
	for _, x := range out {
		ev := x.(proto.CanonicalEvent)
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

func TestAT_PROTO_002_06_EmptyUpstreamContent(t *testing.T) {
	t.Skip("Buffered ProviderResponseToCanonical Phase 4.5; spec §3 Phase A buffered path not yet implemented.")
}

func TestAT_PROTO_002_07_MaxTokensFinishReason(t *testing.T) {
	adapter := &anthropic.Adapter{}
	evt := anthroEvt(t, "message_delta", map[string]any{"delta": map[string]any{"stop_reason": "max_tokens"}, "usage": map[string]any{"output_tokens": 99}})
	state := &anthropic.UpstreamState{}
	out, _, _ := adapter.ProviderEventToCanonicalEvents(context.Background(), evt, state)
	if len(out) != 1 {
		t.Fatalf("expected 1 canonical event; got %d", len(out))
	}
	ev := out[0].(proto.CanonicalEvent)
	if ev.StopReason != proto.CanonicalStopMaxTokens {
		t.Fatalf("max_tokens stop_reason did not map to CanonicalStopMaxTokens; got %q", ev.StopReason)
	}
}

func TestAT_PROTO_002_08_UnknownEventType(t *testing.T) {
	adapter := &anthropic.Adapter{}
	evt := anthroEvt(t, "ping", nil)
	state := &anthropic.UpstreamState{}
	out, loss, err := adapter.ProviderEventToCanonicalEvents(context.Background(), evt, state)
	if !errors.Is(err, proto.ErrUnknownEventType) {
		t.Fatalf("expected ErrUnknownEventType; got %v", err)
	}
	if len(out) != 0 {
		t.Fatalf("unknown event must NOT emit canonical event; got %d", len(out))
	}
	if len(loss) == 0 {
		t.Fatalf("unknown event must emit protocol_loss entry (NOT silent drop)")
	}
}

func TestAT_PROTO_002_09_UnknownStopReason(t *testing.T) {
	adapter := &anthropic.Adapter{}
	evt := anthroEvt(t, "message_delta", map[string]any{"delta": map[string]any{"stop_reason": "policy_violation_xyz"}})
	state := &anthropic.UpstreamState{}
	out, loss, _ := adapter.ProviderEventToCanonicalEvents(context.Background(), evt, state)
	ev := out[0].(proto.CanonicalEvent)
	if ev.StopReason != proto.CanonicalStopUnknown {
		t.Fatalf("unknown stop_reason MUST map to CanonicalStopUnknown (not auto-end_turn); got %q", ev.StopReason)
	}
	if len(loss) == 0 {
		t.Fatalf("unknown stop_reason must emit protocol_loss")
	}
}

func TestAT_PROTO_002_10_BufferedInterleaving(t *testing.T) {
	t.Skip("Buffered ProviderResponseToCanonical Phase 4.5; spec §3 Phase A buffered path not yet implemented.")
}

func TestAT_PROTO_002_11_StreamBufferedUsageEquivalence(t *testing.T) {
	t.Skip("Buffered path Phase 4.5; equivalence requires both stream+buffered translators.")
}

func TestAT_PROTO_002_13_LengthFinishReasonPreserved(t *testing.T) {
	adapter := &anthropic.Adapter{}
	evt := anthroEvt(t, "message_delta", map[string]any{"delta": map[string]any{"stop_reason": "max_tokens"}})
	state := &anthropic.UpstreamState{}
	out, _, _ := adapter.ProviderEventToCanonicalEvents(context.Background(), evt, state)
	if len(out) != 1 {
		t.Fatalf("expected 1 event")
	}
	ev := out[0].(proto.CanonicalEvent)
	if ev.StopReason != proto.CanonicalStopMaxTokens {
		t.Fatalf("max_tokens MUST preserve as CanonicalStopMaxTokens (Chat client maps to length); got %q", ev.StopReason)
	}
}

func TestAT_PROTO_002_14_TranslationLatencySLO(t *testing.T) {
	adapter := &anthropic.Adapter{}
	evts := [][]byte{
		anthroEvt(t, "message_start", map[string]any{"message": map[string]any{"id": "m", "model": "x"}}),
		anthroEvt(t, "content_block_start", map[string]any{"index": 0, "content_block": map[string]any{"type": "text"}}),
	}
	for i := 0; i < 998; i++ {
		evts = append(evts, anthroEvt(t, "content_block_delta", map[string]any{"index": 0, "delta": map[string]any{"type": "text_delta", "text": "x"}}))
	}
	state := &anthropic.UpstreamState{}
	durations := make([]time.Duration, 0, len(evts))
	for _, e := range evts {
		t0 := time.Now()
		_, _, _ = adapter.ProviderEventToCanonicalEvents(context.Background(), e, state)
		durations = append(durations, time.Since(t0))
	}
	for i := 1; i < len(durations); i++ {
		for j := i; j > 0 && durations[j-1] > durations[j]; j-- {
			durations[j-1], durations[j] = durations[j], durations[j-1]
		}
	}
	p99 := durations[(99*len(durations))/100]
	const slo = 200 * time.Microsecond
	if p99 > slo {
		t.Logf("WARN p99=%v exceeds SLO %v (spec marks as telemetry, not failure)", p99, slo)
	}
	t.Logf("translation latency p99=%v over %d events", p99, len(durations))
}
