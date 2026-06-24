package proto

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// decodeJSON 是 test 内部用的轻包装；避免 client_adapter_common_test 与本文件
// 重复 import 同名 helper。
func decodeJSON(data []byte, out any) error {
	return json.Unmarshal(data, out)
}

// 覆盖 P-2 D1 anthropic_messages.RequestToCanonical 第一片：text + system + 基础控制。

func newTestAnthropicCtx(t *testing.T) context.Context {
	t.Helper()
	return ContextWithRequestMetaSeed(context.Background(), RequestMetaSeed{
		RequestID:      "req_test_d1",
		ClientProtocol: ClientProtocolAnthropicMessages,
		ProtocolFamily: "anthropic",
		IngressPath:    "/v1/messages",
		EvidenceLabel:  EvidenceMock,
	})
}

func TestAnthropicMessagesClient_HappyPath_TextOnly(t *testing.T) {
	adapter := &AnthropicMessagesClient{}
	body := []byte(`{
		"model": "claude-3-5-sonnet-20241022",
		"max_tokens": 1024,
		"messages": [
			{"role": "user", "content": "Hello"}
		]
	}`)
	env, losses, err := adapter.RequestToCanonical(newTestAnthropicCtx(t), body)
	if err != nil {
		t.Fatalf("RequestToCanonical: %v", err)
	}
	if env == nil {
		t.Fatal("nil envelope")
	}
	if env.RequestMeta.Model != "claude-3-5-sonnet-20241022" {
		t.Errorf("Model: got %q", env.RequestMeta.Model)
	}
	if env.RequestMeta.RequestID != "req_test_d1" {
		t.Errorf("RequestID not propagated from seed: got %q", env.RequestMeta.RequestID)
	}
	if env.RequestControls.MaxTokens == nil || *env.RequestControls.MaxTokens != 1024 {
		t.Errorf("MaxTokens: got %v", env.RequestControls.MaxTokens)
	}
	if len(env.Messages) != 1 || len(env.Messages[0].Content) != 1 || env.Messages[0].Content[0].Text != "Hello" {
		t.Errorf("Messages mismatch: %+v", env.Messages)
	}
	if len(env.CapabilityGraph.Nodes) != 1 {
		t.Errorf("expected 1 text node, got %d", len(env.CapabilityGraph.Nodes))
	}
	if env.CapabilityGraph.Nodes[0].Kind != CapabilityText {
		t.Errorf("node kind: %q", env.CapabilityGraph.Nodes[0].Kind)
	}
	if env.StreamPlan.Mode != StreamModeBuffered {
		t.Errorf("Stream default should be buffered: got %q", env.StreamPlan.Mode)
	}
	if len(losses) != 0 {
		t.Errorf("happy path should have no losses, got %d: %+v", len(losses), losses)
	}
}

func TestAnthropicMessagesClient_StreamFlag(t *testing.T) {
	adapter := &AnthropicMessagesClient{}
	body := []byte(`{"model":"claude-3-5-haiku","max_tokens":10,"stream":true,"messages":[{"role":"user","content":"hi"}]}`)
	env, _, err := adapter.RequestToCanonical(newTestAnthropicCtx(t), body)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if env.StreamPlan.Mode != StreamModeStreaming {
		t.Errorf("expected StreamModeStreaming, got %q", env.StreamPlan.Mode)
	}
}

func TestAnthropicMessagesClient_SystemAsString(t *testing.T) {
	adapter := &AnthropicMessagesClient{}
	body := []byte(`{
		"model":"claude-3",
		"max_tokens":10,
		"system":"You are a helpful AI gateway tester.",
		"messages":[{"role":"user","content":"go"}]
	}`)
	env, losses, err := adapter.RequestToCanonical(newTestAnthropicCtx(t), body)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if env.RequestControls.SystemPrompt != "You are a helpful AI gateway tester." {
		t.Errorf("SystemPrompt: %q", env.RequestControls.SystemPrompt)
	}
	// system 应该也 emit 一个 role=system 的 CapabilityText 节点
	var sysNodeFound bool
	for _, n := range env.CapabilityGraph.Nodes {
		if n.Text != nil && n.Text.Role == "system" {
			sysNodeFound = true
			break
		}
	}
	if !sysNodeFound {
		t.Error("expected a system-role CapabilityText node")
	}
	if len(losses) != 0 {
		t.Errorf("string system should have no losses, got %d", len(losses))
	}
}

func TestAnthropicMessagesClient_SystemAsBlockArray_InfoLoss(t *testing.T) {
	adapter := &AnthropicMessagesClient{}
	body := []byte(`{
		"model":"claude-3","max_tokens":10,
		"system":[{"type":"text","text":"part1"},{"type":"text","text":"part2"}],
		"messages":[{"role":"user","content":"go"}]
	}`)
	env, losses, err := adapter.RequestToCanonical(newTestAnthropicCtx(t), body)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if !strings.Contains(env.RequestControls.SystemPrompt, "part1") || !strings.Contains(env.RequestControls.SystemPrompt, "part2") {
		t.Errorf("expected concatenated system, got %q", env.RequestControls.SystemPrompt)
	}
	var foundInfo bool
	for _, l := range losses {
		if l.Severity == ProtocolLossInfo && strings.Contains(l.Reason, "system_block_array_concatenated") {
			foundInfo = true
			break
		}
	}
	if !foundInfo {
		t.Errorf("expected info loss for system block array concatenation")
	}
}

