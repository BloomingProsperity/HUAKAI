package proto

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func thinkingNodeFrom(t *testing.T, env *HCSF) *ThinkingNode {
	t.Helper()
	for i := range env.CapabilityGraph.Nodes {
		if env.CapabilityGraph.Nodes[i].Kind == CapabilityThinking {
			return env.CapabilityGraph.Nodes[i].Thinking
		}
	}
	t.Fatal("缺少思考节点")
	return nil
}

func TestAnthropicMessages_KeepsThinkingDisplay(t *testing.T) {
	adapter := &AnthropicMessagesClient{}
	body := []byte(`{
		"model":"claude-opus-4-7",
		"max_tokens":1024,
		"thinking":{"type":"adaptive","display":"omitted"},
		"messages":[{"role":"user","content":"hi"}]
	}`)
	env, _, err := adapter.RequestToCanonical(newTestAnthropicCtx(t), body)
	if err != nil {
		t.Fatalf("RequestToCanonical: %v", err)
	}
	node := thinkingNodeFrom(t, env)
	if node.Display != "omitted" {
		t.Fatalf("Display=%q 必须原样保留 omitted", node.Display)
	}
}

func TestAnthropicMessages_RejectsUnknownThinkingDisplay(t *testing.T) {
	adapter := &AnthropicMessagesClient{}
	body := []byte(`{
		"model":"claude-opus-4-7",
		"max_tokens":1024,
		"thinking":{"type":"adaptive","display":"verbose"},
		"messages":[{"role":"user","content":"hi"}]
	}`)
	_, _, err := adapter.RequestToCanonical(newTestAnthropicCtx(t), body)
	if !errors.Is(err, ErrUnknownThinkingDisplay) {
		t.Fatalf("未知 display 必须拒绝，得到 %v", err)
	}
}

func TestAnthropicMessages_RejectsDisplayWhenThinkingDisabled(t *testing.T) {
	adapter := &AnthropicMessagesClient{}
	body := []byte(`{
		"model":"claude-opus-4-7",
		"max_tokens":1024,
		"thinking":{"type":"disabled","display":"summarized"},
		"messages":[{"role":"user","content":"hi"}]
	}`)
	_, _, err := adapter.RequestToCanonical(newTestAnthropicCtx(t), body)
	if !errors.Is(err, ErrThinkingDisplayWhenDisabled) {
		t.Fatalf("关闭思考仍带 display 必须拒绝，得到 %v", err)
	}
}

func TestAnthropicMessages_UpdatesRequiresBeta(t *testing.T) {
	adapter := &AnthropicMessagesClient{}
	body := []byte(`{
		"model":"claude-opus-4-7",
		"max_tokens":1024,
		"thinking":{"type":"adaptive","display":"updates"},
		"messages":[{"role":"user","content":"hi"}]
	}`)
	_, _, err := adapter.RequestToCanonical(newTestAnthropicCtx(t), body)
	if !errors.Is(err, ErrThinkingDisplayNeedsBeta) {
		t.Fatalf("无 beta 的 updates 必须拒绝，得到 %v", err)
	}

	ctx := ContextWithRequestMetaSeed(newTestAnthropicCtx(t), RequestMetaSeed{
		RequestID:         "req_test_d1",
		ClientProtocol:    ClientProtocolAnthropicMessages,
		ProtocolFamily:    "anthropic",
		IngressPath:       "/v1/messages",
		EvidenceLabel:     EvidenceMock,
		InboundBetaTokens: []string{"thinking-display-updates-2026-08-18"},
	})
	env, _, err := adapter.RequestToCanonical(ctx, body)
	if err != nil {
		t.Fatalf("带 beta 的 updates 应接受: %v", err)
	}
	if thinkingNodeFrom(t, env).Display != "updates" {
		t.Fatalf("Display=%q want updates", thinkingNodeFrom(t, env).Display)
	}
}

func TestAnthropicMessages_ClientEchoesNestedThinkingTokens(t *testing.T) {
	adapter := &AnthropicMessagesClient{}
	env := NewEmptyEnvelope()
	env.BufferedResponse = &CanonicalResponse{
		ID:    "msg_1",
		Model: "claude-opus-4-7",
		Content: []CanonicalContentBlock{{
			Type: "thinking", Signature: "sig_keep",
		}, {
			Type: "text", Text: "hi",
		}},
		Usage: CanonicalUsage{
			InputTokens:         10,
			OutputTokens:        40,
			ReasoningTokens:     12,
			ThinkingTokensKnown: true,
		},
		StopReason: CanonicalStopEndTurn,
	}
	raw, _, err := adapter.CanonicalToClientResponse(newTestAnthropicCtx(t), env)
	if err != nil {
		t.Fatalf("CanonicalToClientResponse: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("json: %v", err)
	}
	usage, _ := out["usage"].(map[string]any)
	details, _ := usage["output_tokens_details"].(map[string]any)
	if details == nil || details["thinking_tokens"] != float64(12) {
		t.Fatalf("客户端必须回嵌套 thinking_tokens=12: %s", raw)
	}
	if usage["output_tokens"] != float64(40) {
		t.Fatalf("包容性 output 必须仍是 40: %s", raw)
	}
	if strings.Count(string(raw), `"thinking_tokens"`) != 1 {
		t.Fatalf("思考分解不得顶层再写一笔: %s", raw)
	}
}

func TestAnthropicMessages_OmittedEmptyThinkingBlockKept(t *testing.T) {
	adapter := &AnthropicMessagesClient{}
	env := NewEmptyEnvelope()
	env.BufferedResponse = &CanonicalResponse{
		ID:    "msg_omit",
		Model: "claude-opus-4-7",
		Content: []CanonicalContentBlock{{
			Type: "thinking", Thinking: "", Signature: "sig_empty",
		}},
		Usage: CanonicalUsage{
			OutputTokens:        20,
			ReasoningTokens:     8,
			ThinkingTokensKnown: true,
		},
		StopReason: CanonicalStopEndTurn,
	}
	raw, _, err := adapter.CanonicalToClientResponse(newTestAnthropicCtx(t), env)
	if err != nil {
		t.Fatalf("CanonicalToClientResponse: %v", err)
	}
	if !strings.Contains(string(raw), `"type":"thinking"`) || !strings.Contains(string(raw), "sig_empty") {
		t.Fatalf("omitted 空思考块必须保留类型与签名: %s", raw)
	}
}
