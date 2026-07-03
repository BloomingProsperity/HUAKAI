package proto

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func newTestOpenAIResponsesCtx(t *testing.T) context.Context {
	t.Helper()
	return ContextWithRequestMetaSeed(context.Background(), RequestMetaSeed{
		RequestID:      "req_test_d9",
		ClientProtocol: ClientProtocolOpenAIResponses,
		ProtocolFamily: "openai",
		IngressPath:    "/v1/responses",
		EvidenceLabel:  EvidenceMock,
	})
}

func TestOpenAIResponsesClient_HappyPath_InputString(t *testing.T) {
	adapter := &OpenAIResponsesClient{}
	body := []byte(`{"model":"gpt-4o","input":"Hello"}`)
	env, losses, err := adapter.RequestToCanonical(newTestOpenAIResponsesCtx(t), body)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if env.RequestMeta.Model != "gpt-4o" {
		t.Errorf("Model: %q", env.RequestMeta.Model)
	}
	if len(env.Messages) != 1 || env.Messages[0].Role != "user" || env.Messages[0].Content[0].Text != "Hello" {
		t.Errorf("Messages: %+v", env.Messages)
	}
	if len(losses) != 0 {
		t.Errorf("happy path losses: %+v", losses)
	}
}

func TestParamPassthroughNotDropped(t *testing.T) {
	tests := []struct {
		name    string
		run     func(t *testing.T) (*HCSF, []ProtocolLossEntry, error)
		field   string
		wantRaw string
	}{
		{
			name: "anthropic top_k",
			run: func(t *testing.T) (*HCSF, []ProtocolLossEntry, error) {
				t.Helper()
				return (&AnthropicMessagesClient{}).RequestToCanonical(newTestAnthropicCtx(t), []byte(`{
					"model":"claude-3",
					"max_tokens":10,
					"top_k":17,
					"messages":[{"role":"user","content":"hi"}]
				}`))
			},
			field:   "top_k",
			wantRaw: `17`,
		},
		{
			name: "responses max_tool_calls",
			run: func(t *testing.T) (*HCSF, []ProtocolLossEntry, error) {
				t.Helper()
				return (&OpenAIResponsesClient{}).RequestToCanonical(newTestOpenAIResponsesCtx(t), []byte(`{
					"model":"gpt-4o",
					"input":"hi",
					"max_tool_calls":3
				}`))
			},
			field:   "max_tool_calls",
			wantRaw: `3`,
		},
		{
			name: "responses prompt_cache_key",
			run: func(t *testing.T) (*HCSF, []ProtocolLossEntry, error) {
				t.Helper()
				return (&OpenAIResponsesClient{}).RequestToCanonical(newTestOpenAIResponsesCtx(t), []byte(`{
					"model":"gpt-4o",
					"input":"hi",
					"prompt_cache_key":"tenant-a:stable-prefix"
				}`))
			},
			field:   "prompt_cache_key",
			wantRaw: `"tenant-a:stable-prefix"`,
		},
		{
			name: "responses truncation",
			run: func(t *testing.T) (*HCSF, []ProtocolLossEntry, error) {
				t.Helper()
				return (&OpenAIResponsesClient{}).RequestToCanonical(newTestOpenAIResponsesCtx(t), []byte(`{
					"model":"gpt-4o",
					"input":"hi",
					"truncation":"auto"
				}`))
			},
			field:   "truncation",
			wantRaw: `"auto"`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			env, losses, err := tc.run(t)
			if err != nil {
				t.Fatalf("RequestToCanonical: %v", err)
			}
			if env.Passthrough == nil {
				t.Fatalf("Passthrough nil; losses=%+v", losses)
			}
			got, ok := env.Passthrough.Extra[tc.field]
			if !ok {
				t.Fatalf("Passthrough.Extra missing %q; extra=%+v losses=%+v", tc.field, env.Passthrough.Extra, losses)
			}
			assertRawJSONEqual(t, got, tc.wantRaw)
		})
	}
}

