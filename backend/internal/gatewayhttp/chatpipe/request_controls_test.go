package chatpipe

import (
	"encoding/json"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/proto"
)

// TestInjectStreamingRequestControlsResponseFormatRawPassthroughOpenAIChat
// 守流式 controls 注入路径:Type:"raw" 时必须 1:1 还原 Schema 整体。
func TestInjectStreamingRequestControlsResponseFormatRawPassthroughOpenAIChat(t *testing.T) {
	raw := json.RawMessage(`{"type":"json_object"}`)
	env := &proto.HCSF{
		RequestControls: proto.RequestControls{
			ResponseFormat: &proto.ResponseFormat{Type: "raw", Schema: raw},
		},
	}
	out, err := injectStreamingRequestControls([]byte(`{}`), env, "openai_chat")
	if err != nil {
		t.Fatalf("injectStreamingRequestControls err=%v", err)
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
		t.Fatalf("streaming response_format.type = %v, want inbound 'json_object'", rf["type"])
	}
	if _, hasSchema := rf["schema"]; hasSchema {
		t.Fatalf("streaming response_format 出现多余 schema 字段: body=%+v", body)
	}
}

// TestInjectStreamingRequestControlsResponseFormatRawPassthroughOpenAIResponses
// 守 Responses 流式 controls 注入路径的 raw text.format 直通。
func TestInjectStreamingRequestControlsResponseFormatRawPassthroughOpenAIResponses(t *testing.T) {
	raw := json.RawMessage(`{"format":{"type":"json_schema","json_schema":{"name":"Person","schema":{"type":"object"}}}}`)
	env := &proto.HCSF{
		RequestControls: proto.RequestControls{
			ResponseFormat: &proto.ResponseFormat{Type: "raw", Schema: raw},
		},
	}
	out, err := injectStreamingRequestControls([]byte(`{}`), env, "openai_responses")
	if err != nil {
		t.Fatalf("injectStreamingRequestControls err=%v", err)
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
		t.Fatalf("streaming text.format.type = %v, want inbound 'json_schema'", format["type"])
	}
	if _, hasOuter := format["schema"]; hasOuter {
		t.Fatalf("streaming text.format 出现多余 schema 字段: body=%+v", body)
	}
}

func TestInjectStreamingRequestControlsMergesRequestPassthrough(t *testing.T) {
	max := 12
	env := &proto.HCSF{
		RequestControls: proto.RequestControls{MaxTokens: &max},
		Passthrough: &proto.PassthroughEnvelope{Extra: map[string]json.RawMessage{
			"max_tool_calls":    json.RawMessage(`3`),
			"prompt_cache_key":  json.RawMessage(`"tenant-a:stable-prefix"`),
			"max_output_tokens": json.RawMessage(`999`),
		}},
	}
	out, err := injectStreamingRequestControls([]byte(`{}`), env, "openai_responses")
	if err != nil {
		t.Fatalf("injectStreamingRequestControls err=%v", err)
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
