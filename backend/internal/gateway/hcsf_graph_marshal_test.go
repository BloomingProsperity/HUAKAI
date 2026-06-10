package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/proto"
	geminiproto "github.com/BloomingProsperity/HUAKAI/internal/proto/gemini"
)

func marshalBody(t *testing.T, env *proto.HCSF, family string) map[string]any {
	t.Helper()
	raw, err := MarshalToProviderRequest(env, family)
	if err != nil {
		t.Fatalf("MarshalToProviderRequest(%s): %v", family, err)
	}
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("body json: %v\n%s", err, raw)
	}
	return body
}

func graphEnv(nodes ...proto.CapabilityNode) *proto.HCSF {
	env := proto.NewEmptyEnvelope()
	env.RequestMeta.Model = "model-in"
	env.RequestMeta.UpstreamModel = "model-up"
	env.CapabilityGraph.Nodes = nodes
	return env
}

func anthropicRequestEnv(t *testing.T, raw string) *proto.HCSF {
	t.Helper()
	adapter := &proto.AnthropicMessagesClient{}
	ctx := proto.ContextWithRequestMetaSeed(context.Background(), proto.RequestMetaSeed{
		RequestID:      "req_gateway_marshal",
		ClientProtocol: proto.ClientProtocolAnthropicMessages,
		ProtocolFamily: "anthropic",
		IngressPath:    "/v1/messages",
		EvidenceLabel:  proto.EvidenceMock,
	})
	env, losses, err := adapter.RequestToCanonical(ctx, []byte(raw))
	if err != nil {
		t.Fatalf("RequestToCanonical: %v", err)
	}
	if len(losses) != 0 {
		t.Fatalf("unexpected RequestToCanonical losses: %+v", losses)
	}
	return env
}

func textNode(id, role, text string) proto.CapabilityNode {
	return proto.CapabilityNode{ID: id, Kind: proto.CapabilityText, StreamReady: proto.StreamReadyYes, Text: &proto.TextNode{Role: role, Block: proto.CanonicalContentBlock{Type: "text", Text: text}}}
}

func toolUseNode() proto.CapabilityNode {
	return proto.CapabilityNode{ID: "n_tool_use_1", Kind: proto.CapabilityToolUse, StreamReady: proto.StreamReadyPartial, ToolUse: &proto.ToolUseNode{ToolCallID: "call_1", Name: "lookup", Input: json.RawMessage(`{"q":"x"}`), Status: proto.ToolNodeComplete}}
}

func toolResultNode() proto.CapabilityNode {
	return proto.CapabilityNode{ID: "n_tool_result_1", Kind: proto.CapabilityToolResult, StreamReady: proto.StreamReadyYes, ToolResult: &proto.ToolResultNode{ToolCallID: "call_1", Content: []proto.CanonicalContentBlock{{Type: "text", Text: "ok"}}, Status: proto.ToolNodeComplete}}
}

func imageNode() proto.CapabilityNode {
	return proto.CapabilityNode{ID: "n_image_1", Kind: proto.CapabilityImage, StreamReady: proto.StreamReadyYes, Image: &proto.ImageNode{SourceKind: proto.DataSourceURL, MediaType: "image/png", Locator: proto.DataLocator{Kind: proto.DataSourceURL, Value: "https://example.test/i.png"}}}
}

func inlineImageNode() proto.CapabilityNode {
	return proto.CapabilityNode{
		ID:          "n_image_inline_1",
		Kind:        proto.CapabilityImage,
		StreamReady: proto.StreamReadyYes,
		Image: &proto.ImageNode{
			SourceKind: proto.DataSourceInlineBase64,
			MediaType:  "image/png",
			Locator: proto.DataLocator{
				Kind:  proto.DataSourceInlineBase64,
				Value: "iVBORw0KGgo=",
			},
		},
	}
}

func thinkingNode() proto.CapabilityNode {
	return proto.CapabilityNode{ID: "n_thinking_1", Kind: proto.CapabilityThinking, StreamReady: proto.StreamReadyPartial, Thinking: &proto.ThinkingNode{Redaction: proto.RedactionPublic, Blocks: []proto.CanonicalContentBlock{{Type: "text", Text: "visible thought"}}}}
}

func thinkingBudgetNode() proto.CapabilityNode {
	return proto.CapabilityNode{
		ID:          "n_thinking_budget_1",
		Kind:        proto.CapabilityThinking,
		StreamReady: proto.StreamReadyPartial,
		Thinking: &proto.ThinkingNode{
			BudgetTokens: 1024,
			Blocks:       []proto.CanonicalContentBlock{},
			Redaction:    proto.RedactionProviderOnly,
		},
	}
}

