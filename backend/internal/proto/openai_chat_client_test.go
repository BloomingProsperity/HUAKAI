package proto

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// 覆盖 P-2 D5 openai_chat.RequestToCanonical 第一片：text + roles + tool_calls
// + tool result（role=tool）+ tools 声明。

func newTestOpenAIChatCtx(t *testing.T) context.Context {
	t.Helper()
	return ContextWithRequestMetaSeed(context.Background(), RequestMetaSeed{
		RequestID:      "req_test_d5",
		ClientProtocol: ClientProtocolOpenAIChat,
		ProtocolFamily: "openai",
		IngressPath:    "/v1/chat/completions",
		EvidenceLabel:  EvidenceMock,
	})
}

func TestOpenAIChatClient_HappyPath_Text(t *testing.T) {
	adapter := &OpenAIChatClient{}
	body := []byte(`{
		"model":"gpt-4o",
		"messages":[
			{"role":"system","content":"You are helpful."},
			{"role":"user","content":"Hello"}
		]
	}`)
	env, losses, err := adapter.RequestToCanonical(newTestOpenAIChatCtx(t), body)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if env.RequestMeta.Model != "gpt-4o" {
		t.Errorf("Model: %q", env.RequestMeta.Model)
	}
	if len(env.Messages) != 2 {
		t.Errorf("expected 2 messages, got %d", len(env.Messages))
	}
	if env.Messages[0].Role != "system" || env.Messages[1].Role != "user" {
		t.Errorf("roles: %s / %s", env.Messages[0].Role, env.Messages[1].Role)
	}
	if env.StreamPlan.Mode != StreamModeBuffered {
		t.Errorf("default stream mode: %s", env.StreamPlan.Mode)
	}
	if len(losses) != 0 {
		t.Errorf("happy path losses: %+v", losses)
	}
}

func TestOpenAIChatClient_StreamFlag(t *testing.T) {
	adapter := &OpenAIChatClient{}
	body := []byte(`{"model":"gpt-4o","stream":true,"messages":[{"role":"user","content":"hi"}]}`)
	env, _, err := adapter.RequestToCanonical(newTestOpenAIChatCtx(t), body)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if env.StreamPlan.Mode != StreamModeStreaming {
		t.Errorf("expected streaming, got %s", env.StreamPlan.Mode)
	}
}

func TestOpenAIChatClient_MaxCompletionTokensPreferred(t *testing.T) {
	adapter := &OpenAIChatClient{}
	body := []byte(`{"model":"gpt-4o","max_tokens":100,"max_completion_tokens":500,"messages":[{"role":"user","content":"hi"}]}`)
	env, _, err := adapter.RequestToCanonical(newTestOpenAIChatCtx(t), body)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if env.RequestControls.MaxTokens == nil || *env.RequestControls.MaxTokens != 500 {
		t.Errorf("expected 500 (max_completion_tokens preferred over max_tokens), got %v", env.RequestControls.MaxTokens)
	}
}

func TestOpenAIChatClient_StopStringOrArray(t *testing.T) {
	adapter := &OpenAIChatClient{}
	body1 := []byte(`{"model":"gpt-4o","stop":"END","messages":[{"role":"user","content":"hi"}]}`)
	env1, _, err := adapter.RequestToCanonical(newTestOpenAIChatCtx(t), body1)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(env1.RequestControls.Stop) != 1 || env1.RequestControls.Stop[0] != "END" {
		t.Errorf("stop string: %+v", env1.RequestControls.Stop)
	}
	body2 := []byte(`{"model":"gpt-4o","stop":["A","B"],"messages":[{"role":"user","content":"hi"}]}`)
	env2, _, err := adapter.RequestToCanonical(newTestOpenAIChatCtx(t), body2)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(env2.RequestControls.Stop) != 2 {
		t.Errorf("stop array: %+v", env2.RequestControls.Stop)
	}
}

func TestOpenAIChatClient_ToolsConverted(t *testing.T) {
	adapter := &OpenAIChatClient{}
	body := []byte(`{
		"model":"gpt-4o",
		"tools":[{"type":"function","function":{"name":"get_weather","description":"...","parameters":{"type":"object"}}}],
		"tool_choice":"auto",
		"messages":[{"role":"user","content":"hi"}]
	}`)
	env, _, err := adapter.RequestToCanonical(newTestOpenAIChatCtx(t), body)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(env.RequestControls.Tools) != 1 || env.RequestControls.Tools[0].Name != "get_weather" {
		t.Errorf("tools: %+v", env.RequestControls.Tools)
	}
	if string(env.RequestControls.ToolChoice) != `"auto"` {
		t.Errorf("tool_choice preserved: %s", string(env.RequestControls.ToolChoice))
	}
}

