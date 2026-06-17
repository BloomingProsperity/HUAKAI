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
	adapter := &anthropic.Adapter{}
	env, losses, err := adapter.ProviderResponseToCanonical(context.Background(), []byte(`{
		"id":"msg_empty",
		"type":"message",
		"role":"assistant",
		"model":"claude-3-5-sonnet",
		"content":[],
		"stop_reason":"end_turn",
		"usage":{"input_tokens":3,"output_tokens":0}
	}`))
	if err != nil {
		t.Fatalf("ProviderResponseToCanonical: %v", err)
	}
	if env == nil || env.BufferedResponse == nil {
		t.Fatal("expected buffered_response envelope")
	}
	if len(env.BufferedResponse.Content) != 0 {
		t.Fatalf("content len=%d want 0", len(env.BufferedResponse.Content))
	}
	if env.BufferedResponse.ID != "msg_empty" || env.BufferedResponse.Model != "claude-3-5-sonnet" {
		t.Fatalf("metadata not preserved: %+v", env.BufferedResponse)
	}
	if env.BufferedResponse.Usage.InputTokens != 3 || env.BufferedResponse.StopReason != proto.CanonicalStopEndTurn {
		t.Fatalf("usage/stop lost on empty content: %+v", env.BufferedResponse)
	}
	if len(losses) == 0 {
		t.Fatal("empty content must emit a non-silent loss entry")
	}
	for _, loss := range losses {
		if loss.IsSilentDrop() {
			t.Fatalf("silent loss entry: %+v", loss)
		}
	}
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
	adapter := &anthropic.Adapter{}
	env, losses, err := adapter.ProviderResponseToCanonical(context.Background(), []byte(`{
		"id":"msg_mix",
		"type":"message",
		"role":"assistant",
		"model":"claude-3-5-sonnet",
		"content":[
			{"type":"text","text":"checking"},
			{"type":"tool_use","id":"toolu_abc123","name":"lookup","input":{"q":"x"}}
		],
		"stop_reason":"tool_use",
		"usage":{"input_tokens":7,"output_tokens":5}
	}`))
	if err != nil {
		t.Fatalf("ProviderResponseToCanonical: %v", err)
	}
	if len(losses) != 0 {
		t.Fatalf("unexpected losses: %+v", losses)
	}
	resp := env.BufferedResponse
	if resp == nil {
		t.Fatal("expected buffered_response")
	}
	if resp.StopReason != proto.CanonicalStopToolUse {
		t.Fatalf("stop_reason=%q want tool_use", resp.StopReason)
	}
	if len(resp.Content) != 2 {
		t.Fatalf("content len=%d want 2: %+v", len(resp.Content), resp.Content)
	}
	if resp.Content[0].Type != "text" || resp.Content[0].Text != "checking" {
		t.Fatalf("text block mismatch: %+v", resp.Content[0])
	}
	tool := resp.Content[1]
	if tool.Type != "tool_use" || tool.CallID != "call_abc123" || tool.Name != "lookup" {
		t.Fatalf("tool block mismatch: %+v", tool)
	}
	if string(tool.Input) != `{"q":"x"}` {
		t.Fatalf("tool input=%s want object", tool.Input)
	}
}