func assistantThinkingNode() proto.CapabilityNode {
	mi, bi := 1, 0
	node := thinkingNode()
	node.Source = &proto.NodeSourceRef{MessageIndex: &mi, BlockIndex: &bi}
	return node
}

func cacheNode(ref string) proto.CapabilityNode {
	return proto.CapabilityNode{ID: "n_cache_1", Kind: proto.CapabilityCacheControl, StreamReady: proto.StreamReadyNo, CacheControl: &proto.CacheControlNode{Scope: proto.CacheScopeBlock, BreakpointRefs: []string{ref}, SanitizeSystemMetadata: true}}
}

func msg0(body map[string]any) map[string]any {
	return body["messages"].([]any)[0].(map[string]any)
}

func content0(msg map[string]any) map[string]any {
	return msg["content"].([]any)[0].(map[string]any)
}

func responseInput0(body map[string]any) map[string]any {
	return body["input"].([]any)[0].(map[string]any)
}

func TestMarshalTextOnlyAllFamilies(t *testing.T) {
	for _, family := range []string{"anthropic_messages", "openai_chat", "openai_responses"} {
		body := marshalBody(t, graphEnv(textNode("n_text_1", "user", "hi")), family)
		switch family {
		case "anthropic_messages":
			if content0(msg0(body))["text"] != "hi" {
				t.Fatalf("anthropic body = %+v", body)
			}
		case "openai_chat":
			if msg0(body)["content"] != "hi" {
				t.Fatalf("openai_chat body = %+v", body)
			}
		case "openai_responses":
			part := responseInput0(body)["content"].([]any)[0].(map[string]any)
			if part["type"] != "input_text" || part["text"] != "hi" {
				t.Fatalf("responses body = %+v", body)
			}
		}
	}
}

func TestMarshalSystemPlacement(t *testing.T) {
	nodes := []proto.CapabilityNode{textNode("n_system_1", "system", "sys"), textNode("n_text_1", "user", "hi")}
	anthropic := marshalBody(t, graphEnv(nodes...), "anthropic_messages")
	if anthropic["system"] != "sys" {
		t.Fatalf("anthropic system = %+v", anthropic["system"])
	}
	chat := marshalBody(t, graphEnv(nodes...), "openai_chat")
	if msg0(chat)["role"] != "system" || msg0(chat)["content"] != "sys" {
		t.Fatalf("openai_chat system = %+v", chat)
	}
	responses := marshalBody(t, graphEnv(nodes...), "openai_responses")
	if responses["instructions"] != "sys" {
		t.Fatalf("responses instructions = %+v", responses)
	}
}

func TestMarshalToolUseAndResultAnthropic(t *testing.T) {
	body := marshalBody(t, graphEnv(toolUseNode(), toolResultNode()), "anthropic_messages")
	first := content0(msg0(body))
	if first["type"] != "tool_use" || first["id"] != "call_1" || first["name"] != "lookup" {
		t.Fatalf("anthropic tool_use = %+v", body)
	}
	second := content0(body["messages"].([]any)[1].(map[string]any))
	if second["type"] != "tool_result" || second["tool_use_id"] != "call_1" {
		t.Fatalf("anthropic tool_result = %+v", body)
	}
}

func TestMarshalToolUseAndResultOpenAIChat(t *testing.T) {
	body := marshalBody(t, graphEnv(toolUseNode(), toolResultNode()), "openai_chat")
	toolCalls := msg0(body)["tool_calls"].([]any)
	if toolCalls[0].(map[string]any)["type"] != "function" {
		t.Fatalf("openai_chat tool_calls = %+v", body)
	}
	second := body["messages"].([]any)[1].(map[string]any)
	if second["role"] != "tool" || second["tool_call_id"] != "call_1" || second["content"] != "ok" {
		t.Fatalf("openai_chat tool result = %+v", body)
	}
}

func TestMarshalToolUseAndResultOpenAIResponses(t *testing.T) {
	body := marshalBody(t, graphEnv(toolUseNode(), toolResultNode()), "openai_responses")
	in := body["input"].([]any)
	if in[0].(map[string]any)["type"] != "function_call" || in[1].(map[string]any)["type"] != "function_call_output" {
		t.Fatalf("responses tools = %+v", body)
	}
}