func TestAnthropicMessagesClient_ContentAsBlockArray(t *testing.T) {
	adapter := &AnthropicMessagesClient{}
	body := []byte(`{
		"model":"claude-3","max_tokens":10,
		"messages":[{"role":"user","content":[{"type":"text","text":"first"},{"type":"text","text":"second"}]}]
	}`)
	env, _, err := adapter.RequestToCanonical(newTestAnthropicCtx(t), body)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(env.Messages[0].Content) != 2 {
		t.Errorf("expected 2 text blocks, got %d", len(env.Messages[0].Content))
	}
}

func TestAnthropicMessagesClient_ContentUnsupportedBlockType_NonSilentLoss(t *testing.T) {
	// Image / thinking content blocks 已在 D1.x 升级；此 case 改测真正未识别的 type。
	adapter := &AnthropicMessagesClient{}
	body := []byte(`{
		"model":"claude-3","max_tokens":10,
		"messages":[{"role":"user","content":[{"type":"exotic_future_type"}]}]
	}`)
	_, losses, err := adapter.RequestToCanonical(newTestAnthropicCtx(t), body)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(losses) == 0 {
		t.Fatal("expected at least 1 loss for unknown block type")
	}
	var foundUnknown bool
	for _, l := range losses {
		if l.Severity == "" {
			t.Errorf("loss missing severity (must not be silent): %+v", l)
		}
		if strings.Contains(l.Reason, "unknown_block_type") {
			foundUnknown = true
		}
	}
	if !foundUnknown {
		t.Errorf("expected unknown_block_type loss, got: %+v", losses)
	}
}

func TestAnthropicMessagesClient_ToolsAndToolChoice(t *testing.T) {
	adapter := &AnthropicMessagesClient{}
	body := []byte(`{
		"model":"claude-3","max_tokens":10,
		"tools":[{"name":"get_weather","description":"weather lookup","input_schema":{"type":"object"}}],
		"tool_choice":{"type":"auto"},
		"messages":[{"role":"user","content":"hi"}]
	}`)
	env, _, err := adapter.RequestToCanonical(newTestAnthropicCtx(t), body)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(env.RequestControls.Tools) != 1 || env.RequestControls.Tools[0].Name != "get_weather" {
		t.Errorf("expected 1 tool 'get_weather', got %+v", env.RequestControls.Tools)
	}
	if len(env.RequestControls.ToolChoice) == 0 {
		t.Errorf("expected tool_choice preserved, got empty")
	}
}

func TestAnthropicMessagesClient_ToolUseToolResultChain(t *testing.T) {
	adapter := &AnthropicMessagesClient{}
	body := []byte(`{
		"model":"claude-3","max_tokens":10,
		"messages":[
			{"role":"user","content":"check weather"},
			{"role":"assistant","content":[
				{"type":"text","text":"i'll look it up"},
				{"type":"tool_use","id":"call_abc","name":"get_weather","input":{"city":"SF"}}
			]},
			{"role":"user","content":[
				{"type":"tool_result","tool_use_id":"call_abc","content":"sunny"}
			]}
		]
	}`)
	env, _, err := adapter.RequestToCanonical(newTestAnthropicCtx(t), body)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	// 预期: 1 user text + 1 assistant text + 1 tool_use + 1 tool_result = 4 nodes + 1 requires edge
	gotTU, gotTR := 0, 0
	for _, n := range env.CapabilityGraph.Nodes {
		switch n.Kind {
		case CapabilityToolUse:
			gotTU++
			if n.ToolUse == nil || n.ToolUse.ToolCallID != "call_abc" {
				t.Errorf("tool_use node payload wrong: %+v", n.ToolUse)
			}
		case CapabilityToolResult:
			gotTR++
			if n.ToolResult == nil || n.ToolResult.ToolCallID != "call_abc" {
				t.Errorf("tool_result node payload wrong: %+v", n.ToolResult)
			}
		}
	}
	if gotTU != 1 || gotTR != 1 {
		t.Errorf("expected 1 tool_use + 1 tool_result, got %d/%d", gotTU, gotTR)
	}
	// 验 requires 边存在
	var foundEdge bool
	for _, e := range env.CapabilityGraph.Edges {
		if e.Type == EdgeRequires && e.Required {
			foundEdge = true
		}
	}
	if !foundEdge {
		t.Error("expected at least one EdgeRequires from tool_result -> tool_use")
	}
}

func TestAnthropicMessagesClient_ToolResultUnknownToolUseID(t *testing.T) {
	adapter := &AnthropicMessagesClient{}
	body := []byte(`{
		"model":"claude-3","max_tokens":10,
		"messages":[
			{"role":"user","content":[{"type":"tool_result","tool_use_id":"call_missing","content":"x"}]}
		]
	}`)
	_, _, err := adapter.RequestToCanonical(newTestAnthropicCtx(t), body)
	if err == nil || !strings.Contains(err.Error(), "unknown tool_use_id") {
		t.Errorf("expected unknown tool_use_id error, got %v", err)
	}
}