func TestAT_PROTO_002_11_StreamBufferedUsageEquivalence(t *testing.T) {
	adapter := &anthropic.Adapter{}
	env, losses, err := adapter.ProviderResponseToCanonical(context.Background(), []byte(`{
		"id":"msg_cache",
		"type":"message",
		"role":"assistant",
		"model":"claude-3-5-sonnet",
		"content":[{"type":"text","text":"ok"}],
		"stop_reason":"stop_sequence",
		"stop_sequence":"\n\nHuman:",
		"usage":{
			"input_tokens":11,
			"output_tokens":4,
			"cache_read_input_tokens":6,
			"cache_creation_input_tokens":9,
			"cache_creation":{
				"ephemeral_5m_input_tokens":2,
				"ephemeral_1h_input_tokens":7
			}
		}
	}`))
	if err != nil {
		t.Fatalf("ProviderResponseToCanonical: %v", err)
	}
	if len(losses) != 0 {
		t.Fatalf("unexpected losses: %+v", losses)
	}
	resp := env.BufferedResponse
	if resp.Usage.InputTokens != 11 || resp.Usage.OutputTokens != 4 ||
		resp.Usage.CacheReadInputTokens != 6 || resp.Usage.CacheCreationInputTokens != 9 {
		t.Fatalf("usage mismatch: %+v", resp.Usage)
	}
	if resp.StopReason != proto.CanonicalStopSequence {
		t.Fatalf("stop_reason=%q want stop_sequence", resp.StopReason)
	}
	respJSON, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal response: %v", err)
	}
	var respMap map[string]any
	if err := json.Unmarshal(respJSON, &respMap); err != nil {
		t.Fatalf("unmarshal response map: %v", err)
	}
	if respMap["stop_sequence"] != "\n\nHuman:" {
		t.Fatalf("stop_sequence not preserved in canonical JSON: %s", respJSON)
	}
	usageJSON, err := json.Marshal(resp.Usage)
	if err != nil {
		t.Fatalf("marshal usage: %v", err)
	}
	var usageMap map[string]any
	if err := json.Unmarshal(usageJSON, &usageMap); err != nil {
		t.Fatalf("unmarshal usage map: %v", err)
	}
	if usageMap["cache_creation_input_tokens_5m"] != float64(2) ||
		usageMap["cache_creation_input_tokens_1h"] != float64(7) {
		t.Fatalf("cache TTL usage not preserved: %s", usageJSON)
	}

	clientBody, clientLosses, err := (&proto.AnthropicMessagesClient{}).CanonicalToClientResponse(context.Background(), env)
	if err != nil {
		t.Fatalf("CanonicalToClientResponse: %v", err)
	}
	if len(clientLosses) != 0 {
		t.Fatalf("unexpected client losses: %+v", clientLosses)
	}
	var clientResp map[string]any
	if err := json.Unmarshal(clientBody, &clientResp); err != nil {
		t.Fatalf("unmarshal client response: %v", err)
	}
	clientUsage, ok := clientResp["usage"].(map[string]any)
	if !ok {
		t.Fatalf("client usage missing or wrong type: %s", clientBody)
	}
	if clientUsage["cache_creation_input_tokens"] != float64(9) ||
		clientUsage["cache_read_input_tokens"] != float64(6) {
		t.Fatalf("client aggregate cache usage mismatch: %+v", clientUsage)
	}
	cacheCreation, ok := clientUsage["cache_creation"].(map[string]any)
	if !ok {
		t.Fatalf("client cache_creation breakdown missing: %s", clientBody)
	}
	if cacheCreation["ephemeral_5m_input_tokens"] != float64(2) ||
		cacheCreation["ephemeral_1h_input_tokens"] != float64(7) {
		t.Fatalf("client cache_creation breakdown mismatch: %+v", cacheCreation)
	}
}

func TestAT_PROTO_002_11_ClientResponseOmitsCacheCreationBreakdownWhenAbsent(t *testing.T) {
	adapter := &anthropic.Adapter{}
	env, losses, err := adapter.ProviderResponseToCanonical(context.Background(), []byte(`{
		"id":"msg_cache_aggregate",
		"type":"message",
		"role":"assistant",
		"model":"claude-3-5-sonnet",
		"content":[{"type":"text","text":"ok"}],
		"stop_reason":"end_turn",
		"usage":{
			"input_tokens":11,
			"output_tokens":4,
			"cache_read_input_tokens":6,
			"cache_creation_input_tokens":9
		}
	}`))
	if err != nil {
		t.Fatalf("ProviderResponseToCanonical: %v", err)
	}
	if len(losses) != 0 {
		t.Fatalf("unexpected losses: %+v", losses)
	}

	clientBody, clientLosses, err := (&proto.AnthropicMessagesClient{}).CanonicalToClientResponse(context.Background(), env)
	if err != nil {
		t.Fatalf("CanonicalToClientResponse: %v", err)
	}
	if len(clientLosses) != 0 {
		t.Fatalf("unexpected client losses: %+v", clientLosses)
	}
	var clientResp map[string]any
	if err := json.Unmarshal(clientBody, &clientResp); err != nil {
		t.Fatalf("unmarshal client response: %v", err)
	}
	clientUsage, ok := clientResp["usage"].(map[string]any)
	if !ok {
		t.Fatalf("client usage missing or wrong type: %s", clientBody)
	}
	if clientUsage["cache_creation_input_tokens"] != float64(9) ||
		clientUsage["cache_read_input_tokens"] != float64(6) {
		t.Fatalf("client aggregate cache usage mismatch: %+v", clientUsage)
	}
	if _, ok := clientUsage["cache_creation"]; ok {
		t.Fatalf("client cache_creation breakdown should be omitted when absent: %s", clientBody)
	}
}