func TestOpenAIResponsesClient_HappyPath_InputArrayWithMessage(t *testing.T) {
	adapter := &OpenAIResponsesClient{}
	body := []byte(`{
		"model":"gpt-4o",
		"instructions":"You are helpful.",
		"input":[
			{"type":"message","role":"user","content":[{"type":"input_text","text":"Hi"}]}
		]
	}`)
	env, _, err := adapter.RequestToCanonical(newTestOpenAIResponsesCtx(t), body)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if env.RequestControls.SystemPrompt != "You are helpful." {
		t.Errorf("SystemPrompt: %q", env.RequestControls.SystemPrompt)
	}
	// 期望 1 个 system + 1 个 user 文本节点
	if len(env.CapabilityGraph.Nodes) != 2 {
		t.Errorf("expected 2 text nodes (system + user), got %d", len(env.CapabilityGraph.Nodes))
	}
}

func TestOpenAIResponsesClient_FunctionCallThenOutputChain(t *testing.T) {
	adapter := &OpenAIResponsesClient{}
	body := []byte(`{
		"model":"gpt-4o",
		"input":[
			{"type":"message","role":"user","content":[{"type":"input_text","text":"weather?"}]},
			{"type":"function_call","call_id":"call_a","name":"get_weather","arguments":"{\"city\":\"SF\"}"},
			{"type":"function_call_output","call_id":"call_a","output":"sunny"}
		]
	}`)
	env, _, err := adapter.RequestToCanonical(newTestOpenAIResponsesCtx(t), body)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	tu, tr := 0, 0
	for _, n := range env.CapabilityGraph.Nodes {
		switch n.Kind {
		case CapabilityToolUse:
			tu++
		case CapabilityToolResult:
			tr++
		}
	}
	if tu != 1 || tr != 1 {
		t.Errorf("expected 1 tool_use + 1 tool_result, got %d/%d", tu, tr)
	}
	var foundEdge bool
	for _, e := range env.CapabilityGraph.Edges {
		if e.Type == EdgeRequires {
			foundEdge = true
		}
	}
	if !foundEdge {
		t.Error("expected EdgeRequires for function_call_output -> function_call")
	}
}

func TestOpenAIResponsesClient_BuiltinToolNativeRequired(t *testing.T) {
	adapter := &OpenAIResponsesClient{}
	body := []byte(`{
		"model":"gpt-4o",
		"input":"hi",
		"tools":[
			{"type":"function","name":"f1","description":"...","parameters":{}},
			{"type":"web_search"},
			{"type":"code_interpreter"}
		]
	}`)
	env, losses, err := adapter.RequestToCanonical(newTestOpenAIResponsesCtx(t), body)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(env.RequestControls.Tools) != 1 || env.RequestControls.Tools[0].Name != "f1" {
		t.Errorf("expected only function tool registered, got %+v", env.RequestControls.Tools)
	}
	var web, code bool
	for _, l := range losses {
		if l.Severity == "" {
			t.Errorf("loss must not be silent: %+v", l)
		}
		if strings.Contains(l.Reason, "web_search") {
			web = true
		}
		if strings.Contains(l.Reason, "code_interpreter") {
			code = true
		}
		if l.NativePath != "" && l.NativePath != "/v1/native/openai/responses" {
			t.Errorf("unexpected NativePath: %q", l.NativePath)
		}
	}
	if !web || !code {
		t.Errorf("expected both builtin tools to emit native_required losses, web=%v code=%v", web, code)
	}
}

func TestOpenAIResponsesClient_PreviousResponseIDToSessionHash(t *testing.T) {
	adapter := &OpenAIResponsesClient{}
	body := []byte(`{"model":"gpt-4o","input":"hi","previous_response_id":"resp_abc"}`)
	env, _, err := adapter.RequestToCanonical(newTestOpenAIResponsesCtx(t), body)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if env.RequestMeta.SessionHash != "resp_abc" {
		t.Errorf("expected SessionHash=resp_abc, got %q", env.RequestMeta.SessionHash)
	}
}

func TestOpenAIResponsesClient_StreamFlag(t *testing.T) {
	adapter := &OpenAIResponsesClient{}
	body := []byte(`{"model":"gpt-4o","input":"hi","stream":true}`)
	env, _, err := adapter.RequestToCanonical(newTestOpenAIResponsesCtx(t), body)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if env.StreamPlan.Mode != StreamModeStreaming {
		t.Errorf("expected streaming, got %s", env.StreamPlan.Mode)
	}
}

