package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/proto"
	protogemini "github.com/BloomingProsperity/HUAKAI/internal/proto/gemini"
	"github.com/BloomingProsperity/HUAKAI/internal/provider"
)

func TestHCSFGeminiIngressToolResultMarshalsBack(t *testing.T) {
	client := &protogemini.GeminiClient{}
	ctx := proto.ContextWithRequestMetaSeed(context.Background(), proto.RequestMetaSeed{
		RequestID:      "req-313-gemini-loop",
		ClientProtocol: proto.ClientProtocolGemini,
		ProtocolFamily: "gemini_messages",
		IngressPath:    "/v1beta/models/gemini-2.5-pro:generateContent",
		Model:          "gemini-2.5-pro",
	})
	env, _, err := client.RequestToCanonical(ctx, []byte(`{
		"contents":[
			{"role":"model","parts":[{"functionCall":{"id":"call_1","name":"lookup","args":{"q":"x"},"thoughtSignature":"sig-keep"}}]},
			{"role":"user","parts":[{"functionResponse":{"id":"call_1","name":"lookup","response":{"content":"ok"}}}]}
		]
	}`))
	if err != nil {
		t.Fatalf("RequestToCanonical: %v", err)
	}
	body := marshalBody(t, env, "gemini_messages")
	contents := body["contents"].([]any)
	if len(contents) != 2 {
		t.Fatalf("contents=%d body=%+v", len(contents), body)
	}
	call := contents[0].(map[string]any)["parts"].([]any)[0].(map[string]any)["functionCall"].(map[string]any)
	if call["name"] != "lookup" || call["id"] != "call_1" || call["thoughtSignature"] != "sig-keep" {
		t.Fatalf("functionCall 回放失败: %+v", call)
	}
	resp := contents[1].(map[string]any)["parts"].([]any)[0].(map[string]any)["functionResponse"].(map[string]any)
	if resp["name"] != "lookup" || resp["id"] != "call_1" {
		t.Fatalf("functionResponse 丢失: %+v", resp)
	}
	if resp["response"].(map[string]any)["content"] != "ok" {
		t.Fatalf("functionResponse 内容: %+v", resp)
	}
}

func TestHCSFGemini3MissingThoughtFailsClosedOnMarshal(t *testing.T) {
	env := graphEnv(toolUseNode(), toolResultNode())
	env.RequestMeta.Model = "gemini-3.1-pro"
	env.RequestMeta.UpstreamModel = "gemini-3.1-pro"
	_, err := MarshalToProviderRequest(env, "gemini_messages")
	if !errors.Is(err, protogemini.ErrGeminiThoughtStateMissing) {
		t.Fatalf("Gemini 3 缺思考状态必须 fail-closed, err=%v", err)
	}
}

func TestHCSFGemini3ReplaysThoughtAndAllowsParallelGap(t *testing.T) {
	first := toolUseNode()
	first.ToolUse.OpaqueState = "sig-first"
	second := proto.CapabilityNode{
		ID: "n_tool_use_2", Kind: proto.CapabilityToolUse, StreamReady: proto.StreamReadyPartial,
		ToolUse: &proto.ToolUseNode{ToolCallID: "call_2", Name: "lookup", Input: json.RawMessage(`{"q":"y"}`), Status: proto.ToolNodeComplete},
	}
	secondResult := proto.CapabilityNode{
		ID: "n_tool_result_2", Kind: proto.CapabilityToolResult, StreamReady: proto.StreamReadyYes,
		ToolResult: &proto.ToolResultNode{ToolCallID: "call_2", Content: []proto.CanonicalContentBlock{{Type: "text", Text: "y"}}, Status: proto.ToolNodeComplete},
	}
	env := graphEnv(first, second, toolResultNode(), secondResult)
	env.RequestMeta.Model = "gemini-3-flash"
	env.RequestMeta.UpstreamModel = "gemini-3-flash"
	body := marshalBody(t, env, "gemini_messages")
	modelParts := body["contents"].([]any)[0].(map[string]any)["parts"].([]any)
	firstCall := modelParts[0].(map[string]any)["functionCall"].(map[string]any)
	secondCall := modelParts[1].(map[string]any)["functionCall"].(map[string]any)
	if firstCall["thoughtSignature"] != "sig-first" {
		t.Fatalf("首个 functionCall 必须带回真实签名: %+v", firstCall)
	}
	if _, ok := secondCall["thoughtSignature"]; ok {
		t.Fatalf("并行后续调用官方不带签名，不得伪造: %+v", secondCall)
	}
}