func TestOpenAIChatClient_AssistantToolCallsThenToolResult(t *testing.T) {
	adapter := &OpenAIChatClient{}
	body := []byte(`{
		"model":"gpt-4o",
		"messages":[
			{"role":"user","content":"weather?"},
			{"role":"assistant","content":null,"tool_calls":[
				{"id":"call_a","type":"function","function":{"name":"get_weather","arguments":"{\"city\":\"SF\"}"}}
			]},
			{"role":"tool","tool_call_id":"call_a","content":"sunny"}
		]
	}`)
	env, _, err := adapter.RequestToCanonical(newTestOpenAIChatCtx(t), body)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	gotTU, gotTR := 0, 0
	for _, n := range env.CapabilityGraph.Nodes {
		switch n.Kind {
		case CapabilityToolUse:
			gotTU++
			if n.ToolUse == nil || n.ToolUse.ToolCallID != "call_a" || n.ToolUse.Name != "get_weather" {
				t.Errorf("tool_use payload: %+v", n.ToolUse)
			}
		case CapabilityToolResult:
			gotTR++
			if n.ToolResult == nil || n.ToolResult.ToolCallID != "call_a" {
				t.Errorf("tool_result payload: %+v", n.ToolResult)
			}
		}
	}
	if gotTU != 1 || gotTR != 1 {
		t.Errorf("expected 1 tool_use + 1 tool_result, got %d/%d", gotTU, gotTR)
	}
	var foundEdge bool
	for _, e := range env.CapabilityGraph.Edges {
		if e.Type == EdgeRequires {
			foundEdge = true
		}
	}
	if !foundEdge {
		t.Error("expected EdgeRequires from tool_result to tool_use")
	}
}

func TestOpenAIChatClient_ImageURLContentPartLoss(t *testing.T) {
	adapter := &OpenAIChatClient{}
	body := []byte(`{
		"model":"gpt-4o",
		"messages":[{"role":"user","content":[
			{"type":"text","text":"see this:"},
			{"type":"image_url","image_url":{"url":"https://x"}}
		]}]
	}`)
	_, losses, err := adapter.RequestToCanonical(newTestOpenAIChatCtx(t), body)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	var foundImg bool
	for _, l := range losses {
		if l.Severity == "" {
			t.Errorf("loss must not be silent: %+v", l)
		}
		if strings.Contains(l.Reason, "image_url_d5_pending") {
			foundImg = true
		}
	}
	if !foundImg {
		t.Errorf("expected image_url pending loss")
	}
}

func TestOpenAIChatClient_NegativeMissingModel(t *testing.T) {
	adapter := &OpenAIChatClient{}
	body := []byte(`{"messages":[{"role":"user","content":"hi"}]}`)
	_, _, err := adapter.RequestToCanonical(newTestOpenAIChatCtx(t), body)
	if err == nil || !strings.Contains(err.Error(), "model") {
		t.Errorf("expected missing model error, got %v", err)
	}
}

func TestOpenAIChatClient_NegativeEmptyMessages(t *testing.T) {
	adapter := &OpenAIChatClient{}
	body := []byte(`{"model":"gpt-4o","messages":[]}`)
	_, _, err := adapter.RequestToCanonical(newTestOpenAIChatCtx(t), body)
	if err == nil || !strings.Contains(err.Error(), "messages") {
		t.Errorf("expected empty messages error, got %v", err)
	}
}

func TestOpenAIChatClient_NegativeToolResultUnknownID(t *testing.T) {
	adapter := &OpenAIChatClient{}
	body := []byte(`{
		"model":"gpt-4o",
		"messages":[{"role":"tool","tool_call_id":"call_missing","content":"x"}]
	}`)
	_, _, err := adapter.RequestToCanonical(newTestOpenAIChatCtx(t), body)
	if err == nil || !strings.Contains(err.Error(), "unknown tool_call_id") {
		t.Errorf("expected unknown tool_call_id error, got %v", err)
	}
}