func TestAnthropicProviderResponseToCanonical_BufferedTextOnly(t *testing.T) {
	adapter := &anthropic.Adapter{}
	env, losses, err := adapter.ProviderResponseToCanonical(context.Background(), []byte(`{
		"id":"msg_text",
		"type":"message",
		"role":"assistant",
		"model":"claude-3-5-haiku",
		"content":[{"type":"text","text":"hello"}],
		"stop_reason":"end_turn",
		"usage":{"input_tokens":5,"output_tokens":2}
	}`))
	if err != nil {
		t.Fatalf("ProviderResponseToCanonical: %v", err)
	}
	if len(losses) != 0 {
		t.Fatalf("unexpected losses: %+v", losses)
	}
	if env == nil || env.Version != proto.HCSFVersion || env.BufferedResponse == nil {
		t.Fatalf("envelope = %+v", env)
	}
	resp := env.BufferedResponse
	if resp.ID != "msg_text" || resp.Model != "claude-3-5-haiku" {
		t.Fatalf("metadata mismatch: %+v", resp)
	}
	if len(resp.Content) != 1 || resp.Content[0].Type != "text" || resp.Content[0].Text != "hello" {
		t.Fatalf("content mismatch: %+v", resp.Content)
	}
	if len(resp.Content[0].Raw) != 0 {
		t.Fatalf("plain text block should not carry raw passthrough: %s", resp.Content[0].Raw)
	}
	if resp.Usage.InputTokens != 5 || resp.Usage.OutputTokens != 2 || resp.Usage.TotalTokens != 7 {
		t.Fatalf("usage mismatch: %+v", resp.Usage)
	}
	if env.Accounting.Usage != resp.Usage {
		t.Fatalf("accounting usage=%+v want %+v", env.Accounting.Usage, resp.Usage)
	}

	clientBody, clientLosses, err := (&proto.AnthropicMessagesClient{}).CanonicalToClientResponse(context.Background(), env)
	if err != nil {
		t.Fatalf("CanonicalToClientResponse: %v", err)
	}
	if len(clientLosses) != 0 {
		t.Fatalf("unexpected client losses: %+v", clientLosses)
	}
	var clientResp map[string]any
	if err := json.Unmarshal(clientBody, &clientResp); err != nil {
		t.Fatalf("unmarshal client response: %v", err)
	}
	clientContent := clientResp["content"].([]any)
	clientText := clientContent[0].(map[string]any)
	if clientText["type"] != "text" || clientText["text"] != "hello" || len(clientText) != 2 {
		t.Fatalf("plain text client block should stay type+text only, got %+v", clientText)
	}
}