func TestOpenAIResponsesClient_RequestReasoningItemPreservesOpaqueState(t *testing.T) {
	adapter := &OpenAIResponsesClient{}
	body := []byte(`{
		"model":"gpt-4o",
		"input":[
			{
				"type":"reasoning",
				"id":"rs_abc",
				"status":"completed",
				"encrypted_content":"sig_openai_state",
				"summary":[{"type":"summary_text","text":"visible chain summary"}]
			},
			{"type":"message","role":"user","content":[{"type":"input_text","text":"continue"}]}
		]
	}`)
	env, losses, err := adapter.RequestToCanonical(newTestOpenAIResponsesCtx(t), body)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	for _, l := range losses {
		if strings.Contains(l.Code, "unknown_input_item") || strings.Contains(l.Reason, "unknown_input_item") ||
			strings.Contains(l.Code, "reasoning_pending") || strings.Contains(l.Reason, "reasoning_pending") {
			t.Fatalf("reasoning input item must be preserved without pending/unknown loss: %+v", losses)
		}
	}
	if len(env.Messages) != 2 {
		t.Fatalf("expected assistant reasoning + user message, got %+v", env.Messages)
	}
	if env.Messages[0].Role != "assistant" || len(env.Messages[0].Content) != 1 {
		t.Fatalf("assistant reasoning message: %+v", env.Messages[0])
	}
	block := env.Messages[0].Content[0]
	if block.Type != "thinking" || block.Thinking != "visible chain summary" || block.Text != "visible chain summary" || block.Signature != "sig_openai_state" {
		t.Fatalf("reasoning block not preserved: %+v", block)
	}
	if env.Messages[1].Role != "user" || env.Messages[1].Content[0].Text != "continue" {
		t.Fatalf("user continuation message: %+v", env.Messages[1])
	}

	var thinking *ThinkingNode
	for i := range env.CapabilityGraph.Nodes {
		if env.CapabilityGraph.Nodes[i].Kind == CapabilityThinking {
			thinking = env.CapabilityGraph.Nodes[i].Thinking
			break
		}
	}
	if thinking == nil {
		t.Fatal("expected CapabilityThinking node for reasoning item")
	}
	if thinking.Signature != "sig_openai_state" || thinking.Redaction != RedactionPublic {
		t.Fatalf("thinking metadata not preserved: %+v", thinking)
	}
	if len(thinking.Blocks) != 1 || thinking.Blocks[0].Thinking != "visible chain summary" || thinking.Blocks[0].Signature != "sig_openai_state" {
		t.Fatalf("thinking blocks not preserved: %+v", thinking.Blocks)
	}
}

// TestOpenAIResponsesClient_ImageInputBuildsNode 断言 Responses 的 input_image(裸字符串
// image_url)建 CapabilityImage 节点、text/image 顺序保留、无 d9 pending loss。此前只记
// d9_image_pending loss 把图丢了(HCSF 默认开时上游收不到图)——F4 视觉修复的 Responses 兄弟缺口。
// mutation 契约:parse 侧还原成"只记 loss 不建节点"→ 本测试全红(节点数=0 + 报 input_image loss)。
func TestOpenAIResponsesClient_ImageInputBuildsNode(t *testing.T) {
	adapter := &OpenAIResponsesClient{}
	body := []byte(`{
		"model":"gpt-4o",
		"input":[{"type":"message","role":"user","content":[
			{"type":"input_text","text":"see"},
			{"type":"input_image","image_url":"data:image/png;base64,` + testRedPixelPNGBase64 + `"}
		]}]
	}`)
	env, losses, err := adapter.RequestToCanonical(newTestOpenAIResponsesCtx(t), body)
	if err != nil {
		t.Fatalf("err: %v", err)
	}

	imgs := findImageNodes(env)
	if len(imgs) != 1 {
		t.Fatalf("CapabilityImage 节点 = %d, want 1(input_image 未建节点=图被丢)", len(imgs))
	}
	img := imgs[0].Image
	if img == nil {
		t.Fatal("image 节点缺 Image 载荷")
	}
	if img.SourceKind != DataSourceInlineBase64 {
		t.Errorf("SourceKind = %q, want inline_base64", img.SourceKind)
	}
	if img.MediaType != "image/png" {
		t.Errorf("MediaType = %q, want image/png", img.MediaType)
	}
	if img.Locator.Value != testRedPixelPNGBase64 {
		t.Errorf("Locator.Value 与原 base64 不逐字节相等: got=%q", img.Locator.Value)
	}

	// 消息 Content 顺序保留:text 在前、image 在后。
	if len(env.Messages) != 1 || len(env.Messages[0].Content) != 2 {
		t.Fatalf("messages/content 形状不对: %+v", env.Messages)
	}
	if env.Messages[0].Content[0].Type != "text" || env.Messages[0].Content[1].Type != "image" {
		t.Errorf("Content 顺序应为 text,image, got %+v", env.Messages[0].Content)
	}

	// 图已解析:不再有 input_image / d9_image_pending pending loss。
	for _, l := range losses {
		if strings.Contains(l.Reason, "input_image") || l.Code == "d9_image_pending" {
			t.Errorf("图已解析仍报 pending loss: %+v", l)
		}
	}
}