func TestOpenAIChatClient_NegativeToolMessageMissingID(t *testing.T) {
	adapter := &OpenAIChatClient{}
	body := []byte(`{"model":"gpt-4o","messages":[{"role":"tool","content":"x"}]}`)
	_, _, err := adapter.RequestToCanonical(newTestOpenAIChatCtx(t), body)
	if err == nil || !strings.Contains(err.Error(), "tool_call_id") {
		t.Errorf("expected missing tool_call_id, got %v", err)
	}
}

func TestOpenAIChatClient_NegativeInvalidJSON(t *testing.T) {
	adapter := &OpenAIChatClient{}
	_, _, err := adapter.RequestToCanonical(newTestOpenAIChatCtx(t), []byte("not-json"))
	if err == nil {
		t.Fatal("expected parse error")
	}
}

func TestOpenAIChatClient_NegativeMissingSeed(t *testing.T) {
	adapter := &OpenAIChatClient{}
	body := []byte(`{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}]}`)
	_, _, err := adapter.RequestToCanonical(context.Background(), body)
	if !errors.Is(err, ErrMissingRequestMetaSeed) {
		t.Errorf("expected ErrMissingRequestMetaSeed, got %v", err)
	}
}

// D7/D8 测试在下方专属 section；旧 stub-ErrNotImplemented 期望已废弃。

// --------------------------------------------------------------------------
// D7 / D8 streaming tests
// --------------------------------------------------------------------------

func TestOpenAIChat_D7_RoleDeltaThenTextThenFinishThenDONE(t *testing.T) {
	adapter := &OpenAIChatClient{}
	state := NewOpenAIChatStreamState()
	ctx := context.Background()

	chunks, _, err := adapter.CanonicalEventToClientChunk(ctx, &CanonicalEvent{
		Type: "message_start", MessageID: "chatcmpl-x", Model: "gpt-4o",
	}, state)
	if err != nil || len(chunks) != 1 {
		t.Fatalf("message_start chunks=%d err=%v", len(chunks), err)
	}
	if !strings.Contains(string(chunks[0]), `"role":"assistant"`) {
		t.Errorf("role delta missing: %s", chunks[0])
	}

	chunks, _, err = adapter.CanonicalEventToClientChunk(ctx, &CanonicalEvent{
		Type:  "content_block_delta",
		Index: 0,
		Delta: &CanonicalContentDelta{Type: "text_delta", Text: "Hello"},
	}, state)
	if err != nil {
		t.Fatalf("text delta err: %v", err)
	}
	if !strings.Contains(string(chunks[0]), `"content":"Hello"`) {
		t.Errorf("text delta wrong: %s", chunks[0])
	}

	chunks, _, err = adapter.CanonicalEventToClientChunk(ctx, &CanonicalEvent{
		Type: "message_delta", StopReason: CanonicalStopEndTurn,
	}, state)
	if err != nil {
		t.Fatalf("finish err: %v", err)
	}
	if !strings.Contains(string(chunks[0]), `"finish_reason":"stop"`) {
		t.Errorf("finish_reason missing: %s", chunks[0])
	}

	chunks, _, err = adapter.CanonicalEventToClientChunk(ctx, &CanonicalEvent{Type: "message_stop"}, state)
	if err != nil {
		t.Fatalf("message_stop err: %v", err)
	}
	if !strings.Contains(string(chunks[0]), "data: [DONE]") {
		t.Errorf("DONE missing: %s", chunks[0])
	}
	if !state.DoneEmitted {
		t.Error("DoneEmitted must be true")
	}
}

