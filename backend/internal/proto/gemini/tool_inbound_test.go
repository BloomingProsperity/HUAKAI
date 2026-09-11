package gemini

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/proto"
)

func geminiRequestCtx(model string) context.Context {
	return proto.ContextWithRequestMetaSeed(context.Background(), proto.RequestMetaSeed{
		RequestID:      "req-gemini-tool-loop",
		ClientProtocol: proto.ClientProtocolGemini,
		ProtocolFamily: "gemini_messages",
		IngressPath:    "/v1beta/models/" + model + ":generateContent",
		Model:          model,
	})
}

func TestGeminiFunctionResponseBecomesToolResult(t *testing.T) {
	client := &GeminiClient{}
	raw := []byte(`{
		"contents":[
			{"role":"user","parts":[{"text":"查天气"}]},
			{"role":"model","parts":[{"functionCall":{"id":"call_1","name":"lookup","args":{"q":"x"},"thoughtSignature":"sig-real"}}]},
			{"role":"user","parts":[{"functionResponse":{"id":"call_1","name":"lookup","response":{"content":"晴"}}}]}
		]
	}`)
	env, losses, err := client.RequestToCanonical(geminiRequestCtx("gemini-2.5-pro"), raw)
	if err != nil {
		t.Fatalf("RequestToCanonical: %v", err)
	}
	if len(losses) != 0 {
		t.Fatalf("完整工具环不应记 loss: %+v", losses)
	}
	if len(env.Messages) != 3 {
		t.Fatalf("messages=%d want 3", len(env.Messages))
	}
	result := env.Messages[2].Content[0]
	if result.Type != "tool_result" || result.CallID != "call_1" || result.Name != "lookup" {
		t.Fatalf("functionResponse 未建成 tool_result: %+v", result)
	}
	var blocks []proto.CanonicalContentBlock
	if err := json.Unmarshal(result.ToolResult, &blocks); err != nil || len(blocks) == 0 || blocks[0].Text != "晴" {
		t.Fatalf("tool_result 内容=%s", result.ToolResult)
	}

	var use, res *proto.CapabilityNode
	for i := range env.CapabilityGraph.Nodes {
		node := &env.CapabilityGraph.Nodes[i]
		switch node.Kind {
		case proto.CapabilityToolUse:
			use = node
		case proto.CapabilityToolResult:
			res = node
		}
	}
	if use == nil || use.ToolUse == nil || use.ToolUse.OpaqueState != "sig-real" {
		t.Fatalf("tool_use 未保存真实思考状态: %+v", use)
	}
	if res == nil || res.ToolResult == nil || res.ToolResult.ToolCallID != "call_1" {
		t.Fatalf("tool_result 节点缺失: %+v", res)
	}
	foundEdge := false
	for _, edge := range env.CapabilityGraph.Edges {
		if edge.Type == proto.EdgeRequires && edge.From == res.ID && edge.To == use.ID && edge.Required {
			foundEdge = true
		}
	}
	if !foundEdge {
		t.Fatalf("缺少 tool_result→tool_use Requires 边: %+v", env.CapabilityGraph.Edges)
	}
}

func TestGeminiToolSchemaRefIsProjectedOnIngress(t *testing.T) {
	client := &GeminiClient{}
	raw := []byte(`{
		"contents":[{"role":"user","parts":[{"text":"hi"}]}],
		"tools":[{"functionDeclarations":[{"name":"lookup","parameters":{"type":"object","properties":{"item":{"$ref":"#/$defs/Item"}},"$defs":{"Item":{"type":"string"}}}}]}]
	}`)
	env, _, err := client.RequestToCanonical(geminiRequestCtx("gemini-2.5-pro"), raw)
	if err != nil {
		t.Fatalf("可展开 schema 不得拒绝: %v", err)
	}
	if len(env.RequestControls.Tools) != 1 {
		t.Fatalf("tools=%d", len(env.RequestControls.Tools))
	}
	if strings.Contains(string(env.RequestControls.Tools[0].InputSchema), "$ref") {
		t.Fatalf("入站 schema 未展开: %s", env.RequestControls.Tools[0].InputSchema)
	}
}