// TestOpenAIResponsesClient_ImageInputHTTPURL 断言非 data URI 的 image_url 字符串走
// SourceKind=url 原样透传(不误判为 data URI、不丢图)。
func TestOpenAIResponsesClient_ImageInputHTTPURL(t *testing.T) {
	adapter := &OpenAIResponsesClient{}
	body := []byte(`{
		"model":"gpt-4o",
		"input":[{"type":"message","role":"user","content":[
			{"type":"input_image","image_url":"https://x/y.png"}
		]}]
	}`)
	env, losses, err := adapter.RequestToCanonical(newTestOpenAIResponsesCtx(t), body)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	imgs := findImageNodes(env)
	if len(imgs) != 1 {
		t.Fatalf("CapabilityImage 节点 = %d, want 1", len(imgs))
	}
	img := imgs[0].Image
	if img.SourceKind != DataSourceURL || img.Locator.Value != "https://x/y.png" {
		t.Errorf("URL 图节点 = %+v, want url/https://x/y.png", img)
	}
	for _, l := range losses {
		if l.Code == "d9_image_pending" {
			t.Errorf("URL 图已解析仍报 pending loss: %+v", l)
		}
	}
}

func TestOpenAIResponsesClient_Negative_MissingModel(t *testing.T) {
	adapter := &OpenAIResponsesClient{}
	_, _, err := adapter.RequestToCanonical(newTestOpenAIResponsesCtx(t), []byte(`{"input":"hi"}`))
	if err == nil || !strings.Contains(err.Error(), "model") {
		t.Errorf("expected missing model error, got %v", err)
	}
}

func TestOpenAIResponsesClient_Negative_MissingInput(t *testing.T) {
	adapter := &OpenAIResponsesClient{}
	_, _, err := adapter.RequestToCanonical(newTestOpenAIResponsesCtx(t), []byte(`{"model":"gpt-4o"}`))
	if err == nil || !strings.Contains(err.Error(), "input") {
		t.Errorf("expected missing input error, got %v", err)
	}
}

func TestOpenAIResponsesClient_Negative_FunctionCallOutputUnknownID(t *testing.T) {
	adapter := &OpenAIResponsesClient{}
	body := []byte(`{
		"model":"gpt-4o",
		"input":[{"type":"function_call_output","call_id":"call_missing","output":"x"}]
	}`)
	_, _, err := adapter.RequestToCanonical(newTestOpenAIResponsesCtx(t), body)
	if err == nil || !strings.Contains(err.Error(), "unknown call_id") {
		t.Errorf("expected unknown call_id error, got %v", err)
	}
}

func TestOpenAIResponsesClient_Negative_MissingSeed(t *testing.T) {
	adapter := &OpenAIResponsesClient{}
	_, _, err := adapter.RequestToCanonical(context.Background(), []byte(`{"model":"gpt-4o","input":"hi"}`))
	if !errors.Is(err, ErrMissingRequestMetaSeed) {
		t.Errorf("expected ErrMissingRequestMetaSeed, got %v", err)
	}
}

// --------------------------------------------------------------------------
// --------------------------------------------------------------------------