func TestAnthropicMessagesClient_ToolUseMissingID(t *testing.T) {
	adapter := &AnthropicMessagesClient{}
	body := []byte(`{
		"model":"claude-3","max_tokens":10,
		"messages":[{"role":"assistant","content":[{"type":"tool_use","name":"x","input":{}}]}]
	}`)
	_, _, err := adapter.RequestToCanonical(newTestAnthropicCtx(t), body)
	if err == nil || !strings.Contains(err.Error(), "tool_use missing") {
		t.Errorf("expected tool_use missing id error, got %v", err)
	}
}

func TestAnthropicMessagesClient_Negative_MissingModel(t *testing.T) {
	adapter := &AnthropicMessagesClient{}
	body := []byte(`{"max_tokens":10,"messages":[{"role":"user","content":"hi"}]}`)
	_, _, err := adapter.RequestToCanonical(newTestAnthropicCtx(t), body)
	if err == nil || !strings.Contains(err.Error(), "model") {
		t.Errorf("expected missing-model error, got %v", err)
	}
}

func TestAnthropicMessagesClient_Negative_EmptyMessages(t *testing.T) {
	adapter := &AnthropicMessagesClient{}
	body := []byte(`{"model":"claude-3","max_tokens":10,"messages":[]}`)
	_, _, err := adapter.RequestToCanonical(newTestAnthropicCtx(t), body)
	if err == nil || !strings.Contains(err.Error(), "messages") {
		t.Errorf("expected empty-messages error, got %v", err)
	}
}

func TestAnthropicMessagesClient_Negative_MissingRole(t *testing.T) {
	adapter := &AnthropicMessagesClient{}
	body := []byte(`{"model":"claude-3","max_tokens":10,"messages":[{"content":"hi"}]}`)
	_, _, err := adapter.RequestToCanonical(newTestAnthropicCtx(t), body)
	if err == nil || !strings.Contains(err.Error(), "role") {
		t.Errorf("expected missing-role error, got %v", err)
	}
}

func TestAnthropicMessagesClient_Negative_InvalidJSON(t *testing.T) {
	adapter := &AnthropicMessagesClient{}
	_, _, err := adapter.RequestToCanonical(newTestAnthropicCtx(t), []byte("not-json"))
	if err == nil {
		t.Fatal("expected parse error")
	}
}

func TestAnthropicMessagesClient_Negative_MissingSeed(t *testing.T) {
	adapter := &AnthropicMessagesClient{}
	body := []byte(`{"model":"claude-3","max_tokens":10,"messages":[{"role":"user","content":"hi"}]}`)
	_, _, err := adapter.RequestToCanonical(context.Background(), body)
	if !errors.Is(err, ErrMissingRequestMetaSeed) {
		t.Errorf("expected ErrMissingRequestMetaSeed, got %v", err)
	}
}

// D3 / D4 测试在下方 D3 section 覆盖；旧 stub-ErrNotImplemented 期望已废弃。

// --------------------------------------------------------------------------
// D2 CanonicalToClientResponse 测试
// --------------------------------------------------------------------------

func makeAnthropicBufferedEnvelope(content []CanonicalContentBlock, stop CanonicalStopReason) *HCSF {
	env := NewEmptyEnvelope()
	env.RequestMeta.RequestID = "req_test_d2"
	env.RequestMeta.ClientProtocol = ClientProtocolAnthropicMessages
	env.RequestMeta.ProtocolFamily = "anthropic"
	env.RequestMeta.IngressPath = "/v1/messages"
	env.RequestMeta.Model = "claude-3-5-sonnet"
	env.BufferedResponse = &CanonicalResponse{
		ID:         "msg_test_001",
		Model:      "claude-3-5-sonnet-20241022",
		Content:    content,
		StopReason: stop,
		Usage:      CanonicalUsage{InputTokens: 25, OutputTokens: 7},
	}
	return env
}

func TestAnthropicMessagesClient_D2_HappyPathText(t *testing.T) {
	adapter := &AnthropicMessagesClient{}
	env := makeAnthropicBufferedEnvelope(
		[]CanonicalContentBlock{{Type: "text", Text: "Hello, how can I help?"}},
		CanonicalStopEndTurn,
	)
	body, losses, err := adapter.CanonicalToClientResponse(context.Background(), env)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(losses) != 0 {
		t.Errorf("happy path losses: %+v", losses)
	}
	var out map[string]any
	if err := jsonUnmarshal(body, &out); err != nil {
		t.Fatalf("parse output: %v", err)
	}
	if out["type"] != "message" {
		t.Errorf("type: %v", out["type"])
	}
	if out["role"] != "assistant" {
		t.Errorf("role: %v", out["role"])
	}
	if out["id"] != "msg_test_001" {
		t.Errorf("id: %v", out["id"])
	}
	if out["stop_reason"] != "end_turn" {
		t.Errorf("stop_reason: %v", out["stop_reason"])
	}
	cnt, _ := out["content"].([]any)
	if len(cnt) != 1 {
		t.Fatalf("content len: %d", len(cnt))
	}
	first, _ := cnt[0].(map[string]any)
	if first["type"] != "text" || first["text"] != "Hello, how can I help?" {
		t.Errorf("content[0]: %+v", first)
	}
	usage, _ := out["usage"].(map[string]any)
	if usage["input_tokens"].(float64) != 25 || usage["output_tokens"].(float64) != 7 {
		t.Errorf("usage: %+v", usage)
	}
}