func TestAnthropicBufferedTopLevelPassthroughRoundTripToClientResponse(t *testing.T) {
	adapter := &anthropic.Adapter{}
	env, losses, err := adapter.ProviderResponseToCanonical(context.Background(), []byte(`{
		"id":"msg_vendor_top",
		"type":"message",
		"role":"assistant",
		"model":"claude-3-5-haiku",
		"content":[{"type":"text","text":"ok"}],
		"stop_reason":"end_turn",
		"usage":{"input_tokens":5,"output_tokens":2},
		"service_tier":"standard",
		"vendor_trace":{"region":"iad1","attempt":2}
	}`))
	if err != nil {
		t.Fatalf("ProviderResponseToCanonical: %v", err)
	}
	if len(losses) != 0 {
		t.Fatalf("unexpected losses: %+v", losses)
	}
	resp := env.BufferedResponse
	if resp == nil || resp.Passthrough == nil {
		t.Fatal("buffered top-level vendor fields must be captured in Passthrough")
	}
	for _, key := range []string{"service_tier", "vendor_trace"} {
		if _, ok := resp.Passthrough.Extra[key]; !ok {
			t.Fatalf("Passthrough.Extra missing %q: %+v", key, resp.Passthrough)
		}
	}

	clientBody, clientLosses, err := (&proto.AnthropicMessagesClient{}).CanonicalToClientResponse(context.Background(), env)
	if err != nil {
		t.Fatalf("CanonicalToClientResponse: %v", err)
	}
	if len(clientLosses) != 0 {
		t.Fatalf("unexpected client losses: %+v", clientLosses)
	}
	var clientResp map[string]any
	if err := json.Unmarshal(clientBody, &clientResp); err != nil {
		t.Fatalf("unmarshal client response: %v", err)
	}
	if clientResp["service_tier"] != "standard" {
		t.Fatalf("client response lost service_tier: %s", clientBody)
	}
	trace, ok := clientResp["vendor_trace"].(map[string]any)
	if !ok || trace["region"] != "iad1" || trace["attempt"] != float64(2) {
		t.Fatalf("client response lost vendor_trace: %s", clientBody)
	}
}

func TestAnthropicBufferedTopLevelPassthroughAbsentKeepsClientShape(t *testing.T) {
	adapter := &anthropic.Adapter{}
	env, losses, err := adapter.ProviderResponseToCanonical(context.Background(), []byte(`{
		"id":"msg_no_vendor_top",
		"type":"message",
		"role":"assistant",
		"model":"claude-3-5-haiku",
		"content":[{"type":"text","text":"ok"}],
		"stop_reason":"end_turn",
		"usage":{"input_tokens":5,"output_tokens":2}
	}`))
	if err != nil {
		t.Fatalf("ProviderResponseToCanonical: %v", err)
	}
	if len(losses) != 0 {
		t.Fatalf("unexpected losses: %+v", losses)
	}
	if env.BufferedResponse == nil || env.BufferedResponse.Passthrough != nil {
		t.Fatalf("response without vendor extras should keep nil Passthrough, got %+v", env.BufferedResponse)
	}

	clientBody, clientLosses, err := (&proto.AnthropicMessagesClient{}).CanonicalToClientResponse(context.Background(), env)
	if err != nil {
		t.Fatalf("CanonicalToClientResponse: %v", err)
	}
	if len(clientLosses) != 0 {
		t.Fatalf("unexpected client losses: %+v", clientLosses)
	}
	var clientResp map[string]any
	if err := json.Unmarshal(clientBody, &clientResp); err != nil {
		t.Fatalf("unmarshal client response: %v", err)
	}
	wantKeys := map[string]bool{
		"id": true, "type": true, "role": true, "content": true,
		"model": true, "stop_reason": true, "stop_sequence": true, "usage": true,
	}
	if len(clientResp) != len(wantKeys) {
		t.Fatalf("client response top-level shape changed: %s", clientBody)
	}
	for key := range clientResp {
		if !wantKeys[key] {
			t.Fatalf("unexpected top-level key %q in client response: %s", key, clientBody)
		}
	}
	if _, ok := clientResp["service_tier"]; ok {
		t.Fatalf("response without vendor extras should not gain service_tier: %s", clientBody)
	}
}