// TestMarshalGeminiMessages guards the Gemini egress graph projection.
// MUTATION: leaving assistant as "assistant" instead of Gemini "model", or
// dropping functionCall/functionResponse parts, must make this test fail.
func TestMarshalGeminiMessages(t *testing.T) {
	temp := 0.25
	topP := 0.75
	maxTokens := 128
	env := graphEnv(
		textNode("n_system_1", "system", "be concise"),
		textNode("n_user_1", "user", "hello"),
		inlineImageNode(),
		textNode("n_assistant_1", "assistant", "I can help"),
		toolUseNode(),
		toolResultNode(),
		thinkingBudgetNode(),
	)
	env.RequestControls = proto.RequestControls{
		Temperature: &temp,
		TopP:        &topP,
		MaxTokens:   &maxTokens,
		Stop:        []string{"END"},
	}

	body := marshalBody(t, env, "gemini_messages")
	system, ok := body["systemInstruction"].(map[string]any)
	if !ok {
		t.Fatalf("Gemini systemInstruction missing or wrong shape: %+v", body)
	}
	systemParts := system["parts"].([]any)
	if systemParts[0].(map[string]any)["text"] != "be concise" {
		t.Fatalf("Gemini systemInstruction = %+v", body)
	}

	contents, ok := body["contents"].([]any)
	if !ok {
		t.Fatalf("Gemini contents missing or wrong shape: %+v", body)
	}
	if len(contents) != 3 {
		t.Fatalf("Gemini contents len = %d, body=%+v", len(contents), body)
	}
	user := contents[0].(map[string]any)
	if user["role"] != "user" {
		t.Fatalf("Gemini user role = %v, body=%+v", user["role"], body)
	}
	userParts := user["parts"].([]any)
	if userParts[0].(map[string]any)["text"] != "hello" {
		t.Fatalf("Gemini user text part = %+v", userParts[0])
	}
	inlineData := userParts[1].(map[string]any)["inlineData"].(map[string]any)
	if inlineData["mimeType"] != "image/png" || inlineData["data"] != "iVBORw0KGgo=" {
		t.Fatalf("Gemini inlineData = %+v", inlineData)
	}

	model := contents[1].(map[string]any)
	if model["role"] != "model" {
		t.Fatalf("Gemini assistant role = %v, want model; body=%+v", model["role"], body)
	}
	modelParts := model["parts"].([]any)
	if modelParts[0].(map[string]any)["text"] != "I can help" {
		t.Fatalf("Gemini model text part = %+v", modelParts[0])
	}
	functionCall := modelParts[1].(map[string]any)["functionCall"].(map[string]any)
	if functionCall["name"] != "lookup" || functionCall["id"] != "call_1" {
		t.Fatalf("Gemini functionCall = %+v", functionCall)
	}
	if functionCall["args"].(map[string]any)["q"] != "x" {
		t.Fatalf("Gemini functionCall args = %+v", functionCall["args"])
	}

	toolResponse := contents[2].(map[string]any)
	if toolResponse["role"] != "user" {
		t.Fatalf("Gemini tool response role = %v, want user; body=%+v", toolResponse["role"], body)
	}
	functionResponse := toolResponse["parts"].([]any)[0].(map[string]any)["functionResponse"].(map[string]any)
	if functionResponse["name"] != "lookup" {
		t.Fatalf("Gemini functionResponse name = %+v", functionResponse)
	}
	response := functionResponse["response"].(map[string]any)
	if response["content"] != "ok" {
		t.Fatalf("Gemini functionResponse response = %+v", response)
	}

	generation := body["generationConfig"].(map[string]any)
	if generation["temperature"] != 0.25 || generation["topP"] != 0.75 || generation["maxOutputTokens"].(float64) != 128 {
		t.Fatalf("Gemini generationConfig controls = %+v", generation)
	}
	if generation["stopSequences"].([]any)[0] != "END" {
		t.Fatalf("Gemini stopSequences = %+v", generation["stopSequences"])
	}
	thinking := generation["thinkingConfig"].(map[string]any)
	if thinking["thinkingBudget"].(float64) != 1024 {
		t.Fatalf("Gemini thinkingConfig = %+v", thinking)
	}
}

// TestGeminiIngressToAnthropicUpstream proves Gemini-native ingress produces
// canonical roles that can be projected into a non-Gemini upstream.
// MUTATION: keeping Gemini's "model" role instead of canonical assistant must
// make the Anthropic upstream role assertion fail.
func TestGeminiIngressToAnthropicUpstream(t *testing.T) {
	client := &geminiproto.GeminiClient{}
	ctx := proto.ContextWithRequestMetaSeed(context.Background(), proto.RequestMetaSeed{
		RequestID:      "req_gateway_gemini_loop",
		ClientProtocol: proto.ClientProtocolGemini,
		ProtocolFamily: "gemini_messages",
		IngressPath:    "/v1beta/models/gemini-pro:generateContent",
		Model:          "gemini-pro",
		EvidenceLabel:  proto.EvidenceMock,
	})
	env, losses, err := client.RequestToCanonical(ctx, []byte(`{
		"contents":[{"role":"model","parts":[{"text":"already answered"}]}],
		"systemInstruction":{"parts":[{"text":"operator policy"}]}
	}`))
	if err != nil {
		t.Fatalf("Gemini RequestToCanonical: %v", err)
	}
	if len(losses) != 0 {
		t.Fatalf("unexpected Gemini ingress losses: %+v", losses)
	}

	body := marshalBody(t, env, "anthropic_messages")
	if body["system"] != "operator policy" {
		t.Fatalf("Anthropic system = %+v, body=%+v", body["system"], body)
	}
	messages := body["messages"].([]any)
	if len(messages) != 1 {
		t.Fatalf("Anthropic messages len = %d, body=%+v", len(messages), body)
	}
	msg := messages[0].(map[string]any)
	if msg["role"] != "assistant" {
		t.Fatalf("Anthropic message role = %v, want assistant; body=%+v", msg["role"], body)
	}
	block := msg["content"].([]any)[0].(map[string]any)
	if block["type"] != "text" || block["text"] != "already answered" {
		t.Fatalf("Anthropic content block = %+v", block)
	}
}