func TestOpenAIChat_D7_ToolCallStartThenArgsDelta(t *testing.T) {
	adapter := &OpenAIChatClient{}
	state := NewOpenAIChatStreamState()
	ctx := context.Background()
	_, _, _ = adapter.CanonicalEventToClientChunk(ctx, &CanonicalEvent{Type: "message_start", MessageID: "c1", Model: "gpt"}, state)
	chunks, _, err := adapter.CanonicalEventToClientChunk(ctx, &CanonicalEvent{
		Type:         "content_block_start",
		Index:        0,
		ContentBlock: &CanonicalContentBlock{Type: "tool_use", CallID: "call_a", Name: "get_x"},
	}, state)
	if err != nil {
		t.Fatalf("tool_use start err: %v", err)
	}
	if !strings.Contains(string(chunks[0]), `"id":"call_a"`) || !strings.Contains(string(chunks[0]), `"name":"get_x"`) {
		t.Errorf("tool_use start wrong: %s", chunks[0])
	}
	chunks, _, _ = adapter.CanonicalEventToClientChunk(ctx, &CanonicalEvent{
		Type:  "content_block_delta",
		Index: 0,
		Delta: &CanonicalContentDelta{Type: "input_json_delta", PartialJSON: []byte(`{"city":"SF"}`)},
	}, state)
	if !strings.Contains(string(chunks[0]), `"arguments":"{\"city\":\"SF\"}"`) {
		t.Errorf("args delta wrong: %s", chunks[0])
	}
}

func TestOpenAIChat_D7_DuplicateMessageStartSilenced(t *testing.T) {
	adapter := &OpenAIChatClient{}
	state := NewOpenAIChatStreamState()
	ctx := context.Background()
	_, _, _ = adapter.CanonicalEventToClientChunk(ctx, &CanonicalEvent{Type: "message_start", MessageID: "c1", Model: "gpt"}, state)
	chunks, losses, err := adapter.CanonicalEventToClientChunk(ctx, &CanonicalEvent{Type: "message_start", MessageID: "c1", Model: "gpt"}, state)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(chunks) != 0 {
		t.Errorf("duplicate must emit nothing, got %d", len(chunks))
	}
	if len(losses) == 0 {
		t.Errorf("expected info loss on duplicate")
	}
}

func TestOpenAIChat_D7_EventsAfterDONEDropped(t *testing.T) {
	adapter := &OpenAIChatClient{}
	state := NewOpenAIChatStreamState()
	ctx := context.Background()
	_, _, _ = adapter.CanonicalEventToClientChunk(ctx, &CanonicalEvent{Type: "message_start", MessageID: "c1", Model: "gpt"}, state)
	_, _, _ = adapter.CanonicalEventToClientChunk(ctx, &CanonicalEvent{Type: "message_stop"}, state)
	chunks, losses, err := adapter.CanonicalEventToClientChunk(ctx, &CanonicalEvent{Type: "content_block_delta", Delta: &CanonicalContentDelta{Type: "text_delta", Text: "x"}}, state)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(chunks) != 0 {
		t.Errorf("expected no chunks after DONE, got %d", len(chunks))
	}
	if len(losses) == 0 {
		t.Errorf("expected post-DONE loss")
	}
}

func TestOpenAIChat_D7_PingDroppedWithLoss(t *testing.T) {
	adapter := &OpenAIChatClient{}
	state := NewOpenAIChatStreamState()
	chunks, losses, err := adapter.CanonicalEventToClientChunk(context.Background(), &CanonicalEvent{Type: "ping"}, state)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(chunks) != 0 {
		t.Errorf("ping must not emit chunks, got %d", len(chunks))
	}
	if len(losses) == 0 {
		t.Errorf("expected info loss for ping")
	}
}

func TestOpenAIChat_D7_StateTypeMismatch(t *testing.T) {
	adapter := &OpenAIChatClient{}
	_, _, err := adapter.CanonicalEventToClientChunk(context.Background(), &CanonicalEvent{Type: "ping"}, "bad")
	if err == nil || !strings.Contains(err.Error(), "stream state type mismatch") {
		t.Errorf("expected state mismatch, got %v", err)
	}
}

func TestOpenAIChat_D8_FinalizeAutoFinishAndDONE(t *testing.T) {
	adapter := &OpenAIChatClient{}
	state := NewOpenAIChatStreamState()
	ctx := context.Background()
	_, _, _ = adapter.CanonicalEventToClientChunk(ctx, &CanonicalEvent{Type: "message_start", MessageID: "c1", Model: "gpt"}, state)
	// 模拟上游 EOF 没发 message_delta 或 message_stop。
	out, err := adapter.FinalizeClientStream(ctx, state)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(out) < 2 {
		t.Fatalf("expected at least finish chunk + DONE, got %d", len(out))
	}
	if !strings.Contains(string(out[0]), `"finish_reason":"stop"`) {
		t.Errorf("auto finish chunk wrong: %s", out[0])
	}
	if !strings.Contains(string(out[1]), "data: [DONE]") {
		t.Errorf("DONE not last: %s", out[1])
	}
}