func TestAnthropicMessagesClient_D2_TextRawBlockPassthrough(t *testing.T) {
	adapter := &AnthropicMessagesClient{}
	env := makeAnthropicBufferedEnvelope(
		[]CanonicalContentBlock{{
			Type: "text",
			Text: "answer",
			Raw:  json.RawMessage(`{"type":"text","text":"answer","citations":[{"type":"char_location","start_char_index":0,"end_char_index":6}],"beta_meta":{"trace":"kept"}}`),
		}},
		CanonicalStopEndTurn,
	)
	body, losses, err := adapter.CanonicalToClientResponse(context.Background(), env)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(losses) != 0 {
		t.Fatalf("unexpected losses: %+v", losses)
	}
	var out map[string]any
	if err := jsonUnmarshal(body, &out); err != nil {
		t.Fatalf("parse output: %v", err)
	}
	cnt := out["content"].([]any)
	first := cnt[0].(map[string]any)
	if first["type"] != "text" || first["text"] != "answer" {
		t.Fatalf("content[0]: %+v", first)
	}
	if _, ok := first["citations"]; !ok {
		t.Fatalf("text raw passthrough lost citations: %s", body)
	}
	beta, ok := first["beta_meta"].(map[string]any)
	if !ok || beta["trace"] != "kept" {
		t.Fatalf("text raw passthrough lost beta_meta: %s", body)
	}
}

func TestAnthropicMessagesClient_D2_ToolUseBlock(t *testing.T) {
	adapter := &AnthropicMessagesClient{}
	env := makeAnthropicBufferedEnvelope(
		[]CanonicalContentBlock{
			{Type: "text", Text: "let me check"},
			{Type: "tool_use", CallID: "call_xyz", Name: "get_weather", Input: []byte(`{"city":"SF"}`)},
		},
		CanonicalStopToolUse,
	)
	body, _, err := adapter.CanonicalToClientResponse(context.Background(), env)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	var out map[string]any
	if err := jsonUnmarshal(body, &out); err != nil {
		t.Fatalf("jsonUnmarshal: %v", err)
	}
	cnt := out["content"].([]any)
	if len(cnt) != 2 {
		t.Fatalf("content len: %d", len(cnt))
	}
	tu, _ := cnt[1].(map[string]any)
	if tu["type"] != "tool_use" || tu["id"] != "call_xyz" || tu["name"] != "get_weather" {
		t.Errorf("tool_use block: %+v", tu)
	}
	if out["stop_reason"] != "tool_use" {
		t.Errorf("stop_reason: %v", out["stop_reason"])
	}
}

func TestAnthropicMessagesClient_D2_StopReasonMappings(t *testing.T) {
	adapter := &AnthropicMessagesClient{}
	cases := []struct {
		canon    CanonicalStopReason
		expected string // 空 = stop_reason 应为 null
		wantLoss bool
	}{
		{CanonicalStopEndTurn, "end_turn", false},
		{CanonicalStopMaxTokens, "max_tokens", false},
		{CanonicalStopSequence, "stop_sequence", false},
		{CanonicalStopToolUse, "tool_use", false},
		{CanonicalStopRefusal, "end_turn", true}, // downgrade
		{CanonicalStopUnknown, "", true},         // null
	}
	for _, tc := range cases {
		t.Run(string(tc.canon), func(t *testing.T) {
			env := makeAnthropicBufferedEnvelope([]CanonicalContentBlock{{Type: "text", Text: "hi"}}, tc.canon)
			body, losses, err := adapter.CanonicalToClientResponse(context.Background(), env)
			if err != nil {
				t.Fatalf("err: %v", err)
			}
			var out map[string]any
			if err := jsonUnmarshal(body, &out); err != nil {
				t.Fatalf("jsonUnmarshal: %v", err)
			}
			got, _ := out["stop_reason"]
			if tc.expected == "" {
				if got != nil {
					t.Errorf("expected stop_reason null, got %v", got)
				}
			} else {
				if got != tc.expected {
					t.Errorf("expected %q, got %v", tc.expected, got)
				}
			}
			if tc.wantLoss && len(losses) == 0 {
				t.Errorf("expected loss for %s, got none", tc.canon)
			}
			if !tc.wantLoss && len(losses) != 0 {
				t.Errorf("unexpected losses for %s: %+v", tc.canon, losses)
			}
		})
	}
}

func TestAnthropicMessagesClient_D2_Negative_NilEnvelope(t *testing.T) {
	adapter := &AnthropicMessagesClient{}
	_, _, err := adapter.CanonicalToClientResponse(context.Background(), nil)
	if err == nil || !strings.Contains(err.Error(), "nil envelope") {
		t.Errorf("expected nil envelope error, got %v", err)
	}
}