func TestMarshalImageAllFamilies(t *testing.T) {
	anthropic := marshalBody(t, graphEnv(imageNode()), "anthropic_messages")
	if content0(msg0(anthropic))["source"].(map[string]any)["type"] != "url" {
		t.Fatalf("anthropic image = %+v", anthropic)
	}
	chat := marshalBody(t, graphEnv(imageNode()), "openai_chat")
	if content0(msg0(chat))["type"] != "image_url" {
		t.Fatalf("openai_chat image = %+v", chat)
	}
	responses := marshalBody(t, graphEnv(imageNode()), "openai_responses")
	part := responseInput0(responses)["content"].([]any)[0].(map[string]any)
	if part["type"] != "input_image" {
		t.Fatalf("responses image = %+v", responses)
	}
}

func TestMarshalThinkingFamilyBehavior(t *testing.T) {
	anthropic := marshalBody(t, graphEnv(thinkingNode()), "anthropic_messages")
	if content0(msg0(anthropic))["type"] != "thinking" {
		t.Fatalf("anthropic thinking = %+v", anthropic)
	}
	chatEnv := graphEnv(thinkingNode())
	_ = marshalBody(t, chatEnv, "openai_chat")
	if len(chatEnv.CapabilityGraph.ProtocolLoss) == 0 {
		t.Fatal("openai_chat thinking must emit loss")
	}
	responses := marshalBody(t, graphEnv(thinkingNode()), "openai_responses")
	if responseInput0(responses)["type"] != "reasoning" {
		t.Fatalf("responses thinking = %+v", responses)
	}
}

func TestMarshalThinkingFamilyPreservesContinuationState(t *testing.T) {
	node := proto.CapabilityNode{
		ID:          "n_thinking_sig",
		Kind:        proto.CapabilityThinking,
		StreamReady: proto.StreamReadyPartial,
		Thinking: &proto.ThinkingNode{
			Redaction: proto.RedactionPublic,
			Signature: "sig_openai_state",
			Blocks: []proto.CanonicalContentBlock{{
				Type:      "thinking",
				Text:      "visible thought",
				Thinking:  "visible thought",
				Signature: "sig_openai_state",
			}},
		},
	}

	anthropic := marshalBody(t, graphEnv(node), "anthropic_messages")
	anthropicBlock := content0(msg0(anthropic))
	if anthropicBlock["thinking"] != "visible thought" || anthropicBlock["signature"] != "sig_openai_state" {
		t.Fatalf("anthropic thinking continuation state = %+v", anthropicBlock)
	}

	responses := marshalBody(t, graphEnv(node), "openai_responses")
	responsesItem := responseInput0(responses)
	if responsesItem["type"] != "reasoning" || responsesItem["encrypted_content"] != "sig_openai_state" {
		t.Fatalf("responses reasoning continuation state = %+v", responsesItem)
	}
	summary := responsesItem["summary"].([]any)
	if len(summary) != 1 {
		t.Fatalf("responses summary len: %d", len(summary))
	}
	first := summary[0].(map[string]any)
	if first["type"] != "summary_text" || first["text"] != "visible thought" {
		t.Fatalf("responses reasoning summary = %+v", first)
	}
}

func TestMarshalAnthropicMessagesPreservesTopLevelThinkingControl(t *testing.T) {
	env := anthropicRequestEnv(t, `{
		"model":"claude-3-5-sonnet-20241022",
		"max_tokens":4096,
		"thinking":{"type":"enabled","budget_tokens":2048},
		"messages":[{"role":"user","content":"hi"}]
	}`)
	body := marshalBody(t, env, "anthropic_messages")
	thinking, ok := body["thinking"].(map[string]any)
	if !ok {
		t.Fatalf("missing top-level thinking control: %+v", body)
	}
	if thinking["type"] != "enabled" || thinking["budget_tokens"].(float64) != 2048 {
		t.Fatalf("thinking control mismatch: %+v", thinking)
	}
	if len(body["messages"].([]any)) != 1 || content0(msg0(body))["text"] != "hi" {
		t.Fatalf("message projection changed while preserving thinking: %+v", body)
	}
}

