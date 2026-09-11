package chatpipe

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/proto"
	protogemini "github.com/BloomingProsperity/HUAKAI/internal/proto/gemini"
)

func TestStreamingTranslatedClaudeGetsConservativeMaxTokens(t *testing.T) {
	env := proto.NewEmptyEnvelope()
	env.RequestMeta.Model = "claude-sonnet"
	env.CapabilityGraph.Nodes = []proto.CapabilityNode{{
		ID:          "n1",
		Kind:        proto.CapabilityText,
		StreamReady: proto.StreamReadyYes,
		Text:        &proto.TextNode{Role: "user", Block: proto.CanonicalContentBlock{Type: "text", Text: "hi"}},
	}}
	raw, err := StreamingProviderRequestBody(env, "anthropic_claude_session")
	if err != nil {
		t.Fatalf("StreamingProviderRequestBody: %v", err)
	}
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("json: %v", err)
	}
	if body["max_tokens"].(float64) != 4096 {
		t.Fatalf("流式翻译缺 max_tokens 必须补保守值: %s", raw)
	}
	if body["stream"] != true {
		t.Fatalf("流式翻译必须带 stream=true: %s", raw)
	}
}

func TestStreamingTranslatedClaudeUsesCatalogMaxTokens(t *testing.T) {
	env := proto.NewEmptyEnvelope()
	env.RequestMeta.Model = "claude-sonnet"
	catalog := 8192
	env.RequestMeta.CatalogMaxOutputTokens = &catalog
	env.CapabilityGraph.Nodes = []proto.CapabilityNode{{
		ID:          "n1",
		Kind:        proto.CapabilityText,
		StreamReady: proto.StreamReadyYes,
		Text:        &proto.TextNode{Role: "user", Block: proto.CanonicalContentBlock{Type: "text", Text: "hi"}},
	}}
	raw, err := StreamingProviderRequestBody(env, "anthropic_claude_session")
	if err != nil {
		t.Fatalf("StreamingProviderRequestBody: %v", err)
	}
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("json: %v", err)
	}
	if body["max_tokens"].(float64) != 8192 {
		t.Fatalf("流式翻译必须用目录上限: %s", raw)
	}
}

func TestStreamingGeminiRejectsPatternProperties(t *testing.T) {
	env := proto.NewEmptyEnvelope()
	env.RequestMeta.Model = "gemini-2.5-pro"
	env.CapabilityGraph.Nodes = []proto.CapabilityNode{{
		ID:          "n1",
		Kind:        proto.CapabilityText,
		StreamReady: proto.StreamReadyYes,
		Text:        &proto.TextNode{Role: "user", Block: proto.CanonicalContentBlock{Type: "text", Text: "hi"}},
	}}
	env.RequestControls.Tools = []proto.CanonicalTool{{
		Name:        "lookup",
		InputSchema: json.RawMessage(`{"type":"object","patternProperties":{".*":{"type":"string"}}}`),
	}}
	_, err := StreamingProviderRequestBody(env, "gemini_messages")
	if !errors.Is(err, protogemini.ErrUnsupportedToolSchema) {
		t.Fatalf("流式 Gemini schema 必须 fail-closed, err=%v", err)
	}
}