func TestAnthropicMessagesClient_D2_Negative_NoBufferedResponse(t *testing.T) {
	adapter := &AnthropicMessagesClient{}
	env := NewEmptyEnvelope()
	_, _, err := adapter.CanonicalToClientResponse(context.Background(), env)
	if err == nil || !strings.Contains(err.Error(), "buffered_response") {
		t.Errorf("expected no buffered_response error, got %v", err)
	}
}

func TestAnthropicMessagesClient_D2_Negative_ToolUseMissingFields(t *testing.T) {
	adapter := &AnthropicMessagesClient{}
	env := makeAnthropicBufferedEnvelope(
		[]CanonicalContentBlock{{Type: "tool_use", CallID: "", Name: "x"}},
		CanonicalStopToolUse,
	)
	_, _, err := adapter.CanonicalToClientResponse(context.Background(), env)
	if err == nil || !strings.Contains(err.Error(), "missing call_id") {
		t.Errorf("expected missing call_id error, got %v", err)
	}
}

func TestAnthropicMessagesClient_D2_UnknownBlockTypeNonSilent(t *testing.T) {
	adapter := &AnthropicMessagesClient{}
	env := makeAnthropicBufferedEnvelope(
		[]CanonicalContentBlock{{Type: "exotic_future_type", Text: ""}},
		CanonicalStopEndTurn,
	)
	_, losses, err := adapter.CanonicalToClientResponse(context.Background(), env)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	var found bool
	for _, l := range losses {
		if strings.Contains(l.Reason, "unknown_response_block_type") {
			if l.Severity == "" {
				t.Errorf("loss must not be silent: %+v", l)
			}
			found = true
		}
	}
	if !found {
		t.Errorf("expected unknown block loss, got: %+v", losses)
	}
}

// jsonUnmarshal 是测试用 wrapper，避免每个 test 写 import "encoding/json"。
func jsonUnmarshal(data []byte, out any) error {
	return decodeJSON(data, out)
}

// --------------------------------------------------------------------------
// D3 CanonicalEventToClientChunk + D4 FinalizeClientStream 测试
// --------------------------------------------------------------------------

func TestAnthropicMessages_D3_MessageStartThenContentBlockLifecycle(t *testing.T) {
	adapter := &AnthropicMessagesClient{}
	state := NewAnthropicMessagesStreamState()
	ctx := context.Background()

	chunks, _, err := adapter.CanonicalEventToClientChunk(ctx, &CanonicalEvent{
		Type:      "message_start",
		MessageID: "msg_d3",
		Model:     "claude-3-5",
		Usage:     &CanonicalUsage{InputTokens: 10, OutputTokens: 0},
	}, state)
	if err != nil || len(chunks) != 1 {
		t.Fatalf("message_start: chunks=%d err=%v", len(chunks), err)
	}
	if !strings.Contains(string(chunks[0]), "event: message_start") {
		t.Errorf("expected event: message_start, got %q", chunks[0])
	}
	if !state.Started {
		t.Errorf("state.Started must be true")
	}

	chunks, _, err = adapter.CanonicalEventToClientChunk(ctx, &CanonicalEvent{
		Type:         "content_block_start",
		Index:        0,
		ContentBlock: &CanonicalContentBlock{Type: "text"},
	}, state)
	if err != nil || len(chunks) != 1 {
		t.Fatalf("content_block_start: err=%v", err)
	}
	if !state.OpenBlocks[0] {
		t.Errorf("expected OpenBlocks[0] = true")
	}

	chunks, _, err = adapter.CanonicalEventToClientChunk(ctx, &CanonicalEvent{
		Type:  "content_block_delta",
		Index: 0,
		Delta: &CanonicalContentDelta{Type: "text_delta", Text: "Hello"},
	}, state)
	if err != nil || len(chunks) != 1 {
		t.Fatalf("content_block_delta: err=%v", err)
	}
	if !strings.Contains(string(chunks[0]), `"text":"Hello"`) {
		t.Errorf("delta payload missing text: %s", chunks[0])
	}

	chunks, _, err = adapter.CanonicalEventToClientChunk(ctx, &CanonicalEvent{
		Type:  "content_block_stop",
		Index: 0,
	}, state)
	if err != nil || len(chunks) != 1 {
		t.Fatalf("content_block_stop: err=%v", err)
	}
	if state.OpenBlocks[0] {
		t.Errorf("expected OpenBlocks[0] cleared after stop")
	}

	chunks, _, err = adapter.CanonicalEventToClientChunk(ctx, &CanonicalEvent{
		Type:       "message_delta",
		StopReason: CanonicalStopEndTurn,
		Usage:      &CanonicalUsage{OutputTokens: 7},
	}, state)
	if err != nil || len(chunks) != 1 {
		t.Fatalf("message_delta: err=%v", err)
	}
	if !strings.Contains(string(chunks[0]), `"stop_reason":"end_turn"`) {
		t.Errorf("message_delta missing stop_reason: %s", chunks[0])
	}

	chunks, _, err = adapter.CanonicalEventToClientChunk(ctx, &CanonicalEvent{Type: "message_stop"}, state)
	if err != nil || len(chunks) < 1 {
		t.Fatalf("message_stop: err=%v", err)
	}
	if !state.Terminated {
		t.Errorf("state.Terminated must be true after message_stop")
	}
}