func TestOpenAIResponses_D11_TextLifecycle(t *testing.T) {
	adapter := &OpenAIResponsesClient{}
	state := NewOpenAIResponsesStreamState()
	ctx := context.Background()

	chunks, _, err := adapter.CanonicalEventToClientChunk(ctx, &CanonicalEvent{
		Type: "message_start", MessageID: "resp_x", Model: "gpt-4o",
	}, state)
	if err != nil || len(chunks) != 1 {
		t.Fatalf("message_start: %v", err)
	}
	if !strings.Contains(string(chunks[0]), "event: response.created") {
		t.Errorf("expected response.created, got %s", chunks[0])
	}

	chunks, _, err = adapter.CanonicalEventToClientChunk(ctx, &CanonicalEvent{
		Type:         "content_block_start",
		Index:        0,
		ContentBlock: &CanonicalContentBlock{Type: "text"},
	}, state)
	if err != nil || len(chunks) != 2 {
		t.Fatalf("content_block_start chunks=%d err=%v", len(chunks), err)
	}
	if !strings.Contains(string(chunks[0]), "response.output_item.added") {
		t.Errorf("missing output_item.added: %s", chunks[0])
	}
	if !strings.Contains(string(chunks[1]), "response.content_part.added") {
		t.Errorf("missing content_part.added: %s", chunks[1])
	}

	chunks, _, err = adapter.CanonicalEventToClientChunk(ctx, &CanonicalEvent{
		Type:  "content_block_delta",
		Index: 0,
		Delta: &CanonicalContentDelta{Type: "text_delta", Text: "Hello"},
	}, state)
	if err != nil || len(chunks) != 1 {
		t.Fatalf("text delta: %v", err)
	}
	if !strings.Contains(string(chunks[0]), "response.output_text.delta") || !strings.Contains(string(chunks[0]), `"delta":"Hello"`) {
		t.Errorf("text delta payload wrong: %s", chunks[0])
	}

	chunks, _, err = adapter.CanonicalEventToClientChunk(ctx, &CanonicalEvent{
		Type: "message_stop", Usage: &CanonicalUsage{InputTokens: 5, OutputTokens: 1},
	}, state)
	if err != nil || len(chunks) < 3 {
		t.Fatalf("message_stop chunks=%d err=%v", len(chunks), err)
	}
	// chunks 应为：content_part.done + output_item.done + response.completed
	lastChunk := string(chunks[len(chunks)-1])
	if !strings.Contains(lastChunk, "response.completed") {
		t.Errorf("final chunk should be response.completed, got: %s", lastChunk)
	}
	if !strings.Contains(lastChunk, `"input_tokens":5`) {
		t.Errorf("usage missing in completed: %s", lastChunk)
	}
	if !state.Terminated {
		t.Error("state.Terminated must be true after message_stop")
	}
}

func TestOpenAIResponses_D11_DuplicateMessageStartSilenced(t *testing.T) {
	adapter := &OpenAIResponsesClient{}
	state := NewOpenAIResponsesStreamState()
	_, _, _ = adapter.CanonicalEventToClientChunk(context.Background(), &CanonicalEvent{Type: "message_start", MessageID: "r", Model: "x"}, state)
	chunks, losses, _ := adapter.CanonicalEventToClientChunk(context.Background(), &CanonicalEvent{Type: "message_start", MessageID: "r", Model: "x"}, state)
	if len(chunks) != 0 || len(losses) == 0 {
		t.Errorf("expected silenced + loss; got chunks=%d losses=%d", len(chunks), len(losses))
	}
}

func TestOpenAIResponses_D11_EventsAfterCompletedDropped(t *testing.T) {
	adapter := &OpenAIResponsesClient{}
	state := NewOpenAIResponsesStreamState()
	ctx := context.Background()
	_, _, _ = adapter.CanonicalEventToClientChunk(ctx, &CanonicalEvent{Type: "message_start", MessageID: "r", Model: "x"}, state)
	_, _, _ = adapter.CanonicalEventToClientChunk(ctx, &CanonicalEvent{Type: "message_stop"}, state)
	chunks, losses, _ := adapter.CanonicalEventToClientChunk(ctx, &CanonicalEvent{Type: "content_block_delta", Delta: &CanonicalContentDelta{Type: "text_delta", Text: "x"}}, state)
	if len(chunks) != 0 || len(losses) == 0 {
		t.Errorf("expected drop after completed; chunks=%d losses=%d", len(chunks), len(losses))
	}
}

