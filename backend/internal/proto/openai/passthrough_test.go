// proto/openai/passthrough_test.go — U7-C 测试：openai.Adapter 接入
// PassthroughEnvelope 后，上游 chunk / response 中 HUAKAI typed struct 未
// 声明的字段（system_fingerprint / service_tier / logprobs /
// prompt_filter_results 等）透传到 proto.CanonicalEvent.Passthrough /
// CanonicalResponse.Passthrough。
package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/BloomingProsperity/HUAKAI/internal/proto"
	"strings"
	"testing"
)

// TestOpenAI_StreamingChunk_PassthroughCarriesUnknownFields 真实
// gpt-4o-2024-11-20 streaming chunk 形态，含 system_fingerprint +
// service_tier + 占位 logprobs。
func TestOpenAI_StreamingChunk_PassthroughCarriesUnknownFields(t *testing.T) {
	chunk := `data: {"id":"chatcmpl-x","object":"chat.completion.chunk","model":"gpt-4o-2024-11-20","system_fingerprint":"fp_b04fe7ce4f","service_tier":"scale","choices":[{"index":0,"delta":{"role":"assistant","content":"Hello"}}]}`

	adapter := &Adapter{}
	state := &UpstreamState{}
	events, _, err := adapter.ProviderEventToCanonicalEvents(context.Background(), []byte(chunk), state)
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	if len(events) == 0 {
		t.Fatal("应至少产 message_start + content_block_start + content_block_delta")
	}

	// 第一条 event 应携带 Passthrough
	first, ok := events[0].(proto.CanonicalEvent)
	if !ok {
		t.Fatalf("events[0] 类型 %T", events[0])
	}
	if first.Passthrough == nil {
		t.Fatal("第一条事件应携带 Passthrough（不为 nil）")
	}
	for _, key := range []string{"system_fingerprint", "service_tier"} {
		if _, ok := first.Passthrough.Extra[key]; !ok {
			t.Errorf("Passthrough.Extra 缺 %q（vendor 字段透传失败）", key)
		}
	}
	// 内容验证：raw JSON 完整
	if !bytes.Equal(first.Passthrough.Extra["system_fingerprint"], json.RawMessage(`"fp_b04fe7ce4f"`)) {
		t.Errorf("system_fingerprint 值错：%s", first.Passthrough.Extra["system_fingerprint"])
	}
}

func TestOpenAI_StreamingChunk_PassthroughCopiedToEveryEmittedEvent(t *testing.T) {
	chunk := `data: {"id":"chatcmpl-multi","object":"chat.completion.chunk","model":"gpt-4o","system_fingerprint":"fp_multi","service_tier":"scale","choices":[{"index":0,"delta":{"role":"assistant","content":"Hello"}}]}`

	adapter := &Adapter{}
	state := &UpstreamState{}
	events, _, err := adapter.ProviderEventToCanonicalEvents(context.Background(), []byte(chunk), state)
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	if len(events) < 3 {
		t.Fatalf("events=%d want message_start/content_block_start/content_block_delta", len(events))
	}

	var sawDelta bool
	for i, e := range events {
		ce := e.(proto.CanonicalEvent)
		if ce.Passthrough == nil {
			t.Fatalf("event[%d] %s Passthrough nil", i, ce.Type)
		}
		if !bytes.Equal(ce.Passthrough.Extra["system_fingerprint"], json.RawMessage(`"fp_multi"`)) {
			t.Fatalf("event[%d] system_fingerprint=%s", i, ce.Passthrough.Extra["system_fingerprint"])
		}
		if ce.Type == "content_block_delta" {
			sawDelta = true
		}
	}
	if !sawDelta {
		t.Fatal("content_block_delta event missing")
	}
}

// TestOpenAI_StreamingChunk_NoUnknownFields_PassthroughIsNil 既有路径不破：
// 没有 unknown 字段的 chunk 不应产生 Passthrough（保留 nil）。
func TestOpenAI_StreamingChunk_NoUnknownFields_PassthroughIsNil(t *testing.T) {
	chunk := `data: {"id":"x","model":"gpt-4o","choices":[{"index":0,"delta":{"content":"hi"}}]}`

	adapter := &Adapter{}
	state := &UpstreamState{}
	events, _, err := adapter.ProviderEventToCanonicalEvents(context.Background(), []byte(chunk), state)
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	for i, e := range events {
		ce := e.(proto.CanonicalEvent)
		if ce.Passthrough != nil {
			t.Errorf("event[%d].Passthrough 应为 nil，得 %+v", i, ce.Passthrough)
		}
	}
}