func TestAnthropicMessages_D3_EventsBeforeMessageStartReject(t *testing.T) {
	adapter := &AnthropicMessagesClient{}
	state := NewAnthropicMessagesStreamState()
	_, _, err := adapter.CanonicalEventToClientChunk(context.Background(), &CanonicalEvent{
		Type: "content_block_start", Index: 0, ContentBlock: &CanonicalContentBlock{Type: "text"},
	}, state)
	if err == nil || !strings.Contains(err.Error(), "before message_start") {
		t.Errorf("expected before-message_start error, got %v", err)
	}
}

func TestAnthropicMessages_D3_DuplicateMessageStartSilenced(t *testing.T) {
	adapter := &AnthropicMessagesClient{}
	state := NewAnthropicMessagesStreamState()
	ctx := context.Background()
	_, _, _ = adapter.CanonicalEventToClientChunk(ctx, &CanonicalEvent{
		Type: "message_start", MessageID: "m1", Model: "claude-3",
	}, state)
	chunks, losses, err := adapter.CanonicalEventToClientChunk(ctx, &CanonicalEvent{
		Type: "message_start", MessageID: "m1", Model: "claude-3",
	}, state)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(chunks) != 0 {
		t.Errorf("expected no chunks on duplicate, got %d", len(chunks))
	}
	if len(losses) == 0 {
		t.Errorf("expected info loss on duplicate")
	}
}

func TestAnthropicMessages_D3_EventsAfterTerminatedDropped(t *testing.T) {
	adapter := &AnthropicMessagesClient{}
	state := NewAnthropicMessagesStreamState()
	ctx := context.Background()
	_, _, _ = adapter.CanonicalEventToClientChunk(ctx, &CanonicalEvent{
		Type: "message_start", MessageID: "m1", Model: "x",
	}, state)
	_, _, _ = adapter.CanonicalEventToClientChunk(ctx, &CanonicalEvent{Type: "message_stop"}, state)
	chunks, losses, err := adapter.CanonicalEventToClientChunk(ctx, &CanonicalEvent{
		Type: "content_block_start", Index: 0, ContentBlock: &CanonicalContentBlock{Type: "text"},
	}, state)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(chunks) != 0 {
		t.Errorf("expected no chunks after terminated, got %d", len(chunks))
	}
	if len(losses) == 0 {
		t.Errorf("expected loss after terminated")
	}
}

func TestAnthropicMessages_D3_ToolUseBlockLifecycle(t *testing.T) {
	adapter := &AnthropicMessagesClient{}
	state := NewAnthropicMessagesStreamState()
	ctx := context.Background()
	_, _, _ = adapter.CanonicalEventToClientChunk(ctx, &CanonicalEvent{
		Type: "message_start", MessageID: "m1", Model: "x",
	}, state)
	chunks, _, err := adapter.CanonicalEventToClientChunk(ctx, &CanonicalEvent{
		Type:         "content_block_start",
		Index:        1,
		ContentBlock: &CanonicalContentBlock{Type: "tool_use", CallID: "call_a", Name: "get_x"},
	}, state)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if !strings.Contains(string(chunks[0]), `"type":"tool_use"`) || !strings.Contains(string(chunks[0]), `"id":"call_a"`) {
		t.Errorf("tool_use start payload wrong: %s", chunks[0])
	}
	chunks, _, _ = adapter.CanonicalEventToClientChunk(ctx, &CanonicalEvent{
		Type:  "content_block_delta",
		Index: 1,
		Delta: &CanonicalContentDelta{Type: "input_json_delta", PartialJSON: []byte(`"city"`)},
	}, state)
	if !strings.Contains(string(chunks[0]), `"input_json_delta"`) {
		t.Errorf("expected input_json_delta in payload, got %s", chunks[0])
	}
}

func TestAnthropicMessages_D3_PingPassthrough(t *testing.T) {
	adapter := &AnthropicMessagesClient{}
	state := NewAnthropicMessagesStreamState()
	chunks, _, err := adapter.CanonicalEventToClientChunk(context.Background(), &CanonicalEvent{Type: "ping"}, state)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(chunks) != 1 || !strings.Contains(string(chunks[0]), "event: ping") {
		t.Errorf("ping payload wrong: %s", chunks[0])
	}
}

func TestAnthropicMessages_D3_UnknownEventTypeLoss(t *testing.T) {
	adapter := &AnthropicMessagesClient{}
	state := NewAnthropicMessagesStreamState()
	_, _, _ = adapter.CanonicalEventToClientChunk(context.Background(), &CanonicalEvent{Type: "message_start", MessageID: "m1", Model: "x"}, state)
	chunks, losses, err := adapter.CanonicalEventToClientChunk(context.Background(), &CanonicalEvent{Type: "exotic_future"}, state)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(chunks) != 0 {
		t.Errorf("expected no chunks for unknown event, got %d", len(chunks))
	}
	if len(losses) == 0 {
		t.Errorf("expected loss for unknown event")
	}
}

func TestAnthropicMessages_D3_StateTypeMismatch(t *testing.T) {
	adapter := &AnthropicMessagesClient{}
	_, _, err := adapter.CanonicalEventToClientChunk(context.Background(), &CanonicalEvent{Type: "ping"}, "not-a-state")
	if err == nil || !strings.Contains(err.Error(), "stream state type mismatch") {
		t.Errorf("expected state type mismatch, got %v", err)
	}
}