func TestMarshalAnthropicMessagesOmitsThinkingWhenRequestHasNone(t *testing.T) {
	env := anthropicRequestEnv(t, `{
		"model":"claude-3-5-sonnet-20241022",
		"max_tokens":4096,
		"messages":[{"role":"user","content":"hi"}]
	}`)
	body := marshalBody(t, env, "anthropic_messages")
	if _, ok := body["thinking"]; ok {
		t.Fatalf("unexpected top-level thinking control: %+v", body)
	}
	if len(body["messages"].([]any)) != 1 || content0(msg0(body))["text"] != "hi" {
		t.Fatalf("message projection changed without thinking: %+v", body)
	}
}

func TestMarshalAnthropicMessagesAssistantThinkingRemainsContentBlock(t *testing.T) {
	body := marshalBody(t, graphEnv(assistantThinkingNode()), "anthropic_messages")
	if _, ok := body["thinking"]; ok {
		t.Fatalf("assistant thinking content block must not become top-level thinking: %+v", body)
	}
	block := content0(msg0(body))
	if block["type"] != "thinking" || block["thinking"] != "visible thought" {
		t.Fatalf("assistant thinking block mismatch: %+v", body)
	}
}

func TestMarshalCacheControlFamilyBehavior(t *testing.T) {
	nodes := []proto.CapabilityNode{textNode("n_text_1", "user", "cache me"), cacheNode("n_text_1")}
	anthropic := marshalBody(t, graphEnv(nodes...), "anthropic_messages")
	if content0(msg0(anthropic))["cache_control"].(map[string]any)["type"] != "ephemeral" {
		t.Fatalf("anthropic cache_control = %+v", anthropic)
	}
	for _, family := range []string{"openai_chat", "openai_responses"} {
		env := graphEnv(nodes...)
		_ = marshalBody(t, env, family)
		if len(env.CapabilityGraph.ProtocolLoss) == 0 {
			t.Fatalf("%s cache_control must emit loss", family)
		}
	}
}

func TestMarshalUnknownNodeEmitsLoss(t *testing.T) {
	env := graphEnv(proto.CapabilityNode{ID: "n_future_1", Kind: proto.CapabilityKind("future_node"), StreamReady: proto.StreamReadyNo})
	_ = marshalBody(t, env, "openai_chat")
	if len(env.CapabilityGraph.ProtocolLoss) != 1 || env.CapabilityGraph.ProtocolLoss[0].Code != "unsupported_capability" {
		t.Fatalf("losses = %+v", env.CapabilityGraph.ProtocolLoss)
	}
}

func TestMarshalOpenAIResponsesNativeExtensionPassthrough(t *testing.T) {
	env := graphEnv(textNode("n_text_1", "user", "hi"))
	env.Extensions = map[string]json.RawMessage{
		"openai_responses:native_body": json.RawMessage(`{"previous_response_id":"resp_1","reasoning":{"effort":"high"}}`),
	}
	body := marshalBody(t, env, "openai_responses")
	if body["previous_response_id"] != "resp_1" || body["reasoning"].(map[string]any)["effort"] != "high" {
		t.Fatalf("responses native passthrough = %+v", body)
	}
}

// TestInjectRequestControlsResponseFormatRawPassthrough_OpenAIChat 守 P2-B 修复:
// inbound response_format = {"type":"json_object"} 时,injectRequestControls
// 必须把出站 body 的 response_format 字段还原为 {"type":"json_object"} 本体,
// 不能再包 {"type":"raw","schema":{"type":"json_object"}} — 上游 OpenAI 只接
// type 为 "json_object" / "json_schema",包错壳会 4xx reject 结构化输出请求。
//
// 注意:测试直接调 injectRequestControls 而非走 marshalBody/MarshalToProviderRequest,
// 因为 ResponseFormat 注入是 dispatcher path (upstream_dispatcher_hcsf.go:173)
// 才调的,marshalBody 跳过该步。
//
// Mutation:把 injectRequestControls 改回原 wrap 逻辑时本用例必红 —
// response_format.type 变 "raw",response_format.schema 会出现。
func TestInjectRequestControlsResponseFormatRawPassthrough_OpenAIChat(t *testing.T) {
	raw := json.RawMessage(`{"type":"json_object"}`)
	env := &proto.HCSF{
		RequestControls: proto.RequestControls{
			ResponseFormat: &proto.ResponseFormat{Type: "raw", Schema: raw},
		},
	}
	out, err := injectRequestControls([]byte(`{}`), env, "openai_chat")
	if err != nil {
		t.Fatalf("injectRequestControls err=%v", err)
	}
	var body map[string]any
	if err := json.Unmarshal(out, &body); err != nil {
		t.Fatalf("unmarshal: %v body=%s", err, out)
	}
	rf, ok := body["response_format"].(map[string]any)
	if !ok {
		t.Fatalf("response_format must be JSON object, got %T: %v", body["response_format"], body["response_format"])
	}
	if rf["type"] != "json_object" {
		t.Fatalf("response_format.type = %v, want inbound 'json_object' (raw-wrap regression)", rf["type"])
	}
	if _, hasSchema := rf["schema"]; hasSchema {
		t.Fatalf("response_format 出现 'schema' key 是 raw-wrap regression,上游 OpenAI 会 4xx: body=%+v", body)
	}
}

