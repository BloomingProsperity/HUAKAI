package gateway

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/proto"
)

func newAdaptiveTestCtx() context.Context {
	return proto.ContextWithRequestMetaSeed(context.Background(), proto.RequestMetaSeed{
		RequestID:      "req_fable",
		ClientProtocol: proto.ClientProtocolAnthropicMessages,
		ProtocolFamily: "anthropic",
		IngressPath:    "/v1/messages",
		EvidenceLabel:  proto.EvidenceMock,
	})
}

// 判别测试:claude-fable-5 的 adaptive thinking(无 budget_tokens)必须端到端经
// HCSF 重组路径保留到上游 body。此前解析侧只认 type=enabled、adaptive 不建节点
// 且零 loss,marshal 侧又要求 budget>0,导致 fable-5 thinking 整个蒸发。
// 变异守卫: 解析不建 adaptive 节点 / marshal 不发 adaptive → thinking 丢失,红。
func TestAnthropicAdaptiveThinking_SurvivesHCSFRoundTrip(t *testing.T) {
	client := &proto.AnthropicMessagesClient{}
	reqBody := []byte(`{
		"model":"claude-fable-5",
		"max_tokens":1024,
		"thinking":{"type":"adaptive"},
		"messages":[{"role":"user","content":"hi"}]
	}`)
	env, _, err := client.RequestToCanonical(newAdaptiveTestCtx(), reqBody)
	if err != nil {
		t.Fatalf("ClientRequestToCanonical: %v", err)
	}
	// 解析侧:必须建一个 adaptive thinking 节点
	var thinkingNode *proto.ThinkingNode
	for i := range env.CapabilityGraph.Nodes {
		if env.CapabilityGraph.Nodes[i].Kind == proto.CapabilityThinking {
			thinkingNode = env.CapabilityGraph.Nodes[i].Thinking
		}
	}
	if thinkingNode == nil {
		t.Fatal("adaptive thinking 必须建 CapabilityThinking 节点(此前蒸发)")
	}
	if thinkingNode.Mode != "adaptive" {
		t.Fatalf("thinking 节点 Mode=%q want adaptive", thinkingNode.Mode)
	}

	// marshal 侧:回写到上游 body 必须是 {"type":"adaptive"} 且不带 budget_tokens
	out, err := MarshalToProviderRequest(env, "anthropic_messages")
	if err != nil {
		t.Fatalf("MarshalToProviderRequest: %v", err)
	}
	var body map[string]json.RawMessage
	if err := json.Unmarshal(out, &body); err != nil {
		t.Fatalf("unmarshal out: %v", err)
	}
	th, ok := body["thinking"]
	if !ok {
		t.Fatalf("上游 body 缺 thinking(adaptive 被丢): %s", out)
	}
	if !strings.Contains(string(th), `"adaptive"`) {
		t.Fatalf("thinking 未回写 adaptive: %s", th)
	}
	if strings.Contains(string(th), "budget_tokens") {
		t.Fatalf("adaptive 不应带 budget_tokens: %s", th)
	}
}

// enabled+budget 回归不变。
func TestAnthropicEnabledThinking_StillMarshalsBudget(t *testing.T) {
	client := &proto.AnthropicMessagesClient{}
	reqBody := []byte(`{"model":"claude-opus","max_tokens":1024,"thinking":{"type":"enabled","budget_tokens":2048},"messages":[{"role":"user","content":"x"}]}`)
	env, _, err := client.RequestToCanonical(newAdaptiveTestCtx(), reqBody)
	if err != nil {
		t.Fatalf("canonical: %v", err)
	}
	out, err := MarshalToProviderRequest(env, "anthropic_messages")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(out), `"budget_tokens":2048`) || !strings.Contains(string(out), `"enabled"`) {
		t.Fatalf("enabled+budget 回归: %s", out)
	}
}