func TestAnthropicMessages_D4_FinalizeIdempotent(t *testing.T) {
	adapter := &AnthropicMessagesClient{}
	state := NewAnthropicMessagesStreamState()
	ctx := context.Background()
	// 起 stream + open 一个 block + 不 emit message_stop。
	_, _, _ = adapter.CanonicalEventToClientChunk(ctx, &CanonicalEvent{Type: "message_start", MessageID: "m1", Model: "x"}, state)
	_, _, _ = adapter.CanonicalEventToClientChunk(ctx, &CanonicalEvent{Type: "content_block_start", Index: 0, ContentBlock: &CanonicalContentBlock{Type: "text"}}, state)

	out1, err := adapter.FinalizeClientStream(ctx, state)
	if err != nil {
		t.Fatalf("Finalize 1: %v", err)
	}
	if len(out1) < 2 {
		t.Errorf("expected at least 2 chunks (block_stop + message_stop), got %d", len(out1))
	}
	if !state.Terminated {
		t.Errorf("state must be Terminated after finalize")
	}
	out2, err := adapter.FinalizeClientStream(ctx, state)
	if err != nil {
		t.Fatalf("Finalize 2: %v", err)
	}
	if len(out2) != 0 {
		t.Errorf("second Finalize must be no-op, got %d chunks", len(out2))
	}
}

// --------------------------------------------------------------------------
// D1.x continuations: cache_control / image / thinking
// --------------------------------------------------------------------------

func TestAnthropicMessages_D1x_CacheControlOnTextBlock(t *testing.T) {
	adapter := &AnthropicMessagesClient{}
	body := []byte(`{
		"model":"claude-3","max_tokens":10,
		"messages":[{"role":"user","content":[
			{"type":"text","text":"big prompt"},
			{"type":"text","text":"trailing","cache_control":{"type":"ephemeral"}}
		]}]
	}`)
	env, _, err := adapter.RequestToCanonical(newTestAnthropicCtx(t), body)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	var cacheNode, textNodeID *CapabilityNode
	for i, n := range env.CapabilityGraph.Nodes {
		if n.Kind == CapabilityCacheControl {
			cacheNode = &env.CapabilityGraph.Nodes[i]
		}
		if n.Kind == CapabilityText && n.Text != nil && n.Text.Block.Text == "trailing" {
			textNodeID = &env.CapabilityGraph.Nodes[i]
		}
	}
	if cacheNode == nil {
		t.Fatal("expected CapabilityCacheControl node")
	}
	if textNodeID == nil {
		t.Fatal("expected trailing text node")
	}
	if len(cacheNode.CacheControl.BreakpointRefs) != 1 || cacheNode.CacheControl.BreakpointRefs[0] != textNodeID.ID {
		t.Errorf("CacheControl.BreakpointRefs should point to trailing text node, got %+v", cacheNode.CacheControl.BreakpointRefs)
	}
	if !cacheNode.CacheControl.SanitizeSystemMetadata {
		t.Error("SanitizeSystemMetadata must default true")
	}
}

func TestAnthropicMessages_D1x_ImageBlockBase64(t *testing.T) {
	adapter := &AnthropicMessagesClient{}
	body := []byte(`{
		"model":"claude-3","max_tokens":10,
		"messages":[{"role":"user","content":[
			{"type":"image","source":{"type":"base64","media_type":"image/png","data":"iVBOR..."}}
		]}]
	}`)
	env, _, err := adapter.RequestToCanonical(newTestAnthropicCtx(t), body)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	var imgNode *CapabilityNode
	for i, n := range env.CapabilityGraph.Nodes {
		if n.Kind == CapabilityImage {
			imgNode = &env.CapabilityGraph.Nodes[i]
			break
		}
	}
	if imgNode == nil {
		t.Fatal("expected CapabilityImage node")
	}
	if imgNode.Image.SourceKind != DataSourceInlineBase64 {
		t.Errorf("SourceKind: %s", imgNode.Image.SourceKind)
	}
	if imgNode.Image.MediaType != "image/png" {
		t.Errorf("MediaType: %s", imgNode.Image.MediaType)
	}
	if imgNode.Image.Locator.Value != "iVBOR..." {
		t.Errorf("Locator.Value: %s", imgNode.Image.Locator.Value)
	}
}

func TestAnthropicMessages_D1x_ImageBlockURL(t *testing.T) {
	adapter := &AnthropicMessagesClient{}
	body := []byte(`{
		"model":"claude-3","max_tokens":10,
		"messages":[{"role":"user","content":[
			{"type":"image","source":{"type":"url","media_type":"image/jpeg","url":"https://x/y.jpg"}}
		]}]
	}`)
	env, _, err := adapter.RequestToCanonical(newTestAnthropicCtx(t), body)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	var imgNode *CapabilityNode
	for i, n := range env.CapabilityGraph.Nodes {
		if n.Kind == CapabilityImage {
			imgNode = &env.CapabilityGraph.Nodes[i]
			break
		}
	}
	if imgNode == nil || imgNode.Image.SourceKind != DataSourceURL || imgNode.Image.Locator.Value != "https://x/y.jpg" {
		t.Errorf("URL image wrong: %+v", imgNode)
	}
}

