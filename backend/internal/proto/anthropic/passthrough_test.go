// proto/anthropic/passthrough_test.go — U7-D 测试：anthropic.Adapter 接入
// PassthroughEnvelope。验证 envelope-level unknown 字段（如 Anthropic 加
// 的 cache_creation_input_tokens / service_tier / system_fingerprint 等）
// 透传到 proto.CanonicalEvent.Passthrough。Bedrock-on-Anthropic（A4
// bedrock.EventStreamAdapter 委托）自动受益。
package anthropic_test

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/BloomingProsperity/HUAKAI/internal/proto"
	"github.com/BloomingProsperity/HUAKAI/internal/proto/anthropic"
	"github.com/BloomingProsperity/HUAKAI/internal/proto/bedrock"
	"strings"
	"testing"
)

// TestAnthropic_MessageStart_PassthroughCarriesUnknownFields 验证 message_start
// 事件顶层 unknown 字段进 Passthrough（如 vendor 加 service_tier）。
func TestAnthropic_MessageStart_PassthroughCarriesUnknownFields(t *testing.T) {
	raw := []byte(`{"type":"message_start","message":{"id":"msg_x","model":"claude-3-7-sonnet"},"service_tier":"standard","custom_field":42}`)
	adapter := &anthropic.Adapter{}
	state := &anthropic.UpstreamState{}
	events, _, err := adapter.ProviderEventToCanonicalEvents(context.Background(), raw, state)
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	if len(events) != 1 {
		t.Fatalf("events=%d want 1", len(events))
	}
	ce := events[0].(proto.CanonicalEvent)
	if ce.Passthrough == nil {
		t.Fatal("Passthrough 不应 nil")
	}
	for _, k := range []string{"service_tier", "custom_field"} {
		if _, ok := ce.Passthrough.Extra[k]; !ok {
			t.Errorf("Passthrough.Extra 缺 %q", k)
		}
	}
	if !bytes.Equal(ce.Passthrough.Extra["service_tier"], json.RawMessage(`"standard"`)) {
		t.Errorf("service_tier=%s", ce.Passthrough.Extra["service_tier"])
	}
}

// TestAnthropic_NoUnknown_PassthroughIsNil 既有路径不破。
func TestAnthropic_NoUnknown_PassthroughIsNil(t *testing.T) {
	raw := []byte(`{"type":"message_start","message":{"id":"y","model":"m"}}`)
	adapter := &anthropic.Adapter{}
	state := &anthropic.UpstreamState{}
	events, _, err := adapter.ProviderEventToCanonicalEvents(context.Background(), raw, state)
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	for i, e := range events {
		if ce := e.(proto.CanonicalEvent); ce.Passthrough != nil {
			t.Errorf("event[%d].Passthrough 应为 nil，得 %+v", i, ce.Passthrough)
		}
	}
}

// TestAnthropic_NestedUnknownAtEnvelopeLevel 嵌套 unknown 保结构。
func TestAnthropic_NestedUnknownAtEnvelopeLevel(t *testing.T) {
	raw := []byte(`{"type":"message_start","message":{"id":"x"},"vendor_metadata":{"region":"us-west-2","tier":{"level":"premium"}}}`)
	adapter := &anthropic.Adapter{}
	state := &anthropic.UpstreamState{}
	events, _, _ := adapter.ProviderEventToCanonicalEvents(context.Background(), raw, state)
	ce := events[0].(proto.CanonicalEvent)
	if ce.Passthrough == nil {
		t.Fatal("Passthrough 不应 nil")
	}
	vm, ok := ce.Passthrough.Extra["vendor_metadata"]
	if !ok {
		t.Fatal("vendor_metadata 应在 Extra")
	}
	var nested map[string]any
	if err := json.Unmarshal(vm, &nested); err != nil {
		t.Fatalf("嵌套 unmarshal err=%v", err)
	}
	if nested["region"] != "us-west-2" {
		t.Errorf("region=%v", nested["region"])
	}
}

// TestBedrockOnAnthropic_PassthroughInheritsFromAnthropic 验证 Bedrock-on-Anthropic
// adapter（A4）通过委托 anthropic.Adapter，自动获得 passthrough 能力。
func TestBedrockOnAnthropic_PassthroughInheritsFromAnthropic(t *testing.T) {
	// 模拟 Bedrock chunk 的 inner JSON（A3 scanner base64-decode 后的形态）
	innerJSON := []byte(`{"type":"message_start","message":{"id":"msg_bedrock","model":"claude-3-7-sonnet"},"bedrock_specific_field":"new_value"}`)

	adapter := bedrock.NewEventStreamAdapter()
	state := &anthropic.UpstreamState{}
	events, _, err := adapter.ProviderEventToCanonicalEvents(context.Background(), innerJSON, state)
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	ce := events[0].(proto.CanonicalEvent)
	if ce.Passthrough == nil {
		t.Fatal("Bedrock delegate 应继承 passthrough 能力")
	}
	if _, ok := ce.Passthrough.Extra["bedrock_specific_field"]; !ok {
		t.Errorf("bedrock_specific_field 应进 Passthrough.Extra")
	}
}

// TestAnthropic_MergeIntoClientOutput end-to-end：上游 unknown → adapter →
// proto.CanonicalEvent → proto.MergeExtrasInto → 客户端最终看到 vendor 字段。
func TestAnthropic_MergeIntoClientOutput(t *testing.T) {
	raw := []byte(`{"type":"message_start","message":{"id":"x","model":"claude-3-7"},"upstream_extra":"new_capability_v2"}`)
	adapter := &anthropic.Adapter{}
	state := &anthropic.UpstreamState{}
	events, _, _ := adapter.ProviderEventToCanonicalEvents(context.Background(), raw, state)
	ce := events[0].(proto.CanonicalEvent)

	// 模拟 ClientAdapter 的 typed marshal
	clientTyped := map[string]any{
		"type":    "message_start",
		"message": map[string]any{"id": ce.MessageID, "model": ce.Model},
	}
	clientJSON, _ := json.Marshal(clientTyped)
	merged, err := proto.MergeExtrasInto(clientJSON, ce.Passthrough)
	if err != nil {
		t.Fatalf("merge err=%v", err)
	}
	if !strings.Contains(string(merged), "upstream_extra") {
		t.Errorf("merged 应含 upstream_extra：%s", merged)
	}
	if !strings.Contains(string(merged), "new_capability_v2") {
		t.Errorf("merged 应含值：%s", merged)
	}
}

// TestAnthropic_MultiEventStream_OnlyFirstEventCarriesPassthrough 一个 chunk
// 产多 event 时，Passthrough 只附在第一条，避免重复 emit。
func TestAnthropic_MultiEventStream_OnlyFirstEventCarriesPassthrough(t *testing.T) {
	// content_block_delta 单事件 + envelope-level unknown
	raw := []byte(`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hi"},"envelope_extra":"x"}`)
	adapter := &anthropic.Adapter{}
	state := &anthropic.UpstreamState{}
	events, _, _ := adapter.ProviderEventToCanonicalEvents(context.Background(), raw, state)
	if len(events) != 1 {
		t.Fatalf("events=%d", len(events))
	}
	ce := events[0].(proto.CanonicalEvent)
	if ce.Passthrough == nil || ce.Passthrough.Extra["envelope_extra"] == nil {
		t.Errorf("第一条 event 应含 envelope_extra：%+v", ce.Passthrough)
	}
}