// D11 tool_use 流式已实现:start 必须发 function_call output_item.added 且不再记
// pending loss(原测试钉旧 pending 行为,功能落地后翻转为回归守卫;完整生命周期
// 见 openai_responses_stream_tool_test.go)。
func TestOpenAIResponses_D11_ToolUseStartEmitsFunctionCallItem(t *testing.T) {
	adapter := &OpenAIResponsesClient{}
	state := NewOpenAIResponsesStreamState()
	_, _, _ = adapter.CanonicalEventToClientChunk(context.Background(), &CanonicalEvent{Type: "message_start", MessageID: "r", Model: "x"}, state)
	chunks, losses, _ := adapter.CanonicalEventToClientChunk(context.Background(), &CanonicalEvent{
		Type:         "content_block_start",
		ContentBlock: &CanonicalContentBlock{Type: "tool_use", CallID: "c", Name: "n"},
	}, state)
	if len(chunks) == 0 || len(losses) != 0 {
		t.Errorf("tool_use start 必须发 chunk 且零 loss; chunks=%d losses=%d", len(chunks), len(losses))
	}
	if len(chunks) > 0 && !strings.Contains(string(chunks[len(chunks)-1]), "function_call") {
		t.Errorf("缺 function_call item: %s", chunks[len(chunks)-1])
	}
}

func TestOpenAIResponses_D11_StateTypeMismatch(t *testing.T) {
	adapter := &OpenAIResponsesClient{}
	_, _, err := adapter.CanonicalEventToClientChunk(context.Background(), &CanonicalEvent{Type: "ping"}, "bad")
	if err == nil || !strings.Contains(err.Error(), "stream state type mismatch") {
		t.Errorf("expected state mismatch, got %v", err)
	}
}

func TestOpenAIResponses_D12_FinalizeBeforeMessageStop(t *testing.T) {
	adapter := &OpenAIResponsesClient{}
	state := NewOpenAIResponsesStreamState()
	ctx := context.Background()
	_, _, _ = adapter.CanonicalEventToClientChunk(ctx, &CanonicalEvent{Type: "message_start", MessageID: "r", Model: "x"}, state)
	_, _, _ = adapter.CanonicalEventToClientChunk(ctx, &CanonicalEvent{Type: "content_block_start", Index: 0, ContentBlock: &CanonicalContentBlock{Type: "text"}}, state)
	out, err := adapter.FinalizeClientStream(ctx, state)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(out) < 3 {
		t.Errorf("expected content_part.done + output_item.done + response.completed, got %d", len(out))
	}
	if !state.Terminated {
		t.Error("state.Terminated must be true after Finalize")
	}
}

func TestOpenAIResponses_D12_FinalizeIdempotent(t *testing.T) {
	adapter := &OpenAIResponsesClient{}
	state := NewOpenAIResponsesStreamState()
	ctx := context.Background()
	_, _, _ = adapter.CanonicalEventToClientChunk(ctx, &CanonicalEvent{Type: "message_start", MessageID: "r", Model: "x"}, state)
	_, _ = adapter.FinalizeClientStream(ctx, state)
	out, _ := adapter.FinalizeClientStream(ctx, state)
	if len(out) != 0 {
		t.Errorf("second Finalize must be no-op, got %d", len(out))
	}
}

func TestOpenAIResponses_D12_FinalizeBeforeStartEmpty(t *testing.T) {
	adapter := &OpenAIResponsesClient{}
	out, err := adapter.FinalizeClientStream(context.Background(), NewOpenAIResponsesStreamState())
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(out) != 0 {
		t.Errorf("Finalize before start must be empty, got %d", len(out))
	}
}

// --------------------------------------------------------------------------
// D10 CanonicalToClientResponse 测试
// --------------------------------------------------------------------------

func makeOpenAIResponsesBufferedEnv(content []CanonicalContentBlock, stop CanonicalStopReason) *HCSF {
	env := NewEmptyEnvelope()
	env.RequestMeta.RequestID = "req_test_d10"
	env.RequestMeta.ClientProtocol = ClientProtocolOpenAIResponses
	env.RequestMeta.ProtocolFamily = "openai"
	env.RequestMeta.IngressPath = "/v1/responses"
	env.RequestMeta.Model = "gpt-4o"
	env.BufferedResponse = &CanonicalResponse{
		ID:         "resp_test_001",
		Model:      "gpt-4o-2024-08-06",
		Content:    content,
		StopReason: stop,
		Usage:      CanonicalUsage{InputTokens: 25, OutputTokens: 7},
	}
	return env
}