func TestAnthropicMessages_D1x_ImageMissingMediaTypeRejected(t *testing.T) {
	adapter := &AnthropicMessagesClient{}
	body := []byte(`{
		"model":"claude-3","max_tokens":10,
		"messages":[{"role":"user","content":[
			{"type":"image","source":{"type":"base64","data":"x"}}
		]}]
	}`)
	_, _, err := adapter.RequestToCanonical(newTestAnthropicCtx(t), body)
	if err == nil || !strings.Contains(err.Error(), "media_type required") {
		t.Errorf("expected media_type required, got %v", err)
	}
}

func TestAnthropicMessages_D1x_ImageBase64MissingDataRejected(t *testing.T) {
	adapter := &AnthropicMessagesClient{}
	body := []byte(`{
		"model":"claude-3","max_tokens":10,
		"messages":[{"role":"user","content":[
			{"type":"image","source":{"type":"base64","media_type":"image/png"}}
		]}]
	}`)
	_, _, err := adapter.RequestToCanonical(newTestAnthropicCtx(t), body)
	if err == nil || !strings.Contains(err.Error(), "data required") {
		t.Errorf("expected data required, got %v", err)
	}
}

func TestAnthropicMessages_D1x_ImageUnsupportedSourceType(t *testing.T) {
	adapter := &AnthropicMessagesClient{}
	body := []byte(`{
		"model":"claude-3","max_tokens":10,
		"messages":[{"role":"user","content":[
			{"type":"image","source":{"type":"exotic_future_kind","media_type":"image/png"}}
		]}]
	}`)
	_, _, err := adapter.RequestToCanonical(newTestAnthropicCtx(t), body)
	if err == nil || !strings.Contains(err.Error(), "not supported") {
		t.Errorf("expected unsupported source type, got %v", err)
	}
}

func TestAnthropicMessages_D1x_ThinkingTopLevel(t *testing.T) {
	adapter := &AnthropicMessagesClient{}
	body := []byte(`{
		"model":"claude-3","max_tokens":10,
		"thinking":{"type":"enabled","budget_tokens":2048},
		"messages":[{"role":"user","content":"hi"}]
	}`)
	env, _, err := adapter.RequestToCanonical(newTestAnthropicCtx(t), body)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	var thinkNode *CapabilityNode
	for i, n := range env.CapabilityGraph.Nodes {
		if n.Kind == CapabilityThinking {
			thinkNode = &env.CapabilityGraph.Nodes[i]
			break
		}
	}
	if thinkNode == nil {
		t.Fatal("expected CapabilityThinking node")
	}
	if thinkNode.Thinking.BudgetTokens != 2048 {
		t.Errorf("BudgetTokens: %d", thinkNode.Thinking.BudgetTokens)
	}
	if thinkNode.Thinking.Redaction != RedactionPublic {
		t.Errorf("Redaction: %s", thinkNode.Thinking.Redaction)
	}
}

func TestAnthropicMessages_D1x_ThinkingDisabledIgnored(t *testing.T) {
	adapter := &AnthropicMessagesClient{}
	body := []byte(`{
		"model":"claude-3","max_tokens":10,
		"thinking":{"type":"disabled","budget_tokens":0},
		"messages":[{"role":"user","content":"hi"}]
	}`)
	env, _, err := adapter.RequestToCanonical(newTestAnthropicCtx(t), body)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	for _, n := range env.CapabilityGraph.Nodes {
		if n.Kind == CapabilityThinking {
			t.Error("expected no thinking node when type=disabled")
		}
	}
}

func TestAnthropicMessages_D4_FinalizeBeforeStartEmpty(t *testing.T) {
	adapter := &AnthropicMessagesClient{}
	out, err := adapter.FinalizeClientStream(context.Background(), NewAnthropicMessagesStreamState())
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(out) != 0 {
		t.Errorf("Finalize before message_start should emit nothing, got %d", len(out))
	}
}

func TestAnthropicMessagesClient_EnvelopeIsValidateReady(t *testing.T) {
	// 检查 D1 产出的 envelope 通过 ValidateEnvelopeVersionGuard（adapter 边界轻量守门）。
	// 完整校验由 -tags debug 测试覆盖。
	adapter := &AnthropicMessagesClient{}
	body := []byte(`{
		"model":"claude-3","max_tokens":10,
		"system":"hello sys",
		"messages":[{"role":"user","content":"hi"},{"role":"assistant","content":"hi back"}]
	}`)
	env, _, err := adapter.RequestToCanonical(newTestAnthropicCtx(t), body)
	if err != nil {
		t.Fatalf("RequestToCanonical: %v", err)
	}
	if err := ValidateEnvelopeVersionGuard(env); err != nil {
		t.Fatalf("ValidateEnvelopeVersionGuard: %v", err)
	}
	// 节点数 = 2 user/assistant text + 1 system text
	if len(env.CapabilityGraph.Nodes) != 3 {
		t.Errorf("expected 3 text nodes (user/assistant/system), got %d", len(env.CapabilityGraph.Nodes))
	}
}