func TestAnthropicBufferedTopLevelPassthroughTypedFieldsWinOnConflict(t *testing.T) {
	adapter := &anthropic.Adapter{}
	env, losses, err := adapter.ProviderResponseToCanonical(context.Background(), []byte(`{
		"id":"msg_typed_wins",
		"type":"message",
		"role":"assistant",
		"model":"claude-3-5-haiku",
		"content":[{"type":"text","text":"ok"}],
		"stop_reason":"end_turn",
		"usage":{"input_tokens":5,"output_tokens":2},
		"service_tier":"standard"
	}`))
	if err != nil {
		t.Fatalf("ProviderResponseToCanonical: %v", err)
	}
	if len(losses) != 0 {
		t.Fatalf("unexpected losses: %+v", losses)
	}
	if env.BufferedResponse == nil || env.BufferedResponse.Passthrough == nil {
		t.Fatal("buffered response should carry service_tier passthrough")
	}
	env.BufferedResponse.Passthrough.Extra["model"] = json.RawMessage(`"vendor_model_should_not_win"`)
	env.BufferedResponse.Passthrough.Extra["id"] = json.RawMessage(`"vendor_id_should_not_win"`)

	clientBody, clientLosses, err := (&proto.AnthropicMessagesClient{}).CanonicalToClientResponse(context.Background(), env)
	if err != nil {
		t.Fatalf("CanonicalToClientResponse: %v", err)
	}
	if len(clientLosses) != 0 {
		t.Fatalf("unexpected client losses: %+v", clientLosses)
	}
	var clientResp map[string]any
	if err := json.Unmarshal(clientBody, &clientResp); err != nil {
		t.Fatalf("unmarshal client response: %v", err)
	}
	if clientResp["service_tier"] != "standard" {
		t.Fatalf("non-conflicting vendor field should still be merged: %s", clientBody)
	}
	if clientResp["model"] != "claude-3-5-haiku" {
		t.Fatalf("typed model must win over passthrough conflict: %s", clientBody)
	}
	if clientResp["id"] != "msg_typed_wins" {
		t.Fatalf("typed id must win over passthrough conflict: %s", clientBody)
	}
}

