package hcsfmarshal

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/proto"
)

func chatSeed(t *testing.T) context.Context {
	t.Helper()
	return proto.ContextWithRequestMetaSeed(context.Background(), proto.RequestMetaSeed{
		RequestID:      "req_at_chat",
		ClientProtocol: proto.ClientProtocolOpenAIChat,
		ProtocolFamily: "openai",
		IngressPath:    "/v1/chat/completions",
		EvidenceLabel:  proto.EvidenceMock,
	})
}

func claudeSeed(t *testing.T) context.Context {
	t.Helper()
	return proto.ContextWithRequestMetaSeed(context.Background(), proto.RequestMetaSeed{
		RequestID:      "req_at_claude",
		ClientProtocol: proto.ClientProtocolAnthropicMessages,
		ProtocolFamily: "anthropic",
		IngressPath:    "/v1/messages",
		EvidenceLabel:  proto.EvidenceMock,
	})
}

func mustJSON(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("json: %v body=%s", err, raw)
	}
	return body
}

func assistantHasThinking(body map[string]any) (text, sig string, ok bool) {
	messages, _ := body["messages"].([]any)
	for _, raw := range messages {
		msg, _ := raw.(map[string]any)
		if msg["role"] != "assistant" {
			continue
		}
		content, _ := msg["content"].([]any)
		for _, blk := range content {
			block, _ := blk.(map[string]any)
			if block["type"] == "thinking" {
				text, _ = block["thinking"].(string)
				sig, _ = block["signature"].(string)
				return text, sig, true
			}
		}
	}
	return "", "", false
}

// TestChatInboundReasoningReplaysToAnthropic 守住：Chat 形 reasoning_content 不得再静默蒸发。
// 变异：删 RequestToCanonical 对推理字段的建点 → 出站 Anthropic 无 thinking 块。
func TestChatInboundReasoningReplaysToAnthropic(t *testing.T) {
	in := []byte(`{
		"model":"deepseek-reasoner",
		"messages":[
			{"role":"user","content":"sum"},
			{"role":"assistant","content":"need tool","reasoning_content":"I should call add","tool_calls":[{"id":"call_1","type":"function","function":{"name":"add","arguments":"{\"a\":1}"}}]},
			{"role":"tool","tool_call_id":"call_1","content":"2"}
		]
	}`)
	env, losses, err := (&proto.OpenAIChatClient{}).RequestToCanonical(chatSeed(t), in)
	if err != nil {
		t.Fatalf("inbound: %v", err)
	}
	for _, loss := range losses {
		if loss.Code == "d1x_thinking_block_pending" || loss.Code == "d5_reasoning_pending" {
			t.Fatalf("不得再记 pending loss: %+v", loss)
		}
	}
	env.RequestMeta.Provider = "deepseek"
	out, err := Marshal(env, "anthropic_messages")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	text, sig, ok := assistantHasThinking(mustJSON(t, out))
	if !ok || text != "I should call add" {
		t.Fatalf("兼容族出站必须带回思考块: %s", out)
	}
	if sig != "" {
		t.Fatalf("Chat 形没有签名，不得伪造签名: %s", out)
	}
	chatOut, err := Marshal(env, "openai_chat")
	if err != nil {
		t.Fatalf("chat marshal: %v", err)
	}
	if !bytes.Contains(chatOut, []byte(`"reasoning_content"`)) || !bytes.Contains(chatOut, []byte("I should call add")) {
		t.Fatalf("Chat 出站必须回传 reasoning_content: %s", chatOut)
	}
}

// TestMessagesInboundSignedThinkingRemarshals 守住：Messages 历史已签名思考在重组后仍在。
// 变异：把 thinking case 改回 pending loss → 出站无 signature。
func TestMessagesInboundSignedThinkingRemarshals(t *testing.T) {
	in := []byte(`{
		"model":"claude-opus-4-8",
		"max_tokens":1024,
		"messages":[
			{"role":"user","content":"sum"},
			{"role":"assistant","content":[
				{"type":"thinking","thinking":"I should call add","signature":"sig_real_not_synthetic"},
				{"type":"text","text":"need tool"},
				{"type":"tool_use","id":"toolu_1","name":"add","input":{"a":1}}
			]},
			{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_1","content":"2"}]}
		]
	}`)
	env, losses, err := (&proto.AnthropicMessagesClient{}).RequestToCanonical(claudeSeed(t), in)
	if err != nil {
		t.Fatalf("inbound: %v", err)
	}
	for _, loss := range losses {
		if loss.Code == "d1x_thinking_block_pending" {
			t.Fatalf("已签名思考不得再 pending: %+v", loss)
		}
	}
	env.RequestMeta.Provider = "minimax"
	out, err := Marshal(env, "anthropic_messages")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	text, sig, ok := assistantHasThinking(mustJSON(t, out))
	if !ok || text != "I should call add" || sig != "sig_real_not_synthetic" {
		t.Fatalf("兼容族必须原样回传签名思考: %s", out)
	}
}