// TestInjectRequestControlsResponseFormatRawPassthrough_OpenAIResponses
// 守 P2-B 在 Responses 协议侧。inbound text =
// {"format":{"type":"json_schema","json_schema":{...}}} 时出站 body 的 text
// 字段必须 1:1 还原,不能被包成 {"format":{"type":"raw","schema":...}}。
//
// Mutation:改回原 wrap 逻辑时 text.format.type 会变 "raw" 而非 inbound
// 'json_schema',或多嵌套一层 'schema' 包壳。
func TestInjectRequestControlsResponseFormatRawPassthrough_OpenAIResponses(t *testing.T) {
	raw := json.RawMessage(`{"format":{"type":"json_schema","json_schema":{"name":"Person","schema":{"type":"object"}}}}`)
	env := &proto.HCSF{
		RequestControls: proto.RequestControls{
			ResponseFormat: &proto.ResponseFormat{Type: "raw", Schema: raw},
		},
	}
	out, err := injectRequestControls([]byte(`{}`), env, "openai_responses")
	if err != nil {
		t.Fatalf("injectRequestControls err=%v", err)
	}
	var body map[string]any
	if err := json.Unmarshal(out, &body); err != nil {
		t.Fatalf("unmarshal: %v body=%s", err, out)
	}
	text, ok := body["text"].(map[string]any)
	if !ok {
		t.Fatalf("text must be JSON object, got %T: %v", body["text"], body["text"])
	}
	format, ok := text["format"].(map[string]any)
	if !ok {
		t.Fatalf("text.format must be object, got %T: %v", text["format"], text["format"])
	}
	if format["type"] != "json_schema" {
		t.Fatalf("text.format.type = %v, want inbound 'json_schema' (raw-wrap regression)", format["type"])
	}
	if _, hasOuter := format["schema"]; hasOuter {
		t.Fatalf("text.format 出现包壳 'schema' 字段是 raw-wrap regression: body=%+v", body)
	}
}

func TestInjectRequestControlsMergesRequestPassthrough(t *testing.T) {
	max := 12
	env := &proto.HCSF{
		RequestControls: proto.RequestControls{MaxTokens: &max},
		Passthrough: &proto.PassthroughEnvelope{Extra: map[string]json.RawMessage{
			"max_tool_calls":    json.RawMessage(`3`),
			"prompt_cache_key":  json.RawMessage(`"tenant-a:stable-prefix"`),
			"max_output_tokens": json.RawMessage(`999`),
		}},
	}
	out, err := injectRequestControls([]byte(`{}`), env, "openai_responses")
	if err != nil {
		t.Fatalf("injectRequestControls err=%v", err)
	}
	var body map[string]any
	if err := json.Unmarshal(out, &body); err != nil {
		t.Fatalf("unmarshal: %v body=%s", err, out)
	}
	maxToolCalls, ok := body["max_tool_calls"].(float64)
	if !ok || maxToolCalls != 3 {
		t.Fatalf("max_tool_calls passthrough lost: %+v", body)
	}
	if body["prompt_cache_key"] != "tenant-a:stable-prefix" {
		t.Fatalf("prompt_cache_key passthrough lost: %+v", body)
	}
	maxOutputTokens, ok := body["max_output_tokens"].(float64)
	if !ok || maxOutputTokens != 12 {
		t.Fatalf("modeled max_output_tokens should win over passthrough conflict: %+v", body)
	}
}

