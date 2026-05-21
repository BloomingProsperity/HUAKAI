package gateway

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/proto"
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

func thinkingNode() proto.CapabilityNode {
	return proto.CapabilityNode{ID: "n_thinking_1", Kind: proto.CapabilityThinking, StreamReady: proto.StreamReadyPartial, Thinking: &proto.ThinkingNode{Redaction: proto.RedactionPublic, Blocks: []proto.CanonicalContentBlock{{Type: "text", Text: "visible thought"}}}}
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