// TestOpenAI_NestedUnknown_PreservesStructure 嵌套 unknown 字段
// （prompt_filter_results 是数组）应保留嵌套结构。
func TestOpenAI_NestedUnknown_PreservesStructure(t *testing.T) {
	chunk := `data: {"id":"x","model":"gpt-4o","prompt_filter_results":[{"index":0,"content_filter_results":{"hate":{"filtered":false}}}],"choices":[{"index":0,"delta":{"content":"a"}}]}`

	adapter := &Adapter{}
	state := &UpstreamState{}
	events, _, err := adapter.ProviderEventToCanonicalEvents(context.Background(), []byte(chunk), state)
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	first := events[0].(proto.CanonicalEvent)
	if first.Passthrough == nil {
		t.Fatal("Passthrough 不应 nil")
	}
	pfr, ok := first.Passthrough.Extra["prompt_filter_results"]
	if !ok {
		t.Fatal("prompt_filter_results 应在 Extra")
	}
	// 嵌套结构能 unmarshal 出来
	var nested []map[string]any
	if err := json.Unmarshal(pfr, &nested); err != nil {
		t.Fatalf("嵌套 unmarshal err=%v", err)
	}
	if len(nested) != 1 {
		t.Errorf("嵌套数组长度=%d", len(nested))
	}
}

// TestOpenAI_DoneMarker_NoPassthrough [DONE] 标记不应触发 Passthrough。
func TestOpenAI_DoneMarker_NoPassthrough(t *testing.T) {
	adapter := &Adapter{}
	state := &UpstreamState{MessageStarted: true} // 模拟流中
	events, _, err := adapter.ProviderEventToCanonicalEvents(context.Background(), []byte("data: [DONE]"), state)
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	for _, e := range events {
		ce := e.(proto.CanonicalEvent)
		if ce.Passthrough != nil {
			t.Errorf("[DONE] 路径不应有 Passthrough，得 %+v", ce.Passthrough)
		}
	}
}

// TestOpenAI_BufferedResponse_PassthroughCaptured 校验非流式
// chat.completion 响应顶层 unknown 字段进 CanonicalResponse.Passthrough。
func TestOpenAI_BufferedResponse_PassthroughCaptured(t *testing.T) {
	resp := `{
		"id":"chatcmpl-x",
		"object":"chat.completion",
		"created":1234567890,
		"model":"gpt-4o",
		"system_fingerprint":"fp_xyz",
		"service_tier":"default",
		"choices":[{"index":0,"message":{"role":"assistant","content":"hi"},"finish_reason":"stop"}],
		"usage":{"prompt_tokens":5,"completion_tokens":2,"total_tokens":7}
	}`

	out, _, err := openAIResponseToCanonicalResponse([]byte(resp))
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	if out.Passthrough == nil {
		t.Fatal("buffered response Passthrough 不应 nil")
	}
	// "object" 已在 openAIChatCompletionResponse 中声明 → 进 typed 不进 Extra
	for _, k := range []string{"system_fingerprint", "service_tier", "created"} {
		if _, ok := out.Passthrough.Extra[k]; !ok {
			t.Errorf("Passthrough.Extra 缺 %q", k)
		}
	}
}

// TestOpenAI_MergeIntoClientOutput 端到端 round-trip 模拟：上游 chunk
// → adapter → proto.CanonicalEvent → ClientAdapter（这里手工模拟）调
// proto.MergeExtrasInto → 客户端最终看到 vendor 字段。
func TestOpenAI_MergeIntoClientOutput(t *testing.T) {
	chunk := `{"id":"x","model":"gpt-4o","system_fingerprint":"fp_abc","choices":[{"index":0,"delta":{"content":"hi"}}]}`

	adapter := &Adapter{}
	state := &UpstreamState{}
	events, _, _ := adapter.ProviderEventToCanonicalEvents(context.Background(), []byte(chunk), state)
	first := events[0].(proto.CanonicalEvent)

	// 模拟 ClientAdapter 把 first 序列化为 OpenAI chat.completion.chunk 形态
	// （这里手工 marshal 一个最小 typed shape；正式 ClientAdapter 在 U7 后续接入）
	clientTyped := map[string]any{
		"id":     state.MessageID,
		"model":  state.Model,
		"object": "chat.completion.chunk",
		"choices": []map[string]any{
			{"index": 0, "delta": map[string]string{"role": "assistant"}},
		},
	}
	clientJSON, _ := json.Marshal(clientTyped)
	merged, err := proto.MergeExtrasInto(clientJSON, first.Passthrough)
	if err != nil {
		t.Fatalf("merge err=%v", err)
	}
	// 客户端最终 JSON 同时含 typed shape + 原 vendor 字段
	if !strings.Contains(string(merged), "system_fingerprint") {
		t.Errorf("merged 应含 system_fingerprint：%s", merged)
	}
	if !strings.Contains(string(merged), "fp_abc") {
		t.Errorf("merged 应含 fp_abc 值：%s", merged)
	}
}