func TestMarshalOpenAIChatReasoningEffortRequestControl(t *testing.T) {
	env := graphEnv(
		textNode("n_text_1", "user", "solve"),
		proto.CapabilityNode{
			ID:          "n_thinking_request_1",
			Kind:        proto.CapabilityThinking,
			StreamReady: proto.StreamReadyPartial,
			Source:      &proto.NodeSourceRef{RequestField: "reasoning_effort"},
			Thinking: &proto.ThinkingNode{
				BudgetTokens: 4096,
				Blocks:       []proto.CanonicalContentBlock{},
				Redaction:    proto.RedactionProviderOnly,
			},
		},
	)
	env.Passthrough = &proto.PassthroughEnvelope{Extra: map[string]json.RawMessage{
		"reasoning_effort": json.RawMessage(`"high"`),
	}}
	raw, err := MarshalToProviderRequest(env, "openai_chat")
	if err != nil {
		t.Fatalf("MarshalToProviderRequest: %v", err)
	}
	if len(env.CapabilityGraph.ProtocolLoss) != 0 {
		t.Fatalf("reasoning_effort request control should not produce marshal loss: %+v", env.CapabilityGraph.ProtocolLoss)
	}
	out, err := injectRequestControls(raw, env, "openai_chat")
	if err != nil {
		t.Fatalf("injectRequestControls: %v", err)
	}
	var body map[string]any
	if err := json.Unmarshal(out, &body); err != nil {
		t.Fatalf("unmarshal: %v body=%s", err, out)
	}
	if body["reasoning_effort"] != "high" {
		t.Fatalf("reasoning_effort passthrough lost: %+v", body)
	}
}

// TestMarshalCompatFamiliesProjectToOpenAIChat 守卫 renew-156 同源的第 4 处
// 族集不对称:MarshalToProviderRequest 必须认识全部 OpenAI 兼容直通族 +
// JSON 形 session 反转族,并把它们投影成与 openai_chat 完全相同的请求 body。
// 此前 kimi/qwen/glm/yi/baichuan/doubao/ernie/step/hunyuan/minimax/cohere/
// ollama 12 族不在映射表里 → 非流式 HCSF 请求 marshal 直接报 unsupported
// (502);流式路径(streamingProviderRequestBody)传原始族名,全部 20 兼容族
// marshal 失败 → 501,投递前就挂。
// 判别性:断言输出与 openai_chat 投影逐字节相等——映射错指到别的形态族
// (anthropic/gemini)会产生不同 body,必红;漏映射直接 error,必红。
// Mutation:从 hcsfProviderRequestModelFamily 删任一族 → 对应子断言红。
func TestMarshalCompatFamiliesProjectToOpenAIChat(t *testing.T) {
	env := graphEnv(textNode("n1", "user", "hello"))
	want, err := MarshalToProviderRequest(env, "openai_chat")
	if err != nil {
		t.Fatalf("baseline openai_chat marshal: %v", err)
	}
	for _, fam := range []string{
		"openrouter_chat", "grok_chat", "deepseek_chat", "mistral_chat",
		"groqcloud_chat", "together_chat", "perplexity_chat", "fireworks_chat",
		"kimi_chat", "qwen_chat", "glm_chat", "yi_chat", "baichuan_chat",
		"doubao_chat", "ernie_chat", "step_chat", "hunyuan_chat",
		"minimax_chat", "cohere_chat", "ollama_chat",
		"copilot_session", "antigravity_session", "kiro_session", "windsurf_session",
	} {
		got, err := MarshalToProviderRequest(env, fam)
		if err != nil {
			t.Errorf("family %q: marshal err=%v(流式翻译路径 501 / 非流式 HCSF 502)", fam, err)
			continue
		}
		if !bytes.Equal(got, want) {
			t.Errorf("family %q 投影 != openai_chat 投影\ngot:  %s\nwant: %s", fam, got, want)
		}
	}
}

// TestInjectRequestControlsSkipsDifyChat 抓的回归:非流式 HCSF 路径
// (hcsfRequestBody→injectRequestControls)把 openai 形 controls(max_tokens/
// temperature/tools)注进 Dify body——Dify 无 per-request 参数,marshal 已对
// controls 记 loss,注入=协议污染且与 loss 记账自相矛盾。流式孪生跳过由
// gatewayhttp 的 TestStreamingProviderRequestBodyDifyChat 钉,本测试钉非流式。
// Mutation:删 injectRequestControls 的 dify_chat 早退 → 本测试红。
func TestInjectRequestControlsSkipsDifyChat(t *testing.T) {
	env := proto.NewEmptyEnvelope()
	max := 42
	env.RequestControls.MaxTokens = &max
	temp := 0.3
	env.RequestControls.Temperature = &temp
	raw := []byte(`{"inputs":{},"query":"USER: hi","response_mode":"blocking","user":"u1","auto_generate_name":false}`)
	out, err := injectRequestControls(raw, env, "dify_chat")
	if err != nil {
		t.Fatalf("injectRequestControls(dify_chat): %v", err)
	}
	if !bytes.Equal(out, raw) {
		t.Fatalf("dify_chat body 被 controls 注入污染:\ngot:  %s\nwant: %s", out, raw)
	}
}