func TestOpenAIResponsesClient_D10_HappyTextMessage(t *testing.T) {
	adapter := &OpenAIResponsesClient{}
	env := makeOpenAIResponsesBufferedEnv(
		[]CanonicalContentBlock{{Type: "text", Text: "Hello!"}},
		CanonicalStopEndTurn,
	)
	body, _, err := adapter.CanonicalToClientResponse(context.Background(), env)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	var out map[string]any
	_ = jsonUnmarshal(body, &out)
	if out["object"] != "response" || out["status"] != "completed" {
		t.Errorf("object/status: %v / %v", out["object"], out["status"])
	}
	outputs := out["output"].([]any)
	if len(outputs) != 1 {
		t.Fatalf("output len: %d", len(outputs))
	}
	msg := outputs[0].(map[string]any)
	if msg["type"] != "message" || msg["role"] != "assistant" {
		t.Errorf("message item: %+v", msg)
	}
	content := msg["content"].([]any)
	first := content[0].(map[string]any)
	if first["type"] != "output_text" || first["text"] != "Hello!" {
		t.Errorf("output_text: %+v", first)
	}
	usage := out["usage"].(map[string]any)
	if usage["total_tokens"].(float64) != 32 {
		t.Errorf("total_tokens: %v", usage["total_tokens"])
	}
}

func TestOpenAIResponsesClient_D10_FunctionCallOutputItem(t *testing.T) {
	adapter := &OpenAIResponsesClient{}
	env := makeOpenAIResponsesBufferedEnv(
		[]CanonicalContentBlock{
			{Type: "text", Text: "calling"},
			{Type: "tool_use", CallID: "call_a", Name: "get_x", Input: []byte(`{"q":"y"}`)},
		},
		CanonicalStopToolUse,
	)
	body, _, err := adapter.CanonicalToClientResponse(context.Background(), env)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	var out map[string]any
	_ = jsonUnmarshal(body, &out)
	outputs := out["output"].([]any)
	// 期望 message item + function_call item = 2 个 item
	if len(outputs) != 2 {
		t.Fatalf("output len: %d", len(outputs))
	}
	fc := outputs[1].(map[string]any)
	if fc["type"] != "function_call" || fc["call_id"] != "call_a" || fc["name"] != "get_x" {
		t.Errorf("function_call: %+v", fc)
	}
}

func TestOpenAIResponsesClient_D10_PreservesReasoningOutputItems(t *testing.T) {
	adapter := &OpenAIResponsesClient{}
	env := makeOpenAIResponsesBufferedEnv(
		[]CanonicalContentBlock{
			{Type: "thinking", Thinking: "visible chain summary", Signature: "sig_openai_state"},
			{Type: "text", Text: "final answer"},
		},
		CanonicalStopEndTurn,
	)
	body, losses, err := adapter.CanonicalToClientResponse(context.Background(), env)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	for _, l := range losses {
		if strings.Contains(l.Code, "reasoning_pending") || strings.Contains(l.Reason, "reasoning_pending") ||
			strings.Contains(l.Code, "unknown_response_block_type") || strings.Contains(l.Reason, "unknown_response_block_type") {
			t.Fatalf("reasoning output must be emitted without pending/unknown loss: %+v", losses)
		}
	}

	var out map[string]any
	_ = jsonUnmarshal(body, &out)
	outputs := out["output"].([]any)
	if len(outputs) != 2 {
		t.Fatalf("expected reasoning item + message item, got %d: %+v", len(outputs), outputs)
	}
	reasoning := outputs[0].(map[string]any)
	if reasoning["type"] != "reasoning" || reasoning["status"] != "completed" {
		t.Fatalf("reasoning item metadata: %+v", reasoning)
	}
	if reasoning["id"] == "" {
		t.Fatalf("reasoning item must have id: %+v", reasoning)
	}
	if reasoning["encrypted_content"] != "sig_openai_state" {
		t.Fatalf("encrypted_content not preserved: %+v", reasoning)
	}
	summary := reasoning["summary"].([]any)
	if len(summary) != 1 {
		t.Fatalf("summary len: %d", len(summary))
	}
	firstSummary := summary[0].(map[string]any)
	if firstSummary["type"] != "summary_text" || firstSummary["text"] != "visible chain summary" {
		t.Fatalf("summary not preserved: %+v", firstSummary)
	}
	msg := outputs[1].(map[string]any)
	if msg["type"] != "message" || msg["role"] != "assistant" {
		t.Fatalf("message item: %+v", msg)
	}
	content := msg["content"].([]any)
	first := content[0].(map[string]any)
	if first["type"] != "output_text" || first["text"] != "final answer" {
		t.Fatalf("output_text not preserved: %+v", first)
	}
}