func TestGeminiToolSchemaPatternPropertiesFailClosed(t *testing.T) {
	client := &GeminiClient{}
	raw := []byte(`{
		"contents":[{"role":"user","parts":[{"text":"hi"}]}],
		"tools":[{"functionDeclarations":[{"name":"lookup","parameters":{"type":"object","patternProperties":{".*":{"type":"string"}}}}]}]
	}`)
	_, _, err := client.RequestToCanonical(geminiRequestCtx("gemini-2.5-pro"), raw)
	if !errors.Is(err, ErrUnsupportedToolSchema) {
		t.Fatalf("patternProperties 必须 fail-closed, err=%v", err)
	}
}

func TestGemini3MissingThoughtFailsClosed(t *testing.T) {
	client := &GeminiClient{}
	raw := []byte(`{
		"contents":[
			{"role":"model","parts":[{"functionCall":{"id":"call_1","name":"lookup","args":{"q":"x"}}}]},
			{"role":"user","parts":[{"functionResponse":{"id":"call_1","name":"lookup","response":{"content":"ok"}}}]}
		]
	}`)
	_, _, err := client.RequestToCanonical(geminiRequestCtx("gemini-3.1-pro"), raw)
	if !errors.Is(err, ErrGeminiThoughtStateMissing) {
		t.Fatalf("Gemini 3 缺思考状态必须 fail-closed, err=%v", err)
	}
}

func TestGemini3ReplaysRealThoughtOntoFirstToolUse(t *testing.T) {
	client := &GeminiClient{}
	raw := []byte(`{
		"contents":[
			{"role":"model","parts":[
				{"functionCall":{"id":"call_1","name":"lookup","args":{"q":"a"}}},
				{"functionCall":{"id":"call_2","name":"lookup","args":{"q":"b"},"thoughtSignature":"sig-parallel"}}
			]},
			{"role":"user","parts":[
				{"functionResponse":{"id":"call_1","name":"lookup","response":{"content":"a"}}},
				{"functionResponse":{"id":"call_2","name":"lookup","response":{"content":"b"}}}
			]}
		]
	}`)
	env, _, err := client.RequestToCanonical(geminiRequestCtx("gemini-3-flash"), raw)
	if err != nil {
		t.Fatalf("真实签名应回放到首个 tool_use: %v", err)
	}
	var states []string
	for _, node := range env.CapabilityGraph.Nodes {
		if node.Kind == proto.CapabilityToolUse && node.ToolUse != nil {
			states = append(states, node.ToolUse.OpaqueState)
		}
	}
	if len(states) != 2 || states[0] != "sig-parallel" || states[1] != "sig-parallel" {
		t.Fatalf("并行回放状态=%v want 首个补真实签名且保留原签名", states)
	}
}

func TestGemini25MissingThoughtStillAccepted(t *testing.T) {
	client := &GeminiClient{}
	raw := []byte(`{
		"contents":[
			{"role":"model","parts":[{"functionCall":{"id":"call_1","name":"lookup","args":{"q":"x"}}}]},
			{"role":"user","parts":[{"functionResponse":{"id":"call_1","name":"lookup","response":{"content":"ok"}}}]}
		]
	}`)
	if _, _, err := client.RequestToCanonical(geminiRequestCtx("gemini-2.5-pro"), raw); err != nil {
		t.Fatalf("非 Gemini 3 缺签名不得拒绝: %v", err)
	}
}

func TestGeminiToolResultUnknownCallFailsClosed(t *testing.T) {
	client := &GeminiClient{}
	raw := []byte(`{
		"contents":[
			{"role":"user","parts":[{"functionResponse":{"id":"missing","name":"lookup","response":{"content":"ok"}}}]}
		]
	}`)
	_, _, err := client.RequestToCanonical(geminiRequestCtx("gemini-2.5-pro"), raw)
	if err == nil || !strings.Contains(err.Error(), "unknown tool call") {
		t.Fatalf("对不上的 functionResponse 必须 fail-closed, err=%v", err)
	}
}