func TestOpenAIChat_D8_FinalizeIdempotent(t *testing.T) {
	adapter := &OpenAIChatClient{}
	state := NewOpenAIChatStreamState()
	ctx := context.Background()
	_, _, _ = adapter.CanonicalEventToClientChunk(ctx, &CanonicalEvent{Type: "message_start", MessageID: "c1", Model: "gpt"}, state)
	_, _ = adapter.FinalizeClientStream(ctx, state)
	out2, err := adapter.FinalizeClientStream(ctx, state)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(out2) != 0 {
		t.Errorf("second Finalize must be no-op, got %d", len(out2))
	}
}

func TestOpenAIChat_D8_FinalizeBeforeStartEmpty(t *testing.T) {
	adapter := &OpenAIChatClient{}
	out, err := adapter.FinalizeClientStream(context.Background(), NewOpenAIChatStreamState())
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(out) != 0 {
		t.Errorf("Finalize before start should be empty, got %d", len(out))
	}
}

// --------------------------------------------------------------------------
// D6 CanonicalToClientResponse 测试
// --------------------------------------------------------------------------

func makeOpenAIChatBufferedEnv(content []CanonicalContentBlock, stop CanonicalStopReason) *HCSF {
	env := NewEmptyEnvelope()
	env.RequestMeta.RequestID = "req_test_d6"
	env.RequestMeta.ClientProtocol = ClientProtocolOpenAIChat
	env.RequestMeta.ProtocolFamily = "openai"
	env.RequestMeta.IngressPath = "/v1/chat/completions"
	env.RequestMeta.Model = "gpt-4o"
	env.BufferedResponse = &CanonicalResponse{
		ID:         "chatcmpl-test-001",
		Model:      "gpt-4o-2024-08-06",
		Content:    content,
		StopReason: stop,
		Usage:      CanonicalUsage{InputTokens: 9, OutputTokens: 12},
	}
	return env
}

func TestOpenAIChatClient_D6_HappyText(t *testing.T) {
	adapter := &OpenAIChatClient{}
	env := makeOpenAIChatBufferedEnv(
		[]CanonicalContentBlock{{Type: "text", Text: "Hello!"}},
		CanonicalStopEndTurn,
	)
	body, _, err := adapter.CanonicalToClientResponse(context.Background(), env)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	var out map[string]any
	_ = jsonUnmarshal(body, &out)
	if out["object"] != "chat.completion" {
		t.Errorf("object: %v", out["object"])
	}
	choices := out["choices"].([]any)
	c0 := choices[0].(map[string]any)
	if c0["finish_reason"] != "stop" {
		t.Errorf("finish_reason: %v", c0["finish_reason"])
	}
	msg := c0["message"].(map[string]any)
	if msg["role"] != "assistant" || msg["content"] != "Hello!" {
		t.Errorf("message: %+v", msg)
	}
	usage := out["usage"].(map[string]any)
	if usage["prompt_tokens"].(float64) != 9 || usage["completion_tokens"].(float64) != 12 || usage["total_tokens"].(float64) != 21 {
		t.Errorf("usage: %+v", usage)
	}
}

func TestOpenAIChatClient_D6_ToolCalls(t *testing.T) {
	adapter := &OpenAIChatClient{}
	env := makeOpenAIChatBufferedEnv(
		[]CanonicalContentBlock{
			{Type: "tool_use", CallID: "call_xyz", Name: "get_weather", Input: []byte(`{"city":"SF"}`)},
		},
		CanonicalStopToolUse,
	)
	body, _, err := adapter.CanonicalToClientResponse(context.Background(), env)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	var out map[string]any
	_ = jsonUnmarshal(body, &out)
	c0 := out["choices"].([]any)[0].(map[string]any)
	if c0["finish_reason"] != "tool_calls" {
		t.Errorf("finish_reason: %v", c0["finish_reason"])
	}
	msg := c0["message"].(map[string]any)
	tc := msg["tool_calls"].([]any)
	if len(tc) != 1 {
		t.Fatalf("tool_calls len: %d", len(tc))
	}
	first := tc[0].(map[string]any)
	if first["id"] != "call_xyz" || first["type"] != "function" {
		t.Errorf("tool_call: %+v", first)
	}
	fn := first["function"].(map[string]any)
	if fn["name"] != "get_weather" || fn["arguments"] != `{"city":"SF"}` {
		t.Errorf("function: %+v", fn)
	}
}