func TestOpenAIResponsesClient_D10_IncompleteOnMaxTokens(t *testing.T) {
	adapter := &OpenAIResponsesClient{}
	env := makeOpenAIResponsesBufferedEnv(
		[]CanonicalContentBlock{{Type: "text", Text: "..."}},
		CanonicalStopMaxTokens,
	)
	body, _, err := adapter.CanonicalToClientResponse(context.Background(), env)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	var out map[string]any
	_ = jsonUnmarshal(body, &out)
	if out["status"] != "incomplete" {
		t.Errorf("status: %v", out["status"])
	}
	incomplete := out["incomplete_details"].(map[string]any)
	if incomplete["reason"] != "max_output_tokens" {
		t.Errorf("incomplete.reason: %v", incomplete["reason"])
	}
}

func TestOpenAIResponsesClient_D10_ReasoningTokensInUsageDetails(t *testing.T) {
	adapter := &OpenAIResponsesClient{}
	env := makeOpenAIResponsesBufferedEnv([]CanonicalContentBlock{{Type: "text", Text: "x"}}, CanonicalStopEndTurn)
	// 走真实非流式生产写路径: reasoning 落在 BufferedResponse.Usage(CanonicalUsage)上,
	// 而非 Accounting 顶层标量(生产从不写,旧 fixture 写它属 §14 非判别假绿)。
	env.BufferedResponse.Usage.ReasoningTokens = 42
	body, _, err := adapter.CanonicalToClientResponse(context.Background(), env)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	var out map[string]any
	_ = jsonUnmarshal(body, &out)
	usage := out["usage"].(map[string]any)
	od := usage["output_tokens_details"].(map[string]any)
	if od["reasoning_tokens"].(float64) != 42 {
		t.Errorf("expected reasoning_tokens=42, got %v", od["reasoning_tokens"])
	}
}

// TestOpenAIResponsesClient_NonStreamPassthroughMerged 守护 #4: 非流式 OpenAI Responses 响应必须把
// 上游顶层透传字段(typed struct 未建模, 如 service_tier)合并回客户端响应体。判别性: 删除
// CanonicalToClientResponse 末尾的 MergeExtrasInto 调用 → 字段丢失 → 本测试转红。
func TestOpenAIResponsesClient_NonStreamPassthroughMerged(t *testing.T) {
	adapter := &OpenAIResponsesClient{}
	env := makeOpenAIResponsesBufferedEnv([]CanonicalContentBlock{{Type: "text", Text: "x"}}, CanonicalStopEndTurn)
	env.BufferedResponse.Passthrough = &PassthroughEnvelope{Extra: map[string]json.RawMessage{
		"service_tier": json.RawMessage(`"scale"`),
	}}
	body, _, err := adapter.CanonicalToClientResponse(context.Background(), env)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	var out map[string]any
	_ = jsonUnmarshal(body, &out)
	if out["service_tier"] != "scale" {
		t.Fatalf("期望上游 service_tier=scale 被合并进响应, 实际 %v (body=%s)", out["service_tier"], body)
	}
	// typed 字段不被透传覆盖(仍是合法 responses 形态)。
	if out["object"] != "response" {
		t.Fatalf("typed object 字段被透传覆盖: %v", out["object"])
	}
}

func TestOpenAIResponsesClient_D10_Negative_NilEnvelope(t *testing.T) {
	adapter := &OpenAIResponsesClient{}
	_, _, err := adapter.CanonicalToClientResponse(context.Background(), nil)
	if err == nil || !strings.Contains(err.Error(), "nil envelope") {
		t.Errorf("expected nil envelope error, got %v", err)
	}
}

func TestOpenAIResponsesClient_D10_Negative_NoBuffered(t *testing.T) {
	adapter := &OpenAIResponsesClient{}
	_, _, err := adapter.CanonicalToClientResponse(context.Background(), NewEmptyEnvelope())
	if err == nil || !strings.Contains(err.Error(), "buffered_response") {
		t.Errorf("expected no buffered_response error, got %v", err)
	}
}

func TestOpenAIResponsesClient_EnvelopeIsValidateReady(t *testing.T) {
	adapter := &OpenAIResponsesClient{}
	body := []byte(`{"model":"gpt-4o","instructions":"sys","input":"hi"}`)
	env, _, err := adapter.RequestToCanonical(newTestOpenAIResponsesCtx(t), body)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if err := ValidateEnvelopeVersionGuard(env); err != nil {
		t.Fatalf("ValidateEnvelopeVersionGuard: %v", err)
	}
}