// TestInjectRequestControlsSkipsOllamaNative 抓的回归:非流式 HCSF 路径把
// openai 形 controls(顶层 max_tokens/temperature)注进 Ollama body——Ollama
// 的采样控制已由 marshal 嵌进 options{}(num_predict),顶层二次注入=协议
// 污染(上游静默忽略顶层字段,且 body 双真相源)。流式孪生跳过由 gatewayhttp
// 的 TestStreamingProviderRequestBodyOllamaNative 钉,本测试钉非流式。
// Mutation:删 injectRequestControls 的 ollama_native 早退 → 本测试红。
func TestInjectRequestControlsSkipsOllamaNative(t *testing.T) {
	env := proto.NewEmptyEnvelope()
	max := 42
	env.RequestControls.MaxTokens = &max
	temp := 0.3
	env.RequestControls.Temperature = &temp
	raw := []byte(`{"model":"llama3.2","messages":[{"role":"user","content":"hi"}],"stream":false,"options":{"num_predict":42,"temperature":0.3}}`)
	out, err := injectRequestControls(raw, env, "ollama_native")
	if err != nil {
		t.Fatalf("injectRequestControls(ollama_native): %v", err)
	}
	if !bytes.Equal(out, raw) {
		t.Fatalf("ollama_native body 被 controls 注入污染:\ngot:  %s\nwant: %s", out, raw)
	}
}

// TestMarshalToProviderRequestOllamaNative 抓的回归:gateway marshal 分派把
// ollama_native 接错投影(回落 openai_chat 形顶层采样字段)或漏接(流式 501/
// 非流式 502)。判别点:options 嵌套 + 显式 stream 字段是 Ollama 形独有,
// openai_chat 投影不会产出。
func TestMarshalToProviderRequestOllamaNative(t *testing.T) {
	env := graphEnv(textNode("n1", "user", "hello"))
	max := 9
	env.RequestControls.MaxTokens = &max
	body := marshalBody(t, env, "ollama_native")
	if _, ok := body["max_tokens"]; ok {
		t.Errorf("顶层不得出现 max_tokens(必须嵌 options.num_predict): %v", body)
	}
	options, ok := body["options"].(map[string]any)
	if !ok || options["num_predict"] != float64(9) {
		t.Errorf("options.num_predict=%v want 9", body["options"])
	}
	if _, present := body["stream"]; !present {
		t.Error("Ollama body 必须显式携带 stream 字段")
	}
	if got := body["model"]; got != "model-up" {
		t.Errorf("model=%v want model-up(UpstreamModel 优先)", got)
	}
}

// TestMarshalSupportsEveryRegisteredProtocolFamily 把 marshal 形态映射纳入
// 0ee632fc 建立的族集对称守卫:入站协议注册表里的每个族,要么能
// MarshalToProviderRequest,要么在下面 fail-closed 例外表里有文档化理由。
// 新增族只注册三表而漏 marshal 映射 → 本测试红,锁死整类 bug 的第 4 处变体。
// 例外表是显式逃逸口:扩编必须带理由注释,review 可见。
func TestMarshalSupportsEveryRegisteredProtocolFamily(t *testing.T) {
	env := graphEnv(textNode("n1", "user", "hello"))
	failClosedFamilies := map[string]string{
		// 与 hcsfProviderRequestModelFamily 的排除注释保持同步(单一真相在
		// upstream_dispatcher_hcsf.go,此处仅作守卫例外登记)。
		"bedrock_invoke":          "binary EventStream;anthropic 走 native-raw + adapter 内 AutoTranslate,openai 入站 marshal fail-closed",
		"openai_codex":            "请求/响应形态仓内记载互斥(native-raw Responses vs chat-chunk 解析器),待 OCAW 采集确认",
		"cursor_session":          "上游 Connect/proto 帧(application/connect+proto),openai_chat JSON 投影不可解析,待 OCAW",
		"gemini_advanced_session": "上游 f.req= form-urlencoded 包装,非 Gemini API JSON,待 OCAW",
	}
	for fam := range BuildDefaultProtocolAdapterRegistry().adapters {
		if _, ok := failClosedFamilies[fam]; ok {
			// fail-closed 族必须真的不支持 marshal——若某天有人加了映射却忘删
			// 例外行,这里反向红,防例外表腐化成静默放行。
			if _, err := MarshalToProviderRequest(env, fam); err == nil {
				t.Errorf("family %q 在 fail-closed 例外表里但 marshal 居然成功——例外表或映射表有一边过期", fam)
			}
			continue
		}
		if _, err := MarshalToProviderRequest(env, fam); err != nil {
			t.Errorf("family %q 已注册入站协议适配器但 HCSF 请求 marshal 不支持(流式 501/非流式 502): %v", fam, err)
		}
	}
}