// TestOfficialFamilyDropsUnsignedKeepsSigned 守住官方族重组：无签名可剥，有签名必须留。
// 未知 vendor 不得猜成可剥。
func TestOfficialFamilyDropsUnsignedKeepsSigned(t *testing.T) {
	unsigned := proto.NewEmptyEnvelope()
	unsigned.RequestMeta.Model = "claude-opus-4-8"
	unsigned.RequestMeta.Provider = "anthropic"
	unsigned.CapabilityGraph.Nodes = []proto.CapabilityNode{{
		ID: "n_think_u", Kind: proto.CapabilityThinking, StreamReady: proto.StreamReadyPartial,
		Thinking: &proto.ThinkingNode{Redaction: proto.RedactionPublic, Blocks: []proto.CanonicalContentBlock{{Type: "thinking", Thinking: "no sig"}}},
	}}
	raw, err := Marshal(unsigned, "anthropic_messages")
	if err != nil {
		t.Fatalf("unsigned marshal: %v", err)
	}
	if _, _, ok := assistantHasThinking(mustJSON(t, raw)); ok {
		t.Fatalf("官方族不得回传无签名思考: %s", raw)
	}
	dropped := false
	for _, loss := range unsigned.CapabilityGraph.ProtocolLoss {
		if loss.Code == "official_unsigned_thinking_dropped" {
			dropped = true
		}
	}
	if !dropped {
		t.Fatal("官方族剥无签名必须记可辨识 loss")
	}

	signed := proto.NewEmptyEnvelope()
	signed.RequestMeta.Model = "claude-opus-4-8"
	signed.RequestMeta.Provider = "anthropic"
	signed.CapabilityGraph.Nodes = []proto.CapabilityNode{{
		ID: "n_think_s", Kind: proto.CapabilityThinking, StreamReady: proto.StreamReadyPartial,
		Thinking: &proto.ThinkingNode{
			Redaction: proto.RedactionPublic, Signature: "sig_keep",
			Blocks: []proto.CanonicalContentBlock{{Type: "thinking", Thinking: "keep me", Signature: "sig_keep"}},
		},
	}}
	raw, err = Marshal(signed, "anthropic_messages")
	if err != nil {
		t.Fatalf("signed marshal: %v", err)
	}
	text, sig, ok := assistantHasThinking(mustJSON(t, raw))
	if !ok || text != "keep me" || sig != "sig_keep" {
		t.Fatalf("官方族必须留已签名思考: %s", raw)
	}

	unknown := proto.NewEmptyEnvelope()
	unknown.RequestMeta.Model = "claude-opus-4-8"
	unknown.CapabilityGraph.Nodes = unsigned.CapabilityGraph.Nodes
	raw, err = Marshal(unknown, "anthropic_messages")
	if err != nil {
		t.Fatalf("unknown marshal: %v", err)
	}
	if _, _, ok := assistantHasThinking(mustJSON(t, raw)); !ok {
		t.Fatalf("未知族不得猜成官方可剥: %s", raw)
	}
}

// TestChatClientResponseEmitsReasoning 守住：回 Chat 客户端时思考进 reasoning_content，不得只留正文。
func TestChatClientResponseEmitsReasoning(t *testing.T) {
	env := proto.NewEmptyEnvelope()
	env.BufferedResponse = &proto.CanonicalResponse{
		ID:    "msg_dump",
		Model: "claude-opus-4-8",
		Content: []proto.CanonicalContentBlock{
			{Type: "thinking", Thinking: "hidden chain", Signature: "sig_real"},
			{Type: "reasoning", Text: "compat reasoning"},
			{Type: "text", Text: "answer"},
		},
		StopReason: proto.CanonicalStopEndTurn,
	}
	body, losses, err := (&proto.OpenAIChatClient{}).CanonicalToClientResponse(context.Background(), env)
	if err != nil {
		t.Fatalf("chat response: %v", err)
	}
	for _, loss := range losses {
		if loss.Code == "d5_reasoning_pending" || loss.Code == "unknown_response_block_type" {
			t.Fatalf("思考不得再当未知块: %+v", loss)
		}
	}
	out := mustJSON(t, body)
	msg := out["choices"].([]any)[0].(map[string]any)["message"].(map[string]any)
	if msg["content"] != "answer" {
		t.Fatalf("正文被污染: %+v", msg)
	}
	reasoning, _ := msg["reasoning_content"].(string)
	if reasoning == "" || !bytes.Contains([]byte(reasoning), []byte("hidden chain")) || !bytes.Contains([]byte(reasoning), []byte("compat reasoning")) {
		t.Fatalf("reasoning_content 缺失: %+v", msg)
	}
	if _, ok := msg["signature"]; ok {
		t.Fatal("Chat 响应不得泄漏签名字段")
	}
}

// TestChatPlainAssistantReasoningReplays 守住：兼容族纯文本助手回合也要回传推理正文。
// 变异：只在带 tool_calls 时建思考节点 → 本用例出站无 thinking。
func TestChatPlainAssistantReasoningReplays(t *testing.T) {
	in := []byte(`{
		"model":"deepseek-reasoner",
		"messages":[
			{"role":"user","content":"sum"},
			{"role":"assistant","content":"4","reasoning_content":"2+2"},
			{"role":"user","content":"again"}
		]
	}`)
	env, losses, err := (&proto.OpenAIChatClient{}).RequestToCanonical(chatSeed(t), in)
	if err != nil {
		t.Fatalf("inbound: %v", err)
	}
	for _, loss := range losses {
		if loss.Code == "d1x_thinking_block_pending" || loss.Code == "d5_reasoning_pending" {
			t.Fatalf("不得再记 pending loss: %+v", loss)
		}
	}
	env.RequestMeta.Provider = "deepseek"
	out, err := Marshal(env, "anthropic_messages")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	text, sig, ok := assistantHasThinking(mustJSON(t, out))
	if !ok || text != "2+2" {
		t.Fatalf("纯文本回合也必须回传思考: %s", out)
	}
	if sig != "" {
		t.Fatalf("不得伪造签名: %s", out)
	}
}