func TestAnthropicProviderResponseToCanonical_BufferedTextExtraFieldsPreservedAsRaw(t *testing.T) {
	adapter := &anthropic.Adapter{}
	env, losses, err := adapter.ProviderResponseToCanonical(context.Background(), []byte(`{
		"id":"msg_text_citations",
		"type":"message",
		"role":"assistant",
		"model":"claude-3-5-haiku",
		"content":[{
			"type":"text",
			"text":"answer",
			"citations":[{"type":"char_location","start_char_index":0,"end_char_index":6}],
			"beta_meta":{"trace":"kept"}
		}],
		"stop_reason":"end_turn",
		"usage":{"input_tokens":5,"output_tokens":2}
	}`))
	if err != nil {
		t.Fatalf("ProviderResponseToCanonical: %v", err)
	}
	if len(losses) != 1 {
		t.Fatalf("expected one preservation loss, got %+v", losses)
	}
	if losses[0].Feature != string(proto.FeatureTextStreaming) || losses[0].IsSilentDrop() || !strings.Contains(losses[0].Note, "raw") {
		t.Fatalf("loss should explain raw preservation for text extras, got %+v", losses[0])
	}
	if env == nil || env.BufferedResponse == nil || len(env.BufferedResponse.Content) != 1 {
		t.Fatalf("envelope content mismatch: %+v", env)
	}
	textBlock := env.BufferedResponse.Content[0]
	if textBlock.Type != "text" || textBlock.Text != "answer" {
		t.Fatalf("canonical text mismatch: %+v", textBlock)
	}
	if len(textBlock.Raw) == 0 {
		t.Fatalf("text block with extra fields must keep raw JSON")
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(textBlock.Raw, &raw); err != nil {
		t.Fatalf("unmarshal raw text block: %v", err)
	}
	if len(raw["citations"]) == 0 || len(raw["beta_meta"]) == 0 {
		t.Fatalf("raw text extras not preserved: %s", textBlock.Raw)
	}
}

func TestAnthropicBufferedTextExtraFieldsRoundTripToClientResponse(t *testing.T) {
	adapter := &anthropic.Adapter{}
	env, _, err := adapter.ProviderResponseToCanonical(context.Background(), []byte(`{
		"id":"msg_text_citations",
		"type":"message",
		"role":"assistant",
		"model":"claude-3-5-haiku",
		"content":[{
			"type":"text",
			"text":"answer",
			"citations":[{"type":"char_location","start_char_index":0,"end_char_index":6}],
			"beta_meta":{"trace":"kept"}
		}],
		"stop_reason":"end_turn",
		"usage":{"input_tokens":5,"output_tokens":2}
	}`))
	if err != nil {
		t.Fatalf("ProviderResponseToCanonical: %v", err)
	}
	clientBody, clientLosses, err := (&proto.AnthropicMessagesClient{}).CanonicalToClientResponse(context.Background(), env)
	if err != nil {
		t.Fatalf("CanonicalToClientResponse: %v", err)
	}
	if len(clientLosses) != 0 {
		t.Fatalf("unexpected client losses: %+v", clientLosses)
	}
	var clientResp map[string]any
	if err := json.Unmarshal(clientBody, &clientResp); err != nil {
		t.Fatalf("unmarshal client response: %v", err)
	}
	clientContent := clientResp["content"].([]any)
	clientText := clientContent[0].(map[string]any)
	if clientText["type"] != "text" || clientText["text"] != "answer" {
		t.Fatalf("client text block mismatch: %+v", clientText)
	}
	if _, ok := clientText["citations"]; !ok {
		t.Fatalf("client text block lost citations: %s", clientBody)
	}
	beta, ok := clientText["beta_meta"].(map[string]any)
	if !ok || beta["trace"] != "kept" {
		t.Fatalf("client text block lost beta metadata: %s", clientBody)
	}
}

func TestAnthropicBufferedUnknownBlockRoundTripToClientResponse(t *testing.T) {
	adapter := &anthropic.Adapter{}
	env, losses, err := adapter.ProviderResponseToCanonical(context.Background(), []byte(`{
		"id":"msg_future_block",
		"type":"message",
		"role":"assistant",
		"model":"claude-3-5-haiku",
		"content":[{
			"type":"future_result",
			"payload":{"answer":42},
			"beta_meta":{"trace":"kept"}
		}],
		"stop_reason":"end_turn",
		"usage":{"input_tokens":5,"output_tokens":2}
	}`))
	if err != nil {
		t.Fatalf("ProviderResponseToCanonical: %v", err)
	}
	if len(losses) != 1 || losses[0].IsSilentDrop() || !strings.Contains(losses[0].Note, "raw") {
		t.Fatalf("unknown block should emit one non-silent raw-preservation loss, got %+v", losses)
	}
	if env == nil || env.BufferedResponse == nil || len(env.BufferedResponse.Content) != 1 {
		t.Fatalf("envelope content mismatch: %+v", env)
	}
	futureBlock := env.BufferedResponse.Content[0]
	if futureBlock.Type != "future_result" || len(futureBlock.Raw) == 0 {
		t.Fatalf("canonical unknown block should keep type and raw JSON, got %+v", futureBlock)
	}

	clientBody, clientLosses, err := (&proto.AnthropicMessagesClient{}).CanonicalToClientResponse(context.Background(), env)
	if err != nil {
		t.Fatalf("CanonicalToClientResponse: %v", err)
	}
	if len(clientLosses) != 0 {
		t.Fatalf("raw-preserved unknown block should not emit client-side drop loss, got %+v", clientLosses)
	}
	var clientResp map[string]any
	if err := json.Unmarshal(clientBody, &clientResp); err != nil {
		t.Fatalf("unmarshal client response: %v", err)
	}
	clientContent := clientResp["content"].([]any)
	if len(clientContent) != 1 {
		t.Fatalf("client content should include unknown block, got len=%d body=%s", len(clientContent), clientBody)
	}
	clientBlock := clientContent[0].(map[string]any)
	if clientBlock["type"] != "future_result" {
		t.Fatalf("client unknown block type mismatch: %+v", clientBlock)
	}
	payload, ok := clientBlock["payload"].(map[string]any)
	if !ok || payload["answer"].(float64) != 42 {
		t.Fatalf("client unknown block lost payload: %s", clientBody)
	}
	beta, ok := clientBlock["beta_meta"].(map[string]any)
	if !ok || beta["trace"] != "kept" {
		t.Fatalf("client unknown block lost beta metadata: %s", clientBody)
	}
}

func TestAnthropicProviderResponseToCanonical_ToolOnlyFallbacks(t *testing.T) {
	adapter := &anthropic.Adapter{}
	env, losses, err := adapter.ProviderResponseToCanonical(context.Background(), []byte(`{
		"id":"msg_tool",
		"type":"message",
		"role":"assistant",
		"model":"claude-3-5-sonnet",
		"content":[{"type":"tool_use","name":"lookup","input":["not","object"]}],
		"stop_reason":"tool_use",
		"usage":{"input_tokens":8,"output_tokens":3}
	}`))
	if err != nil {
		t.Fatalf("ProviderResponseToCanonical: %v", err)
	}
	if len(losses) < 2 {
		t.Fatalf("missing tool id and non-object input must both emit losses, got %+v", losses)
	}
	for _, loss := range losses {
		if loss.IsSilentDrop() {
			t.Fatalf("silent loss entry: %+v", loss)
		}
	}
	resp := env.BufferedResponse
	if len(resp.Content) != 1 {
		t.Fatalf("content len=%d want 1", len(resp.Content))
	}
	tool := resp.Content[0]
	if tool.Type != "tool_use" || tool.Name != "lookup" || !strings.HasPrefix(tool.CallID, "call_") {
		t.Fatalf("tool block mismatch: %+v", tool)
	}
	if string(tool.Input) != `{}` {
		t.Fatalf("non-object tool input must normalize to empty object, got %s", tool.Input)
	}
}

func TestAnthropicProviderResponseToCanonical_ThinkingAndRedactedPreserved(t *testing.T) {
	adapter := &anthropic.Adapter{}
	env, losses, err := adapter.ProviderResponseToCanonical(context.Background(), []byte(`{
		"id":"msg_thinking",
		"type":"message",
		"role":"assistant",
		"model":"claude-3-7-sonnet",
		"content":[
			{"type":"thinking","thinking":"internal trace","signature":"sig_abc"},
			{"type":"redacted_thinking","data":"opaque-redacted"}
		],
		"stop_reason":"end_turn",
		"usage":{"input_tokens":10,"output_tokens":6}
	}`))
	if err != nil {
		t.Fatalf("ProviderResponseToCanonical: %v", err)
	}
	if len(losses) != 0 {
		t.Fatalf("preserved thinking blocks should not emit loss, got %+v", losses)
	}
	resp := env.BufferedResponse
	if len(resp.Content) != 2 {
		t.Fatalf("content len=%d want 2: %+v", len(resp.Content), resp.Content)
	}
	body, err := json.Marshal(resp.Content)
	if err != nil {
		t.Fatalf("marshal content: %v", err)
	}
	var blocks []map[string]any
	if err := json.Unmarshal(body, &blocks); err != nil {
		t.Fatalf("unmarshal content map: %v", err)
	}
	if blocks[0]["type"] != "thinking" || blocks[0]["thinking"] != "internal trace" || blocks[0]["signature"] != "sig_abc" {
		t.Fatalf("thinking block not preserved: %s", body)
	}
	if blocks[1]["type"] != "redacted_thinking" || blocks[1]["data"] != "opaque-redacted" {
		t.Fatalf("redacted_thinking block not preserved: %s", body)
	}
}

func TestAnthropicProviderResponseToCanonical_BadBodiesReturnErrors(t *testing.T) {
	adapter := &anthropic.Adapter{}
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{name: "empty body", raw: "", want: "empty"},
		{name: "bad json", raw: "{not-json", want: "json"},
		{name: "wrong top level type", raw: `{"id":"x","type":"not_message","content":[],"usage":{"input_tokens":1,"output_tokens":1}}`, want: "message"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env, _, err := adapter.ProviderResponseToCanonical(context.Background(), []byte(tc.raw))
			if err == nil {
				t.Fatalf("expected error, got env=%+v", env)
			}
			if !strings.Contains(strings.ToLower(err.Error()), tc.want) {
				t.Fatalf("error=%v want substring %q", err, tc.want)
			}
		})
	}
}