func TestOpenAIChatClient_D6_StopReasonMappings(t *testing.T) {
	adapter := &OpenAIChatClient{}
	cases := []struct {
		canon   CanonicalStopReason
		expect  string
		wantLoss bool
	}{
		{CanonicalStopEndTurn, "stop", false},
		{CanonicalStopSequence, "stop", false},
		{CanonicalStopMaxTokens, "length", false},
		{CanonicalStopToolUse, "tool_calls", false},
		{CanonicalStopRefusal, "content_filter", true},
		{CanonicalStopUnknown, "", true},
	}
	for _, tc := range cases {
		t.Run(string(tc.canon), func(t *testing.T) {
			env := makeOpenAIChatBufferedEnv([]CanonicalContentBlock{{Type: "text", Text: "x"}}, tc.canon)
			body, losses, err := adapter.CanonicalToClientResponse(context.Background(), env)
			if err != nil {
				t.Fatalf("err: %v", err)
			}
			var out map[string]any
			_ = jsonUnmarshal(body, &out)
			finish := out["choices"].([]any)[0].(map[string]any)["finish_reason"]
			if tc.expect == "" {
				if finish != nil {
					t.Errorf("expected null finish_reason, got %v", finish)
				}
			} else if finish != tc.expect {
				t.Errorf("expected %q, got %v", tc.expect, finish)
			}
			if tc.wantLoss && len(losses) == 0 {
				t.Errorf("expected loss")
			}
		})
	}
}

func TestOpenAIChatClient_D6_CacheReadUsage(t *testing.T) {
	adapter := &OpenAIChatClient{}
	env := makeOpenAIChatBufferedEnv([]CanonicalContentBlock{{Type: "text", Text: "x"}}, CanonicalStopEndTurn)
	env.BufferedResponse.Usage.CacheReadInputTokens = 7
	body, _, err := adapter.CanonicalToClientResponse(context.Background(), env)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	var out map[string]any
	_ = jsonUnmarshal(body, &out)
	usage := out["usage"].(map[string]any)
	details := usage["prompt_tokens_details"].(map[string]any)
	if details["cached_tokens"].(float64) != 7 {
		t.Errorf("expected cached_tokens=7, got %v", details["cached_tokens"])
	}
}

func TestOpenAIChatClient_D6_Negative_NilEnvelope(t *testing.T) {
	adapter := &OpenAIChatClient{}
	_, _, err := adapter.CanonicalToClientResponse(context.Background(), nil)
	if err == nil || !strings.Contains(err.Error(), "nil envelope") {
		t.Errorf("expected nil envelope error, got %v", err)
	}
}

func TestOpenAIChatClient_D6_Negative_NoBuffered(t *testing.T) {
	adapter := &OpenAIChatClient{}
	env := NewEmptyEnvelope()
	_, _, err := adapter.CanonicalToClientResponse(context.Background(), env)
	if err == nil || !strings.Contains(err.Error(), "buffered_response") {
		t.Errorf("expected no buffered_response error, got %v", err)
	}
}

func TestOpenAIChatClient_D6_ToolUseMissingFields(t *testing.T) {
	adapter := &OpenAIChatClient{}
	env := makeOpenAIChatBufferedEnv(
		[]CanonicalContentBlock{{Type: "tool_use", CallID: "", Name: "x"}},
		CanonicalStopToolUse,
	)
	_, _, err := adapter.CanonicalToClientResponse(context.Background(), env)
	if err == nil || !strings.Contains(err.Error(), "missing call_id") {
		t.Errorf("expected missing call_id error, got %v", err)
	}
}

func TestOpenAIChatClient_EnvelopeIsValidateReady(t *testing.T) {
	adapter := &OpenAIChatClient{}
	body := []byte(`{
		"model":"gpt-4o",
		"messages":[
			{"role":"system","content":"you are helpful"},
			{"role":"user","content":"hi"}
		]
	}`)
	env, _, err := adapter.RequestToCanonical(newTestOpenAIChatCtx(t), body)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if err := ValidateEnvelopeVersionGuard(env); err != nil {
		t.Fatalf("ValidateEnvelopeVersionGuard: %v", err)
	}
}