func TestHCSFNonGemini3MissingThoughtStillMarshals(t *testing.T) {
	env := graphEnv(toolUseNode(), toolResultNode())
	body := marshalBody(t, env, "gemini_messages")
	call := body["contents"].([]any)[0].(map[string]any)["parts"].([]any)[0].(map[string]any)["functionCall"].(map[string]any)
	if _, ok := call["thoughtSignature"]; ok {
		t.Fatalf("非 Gemini 3 不得伪造签名: %+v", call)
	}
}

func TestHCSFOpenAIToolsToGeminiRejectsPatternProperties(t *testing.T) {
	env := graphEnv(textNode("n1", "user", "hi"))
	env.RequestControls.Tools = []proto.CanonicalTool{{
		Name:        "lookup",
		InputSchema: json.RawMessage(`{"type":"object","patternProperties":{".*":{"type":"string"}}}`),
	}}
	_, err := hcsfRequestBody(env, "gemini_messages")
	if !errors.Is(err, protogemini.ErrUnsupportedToolSchema) {
		t.Fatalf("跨协议 schema 必须 fail-closed, err=%v", err)
	}
}

func TestHCSFOpenAIToolsToGeminiExpandsLocalRef(t *testing.T) {
	env := graphEnv(textNode("n1", "user", "hi"))
	env.RequestControls.Tools = []proto.CanonicalTool{{
		Name:        "lookup",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"item":{"$ref":"#/$defs/Item"}},"$defs":{"Item":{"type":"string"}}}`),
	}}
	raw, err := hcsfRequestBody(env, "gemini_messages")
	if err != nil {
		t.Fatalf("可展开 $ref 必须投影: %v", err)
	}
	if strings.Contains(string(raw), "$ref") || strings.Contains(string(raw), "$defs") {
		t.Fatalf("出站仍含未展开构造: %s", raw)
	}
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("body json: %v", err)
	}
	params := body["tools"].([]any)[0].(map[string]any)["functionDeclarations"].([]any)[0].(map[string]any)["parameters"].(map[string]any)
	if params["properties"].(map[string]any)["item"].(map[string]any)["type"] != "string" {
		t.Fatalf("展开结果错误: %+v", params)
	}
}

func TestHCSFTranslatedClaudeGetsConservativeMaxTokens(t *testing.T) {
	for _, family := range []string{"anthropic_messages", "anthropic_claude_session", "vertex_anthropic"} {
		env := graphEnv(textNode("n1", "user", "hello"))
		env.RequestMeta.ClientProtocol = proto.ClientProtocolOpenAIChat
		raw, err := hcsfRequestBody(env, family)
		if err != nil {
			t.Fatalf("%s: %v", family, err)
		}
		var body map[string]any
		if err := json.Unmarshal(raw, &body); err != nil {
			t.Fatalf("%s json: %v", family, err)
		}
		got, ok := body["max_tokens"].(float64)
		if !ok || got != 4096 {
			t.Fatalf("%s max_tokens=%v want 4096 body=%s", family, body["max_tokens"], raw)
		}
		if !hasProtocolLossCode(env.CapabilityGraph.ProtocolLoss, "claude_max_tokens_translated_default") {
			t.Fatalf("%s 必须记录翻译默认值 loss: %+v", family, env.CapabilityGraph.ProtocolLoss)
		}
	}
}

func TestHCSFTranslatedClaudeUsesCatalogMaxTokens(t *testing.T) {
	env := graphEnv(textNode("n1", "user", "hello"))
	env.RequestMeta.ClientProtocol = proto.ClientProtocolOpenAIChat
	catalog := 8192
	env.RequestMeta.CatalogMaxOutputTokens = &catalog
	raw, err := hcsfRequestBody(env, "anthropic_messages")
	if err != nil {
		t.Fatalf("hcsfRequestBody: %v", err)
	}
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("json: %v", err)
	}
	got, ok := body["max_tokens"].(float64)
	if !ok || got != 8192 {
		t.Fatalf("目录上限未进翻译默认值: %s（变异：忽略 CatalogMaxOutputTokens）", raw)
	}
	if !hasProtocolLossCode(env.CapabilityGraph.ProtocolLoss, "claude_max_tokens_translated_default") {
		t.Fatalf("目录补值仍须记翻译 loss: %+v", env.CapabilityGraph.ProtocolLoss)
	}
}

func TestHCSFTranslatedClaudeCatalogBelowFallback(t *testing.T) {
	env := graphEnv(textNode("n1", "user", "hello"))
	catalog := 2048
	env.RequestMeta.CatalogMaxOutputTokens = &catalog
	raw, err := hcsfRequestBody(env, "anthropic_claude_session")
	if err != nil {
		t.Fatalf("hcsfRequestBody: %v", err)
	}
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("json: %v", err)
	}
	if body["max_tokens"].(float64) != 2048 {
		t.Fatalf("不得超过目录上限: %s", raw)
	}
}

func TestHCSFTranslatedClaudeIgnoresNonPositiveCatalog(t *testing.T) {
	env := graphEnv(textNode("n1", "user", "hello"))
	zero := 0
	env.RequestMeta.CatalogMaxOutputTokens = &zero
	raw, err := hcsfRequestBody(env, "anthropic_messages")
	if err != nil {
		t.Fatalf("hcsfRequestBody: %v", err)
	}
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("json: %v", err)
	}
	if body["max_tokens"].(float64) != 4096 {
		t.Fatalf("非正目录必须回退兜底: %s", raw)
	}
}

func TestHCSFTranslatedClaudeKeepsCallerMaxTokens(t *testing.T) {
	env := graphEnv(textNode("n1", "user", "hello"))
	max := 128
	env.RequestControls.MaxTokens = &max
	catalog := 8192
	env.RequestMeta.CatalogMaxOutputTokens = &catalog
	raw, err := hcsfRequestBody(env, "anthropic_messages")
	if err != nil {
		t.Fatalf("hcsfRequestBody: %v", err)
	}
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("json: %v", err)
	}
	if body["max_tokens"].(float64) != 128 {
		t.Fatalf("调用方上限被覆盖: %s", raw)
	}
	if hasProtocolLossCode(env.CapabilityGraph.ProtocolLoss, "claude_max_tokens_translated_default") {
		t.Fatalf("已有上限不得记翻译默认值: %+v", env.CapabilityGraph.ProtocolLoss)
	}
}

func TestHCSFEmptyToolsDoNotEmitClaudeTools(t *testing.T) {
	env := graphEnv(textNode("n1", "user", "hello"))
	env.RequestControls.Tools = []proto.CanonicalTool{}
	raw, err := hcsfRequestBody(env, "anthropic_messages")
	if err != nil {
		t.Fatalf("hcsfRequestBody: %v", err)
	}
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("json: %v", err)
	}
	if _, ok := body["tools"]; ok {
		t.Fatalf("空 tools 不得出站: %s", raw)
	}
}

func TestHCSFSameShapeOpenAICompatDoesNotGetClaudeDefault(t *testing.T) {
	env := graphEnv(textNode("n1", "user", "hello"))
	raw, err := hcsfRequestBody(env, "kimi_chat")
	if err != nil {
		t.Fatalf("kimi_chat: %v", err)
	}
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("json: %v", err)
	}
	if _, ok := body["max_tokens"]; ok {
		t.Fatalf("同形态直通不得补 Claude 默认: %s", raw)
	}
	if hasProtocolLossCode(env.CapabilityGraph.ProtocolLoss, "claude_max_tokens_translated_default") {
		t.Fatalf("同形态直通不得记 Claude 翻译 loss: %+v", env.CapabilityGraph.ProtocolLoss)
	}
}

func TestOfficialDirectClaudeBodyDoesNotGetTranslatedMaxTokens(t *testing.T) {
	raw := []byte(`{"model":"claude-sonnet","messages":[{"role":"user","content":"hi"}]}`)
	adapter := &stubAdapter{platform: "anthropic"}
	ctx := ContextWithHCSFDispatchInput(context.Background(), HCSFDispatchInput{
		OfficialDirect:  true,
		ProtocolFamily:  "anthropic_messages",
		UpstreamModelID: "claude-sonnet",
		Account:         provider.AccountInfo{AccountID: 1, Platform: "anthropic", AccountType: "oauth"},
		Credential:      provider.Credential{Type: provider.CredentialTypeAPIKey, Value: "sk-ant"},
		RawBody:         raw,
	})
	in := provider.BuildInput{
		UpstreamModelID: "claude-sonnet",
		Account:         provider.AccountInfo{AccountID: 1, Platform: "anthropic", AccountType: "oauth"},
		Credential:      provider.Credential{Type: provider.CredentialTypeAPIKey, Value: "sk-ant"},
	}
	if _, err := buildHCSFProviderRequest(ctx, adapter, in, proto.NewEmptyEnvelope(), "anthropic_messages", "anthropic_claude_session", raw, nil); err != nil {
		t.Fatalf("official direct: %v", err)
	}
	if !bytes.Equal(adapter.lastInput.InboundBody, raw) {
		t.Fatalf("官方直发字节漂移\ngot  %s\nwant %s", adapter.lastInput.InboundBody, raw)
	}
}