func TestAnthropicProviderResponseToCanonical_MissingUsageAndUnknownBlocksEmitLoss(t *testing.T) {
	adapter := &anthropic.Adapter{}
	env, losses, err := adapter.ProviderResponseToCanonical(context.Background(), []byte(`{
		"id":"msg_loss",
		"type":"message",
		"role":"assistant",
		"model":"claude-3-5-sonnet",
		"content":[{},{"type":"future_block","payload":{"x":1}}],
		"stop_reason":"mystery_stop"
	}`))
	if err != nil {
		t.Fatalf("ProviderResponseToCanonical: %v", err)
	}
	if env == nil || env.BufferedResponse == nil {
		t.Fatal("expected buffered_response despite lossy optional fields")
	}
	if len(env.BufferedResponse.Content) != 2 {
		t.Fatalf("lossy blocks must not be silently dropped: %+v", env.BufferedResponse.Content)
	}
	if env.BufferedResponse.Content[0].Type != "empty" || env.BufferedResponse.Content[1].Type != "future_block" {
		t.Fatalf("unexpected lossy block policy: %+v", env.BufferedResponse.Content)
	}
	if env.BufferedResponse.StopReason != proto.CanonicalStopUnknown {
		t.Fatalf("stop_reason=%q want unknown", env.BufferedResponse.StopReason)
	}
	if len(losses) < 3 {
		t.Fatalf("usage missing, empty block, unknown block, and unknown stop should emit losses: %+v", losses)
	}
	for _, loss := range losses {
		if loss.IsSilentDrop() {
			t.Fatalf("silent loss entry: %+v", loss)
		}
	}
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

// TestAnthropicStreamingToolUseMalformedIDPreserved 守 S2(同 S1-1 族的 anthropic streaming 兄弟):
// streaming canonicalBlock 在 tool_use id 无法翻译(缺 toolu_ 前缀/畸形)时必须保留成非空 canonical id，
// 而不是丢成空串——空 CallID 会让下游 openai 流硬报错、anthropic 流发出无法关联 tool_result 的 tool_use。
// buffered 路径(anthropicBufferedToolUseBlock)早已合成 fallback，本测试守 streaming 路径与之对齐。
// Mutation: 把 canonicalBlock 的 err 分支退回 `return {Type:"tool_use", Name, Input}`(不带 CallID) →
// CallID 变空 → 此处 RED。
func TestAnthropicStreamingToolUseMalformedIDPreserved(t *testing.T) {
	adapter := &anthropic.Adapter{}
	const malformedToolID = "weirdid99" // 无 toolu_ 前缀 → ToCanonicalCallID 失败
	evts := [][]byte{
		anthroEvt(t, "message_start", map[string]any{"message": map[string]any{"id": "msg_y", "model": "claude-3-5-sonnet"}}),
		anthroEvt(t, "content_block_start", map[string]any{"index": 0, "content_block": map[string]any{"type": "tool_use", "id": malformedToolID, "name": "search"}}),
		anthroEvt(t, "content_block_stop", map[string]any{"index": 0}),
		anthroEvt(t, "message_stop", nil),
	}
	canonical, _ := runStream(t, adapter, evts)
	var toolBlockEvt *proto.CanonicalEvent
	for i := range canonical {
		if canonical[i].Type == "content_block_start" && canonical[i].ContentBlock != nil && canonical[i].ContentBlock.Type == "tool_use" {
			toolBlockEvt = &canonical[i]
			break
		}
	}
	if toolBlockEvt == nil {
		t.Fatalf("tool_use content_block_start missing")
	}
	if toolBlockEvt.ContentBlock.CallID == "" {
		t.Fatalf("malformed anthropic streaming tool_use id was dropped to empty (the S2 sibling defect)")
	}
	if !strings.HasPrefix(toolBlockEvt.ContentBlock.CallID, "call_") {
		t.Fatalf("synthesized id must keep canonical call_ prefix, got %q", toolBlockEvt.ContentBlock.CallID)
	}
}
