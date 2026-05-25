// field_matrix_test.go — U7-E 测试：字段级 verdict matrix。
//
// 覆盖矩阵：
//   - registered preserved/transformed/dropped 各覆盖
//   - unregistered → FieldPreservedDefault（核心契约）
//   - cross pair boundary：相同 fieldName 在不同 (client,upstream) 不串线
//   - HasEntry 区分 "已登记" vs "默认保留"
//   - lossy/lossless transform 标注
package proto

import (
	"testing"
)

func TestFieldMatrix_LookupKnownField(t *testing.T) {
	m := DefaultFieldMatrix()
	cases := []struct {
		client       ClientProtocol
		upstream     UpstreamProtocol
		field        string
		wantVerdict  FieldVerdict
		wantTransKnd FieldTransformKind
	}{
		// OpenAI typed-known
		{ClientProtocolOpenAIChat, UpstreamProtocolOpenAI, "id", FieldPreserved, FieldTransformNone},
		{ClientProtocolOpenAIChat, UpstreamProtocolOpenAI, "model", FieldPreserved, FieldTransformNone},
		// OpenAI transformed (lossy: enum 映射多对一)
		{ClientProtocolOpenAIChat, UpstreamProtocolOpenAI, "finish_reason", FieldTransformed, FieldTransformLossy},
		// OpenAI vendor passthrough
		{ClientProtocolOpenAIChat, UpstreamProtocolOpenAI, "system_fingerprint", FieldPreserved, FieldTransformNone},
		{ClientProtocolOpenAIChat, UpstreamProtocolOpenAI, "service_tier", FieldPreserved, FieldTransformNone},
		{ClientProtocolOpenAIChat, UpstreamProtocolOpenAI, "prompt_filter_results", FieldPreserved, FieldTransformNone},
		// Anthropic typed-known
		{ClientProtocolAnthropicMessages, UpstreamProtocolAnthropic, "type", FieldPreserved, FieldTransformNone},
		// Anthropic transformed (lossy: mapStopReason 有 default→Unknown 分支)
		{ClientProtocolAnthropicMessages, UpstreamProtocolAnthropic, "stop_reason", FieldTransformed, FieldTransformLossy},
		// Anthropic vendor passthrough
		{ClientProtocolAnthropicMessages, UpstreamProtocolAnthropic, "cache_creation_input_tokens", FieldPreserved, FieldTransformNone},
		{ClientProtocolAnthropicMessages, UpstreamProtocolAnthropic, "cache_read_input_tokens", FieldPreserved, FieldTransformNone},
		// Bedrock-on-Anthropic 路径
		{ClientProtocolAnthropicMessages, UpstreamProtocolBedrock, "type", FieldPreserved, FieldTransformNone},
		{ClientProtocolAnthropicMessages, UpstreamProtocolBedrock, "stop_reason", FieldTransformed, FieldTransformLossy},
		{ClientProtocolAnthropicMessages, UpstreamProtocolBedrock, "cache_creation_input_tokens", FieldPreserved, FieldTransformNone},
	}
	for _, tc := range cases {
		t.Run(string(tc.client)+"/"+string(tc.upstream)+"/"+tc.field, func(t *testing.T) {
			entry := m.Lookup(tc.client, tc.upstream, tc.field)
			if entry.Verdict != tc.wantVerdict {
				t.Errorf("Verdict=%q want %q", entry.Verdict, tc.wantVerdict)
			}
			if entry.TransformKind != tc.wantTransKnd {
				t.Errorf("TransformKind=%q want %q", entry.TransformKind, tc.wantTransKnd)
			}
			// 已登记 entry 应有非空 Reason
			if entry.Reason == "" {
				t.Errorf("已登记 entry 应有非空 Reason")
			}
		})
	}
}

// TestFieldMatrix_UnknownField_PreservedDefault 核心契约：未登记字段返回
// FieldPreservedDefault（不是 FieldUnsupported）。这是 PRESERVE-by-default
// 升级语义。
func TestFieldMatrix_UnknownField_PreservedDefault(t *testing.T) {
	m := DefaultFieldMatrix()
	cases := []struct {
		client   ClientProtocol
		upstream UpstreamProtocol
		field    string
	}{
		{ClientProtocolOpenAIChat, UpstreamProtocolOpenAI, "future_field_2027"},
		{ClientProtocolOpenAIChat, UpstreamProtocolOpenAI, "experimental_xyz"},
		{ClientProtocolAnthropicMessages, UpstreamProtocolAnthropic, "new_capability_v3"},
		{ClientProtocolAnthropicMessages, UpstreamProtocolBedrock, "bedrock_specific_unknown"},
	}
	for _, tc := range cases {
		t.Run(tc.field, func(t *testing.T) {
			entry := m.Lookup(tc.client, tc.upstream, tc.field)
			if entry.Verdict != FieldPreservedDefault {
				t.Errorf("Lookup 未登记字段 %q 应返回 FieldPreservedDefault，得 %q",
					tc.field, entry.Verdict)
			}
			if entry.Reason == "" {
				t.Errorf("默认 entry 应有 Reason 说明 preserve-by-default")
			}
		})
	}
}

// TestFieldMatrix_LookupVerdict_ShortcutEquivalentToLookup 短路 API 等价。
func TestFieldMatrix_LookupVerdict_ShortcutEquivalentToLookup(t *testing.T) {
	m := DefaultFieldMatrix()
	got := m.LookupVerdict(ClientProtocolOpenAIChat, UpstreamProtocolOpenAI, "system_fingerprint")
	if got != FieldPreserved {
		t.Errorf("LookupVerdict 应等价 Lookup().Verdict，得 %q", got)
	}
	got2 := m.LookupVerdict(ClientProtocolOpenAIChat, UpstreamProtocolOpenAI, "future_unknown")
	if got2 != FieldPreservedDefault {
		t.Errorf("未登记 LookupVerdict 应返回 FieldPreservedDefault，得 %q", got2)
	}
}

// TestFieldMatrix_UnregisteredClientUpstreamPair 验证未登记 (client, upstream)
// 对应返回 FieldPreservedDefault，
// 不应"借"另一对的 verdict。两个方向都测——sonnet debugger 提出的
// "client/upstream order matters" 守界。
func TestFieldMatrix_UnregisteredClientUpstreamPair(t *testing.T) {
	m := DefaultFieldMatrix()
	cases := []struct {
		name     string
		client   ClientProtocol
		upstream UpstreamProtocol
		field    string
	}{
		// OpenAI client × Anthropic upstream 当前未登记
		{"openai_chat→anthropic", ClientProtocolOpenAIChat, UpstreamProtocolAnthropic, "id"},
		// 反方向：Anthropic client × OpenAI upstream 也未登记，且与上述是不同 key
		{"anthropic_messages→openai", ClientProtocolAnthropicMessages, UpstreamProtocolOpenAI, "stop_reason"},
		// stop_reason 在 (Anthropic, Anthropic) 下是 Transformed Lossy；在
		// (Anthropic, OpenAI) 下未登记，必须是 PreservedDefault（不串线）
		{"anthropic_messages→openai-known-anthropic-field", ClientProtocolAnthropicMessages, UpstreamProtocolOpenAI, "type"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			entry := m.Lookup(tc.client, tc.upstream, tc.field)
			if entry.Verdict != FieldPreservedDefault {
				t.Errorf("未登记 (client=%s, upstream=%s, field=%q) 应返回 FieldPreservedDefault，得 %q",
					tc.client, tc.upstream, tc.field, entry.Verdict)
			}
		})
	}
}

// TestFieldMatrix_HasEntry 验证 HasEntry 让运维能区分"已登记保留"vs
// "未登记默认保留"——值相同但状态不同。
func TestFieldMatrix_HasEntry(t *testing.T) {
	m := DefaultFieldMatrix()
	if !m.HasEntry(ClientProtocolOpenAIChat, UpstreamProtocolOpenAI, "system_fingerprint") {
		t.Error("system_fingerprint 应已登记")
	}
	if m.HasEntry(ClientProtocolOpenAIChat, UpstreamProtocolOpenAI, "future_unknown_field") {
		t.Error("未登记字段不应 HasEntry=true")
	}
	if m.HasEntry(ClientProtocolOpenAIChat, UpstreamProtocolAnthropic, "id") {
		t.Error("未登记 (client,upstream) 对的 HasEntry 应 false")
	}
}

// TestFieldMatrix_NilSafe 空 matrix 也不 panic。
func TestFieldMatrix_NilSafe(t *testing.T) {
	var m FieldMatrix
	entry := m.Lookup(ClientProtocolOpenAIChat, UpstreamProtocolOpenAI, "id")
	if entry.Verdict != FieldPreservedDefault {
		t.Errorf("nil matrix Lookup 应返回 FieldPreservedDefault，得 %q", entry.Verdict)
	}
	if m.HasEntry(ClientProtocolOpenAIChat, UpstreamProtocolOpenAI, "id") {
		t.Error("nil matrix HasEntry 应 false")
	}
}

// TestFieldMatrix_RegisteredEntriesCoverAllClientsThatHaveAdapters 守界：
// 每个有 adapter 的 (client, upstream) 对应至少有 typed-known 字段登记。
func TestFieldMatrix_RegisteredEntriesCoverAllClientsThatHaveAdapters(t *testing.T) {
	m := DefaultFieldMatrix()
	requiredPairs := []struct {
		client   ClientProtocol
		upstream UpstreamProtocol
	}{
		{ClientProtocolOpenAIChat, UpstreamProtocolOpenAI},
		{ClientProtocolAnthropicMessages, UpstreamProtocolAnthropic},
		{ClientProtocolAnthropicMessages, UpstreamProtocolBedrock},
	}
	for _, p := range requiredPairs {
		t.Run(string(p.client)+"->"+string(p.upstream), func(t *testing.T) {
			byUpstream, ok := m[p.client]
			if !ok {
				t.Errorf("matrix 缺 client=%s 全表", p.client)
				return
			}
			byField, ok := byUpstream[p.upstream]
			if !ok || len(byField) == 0 {
				t.Errorf("matrix[%s][%s] 应至少含若干 typed-known 字段", p.client, p.upstream)
			}
		})
	}
}

// TestFieldMatrix_TransformedEntriesAlwaysHaveTransformKind 验证 transformed
// entry 必带 lossy/lossless 标注。
func TestFieldMatrix_TransformedEntriesAlwaysHaveTransformKind(t *testing.T) {
	m := DefaultFieldMatrix()
	for client, byUpstream := range m {
		for upstream, byField := range byUpstream {
			for fieldName, entry := range byField {
				if entry.Verdict != FieldTransformed {
					continue
				}
				if entry.TransformKind != FieldTransformLossless && entry.TransformKind != FieldTransformLossy {
					t.Errorf("[%s/%s/%s] FieldTransformed entry 必带 TransformKind (lossy/lossless)，得 %q",
						client, upstream, fieldName, entry.TransformKind)
				}
			}
		}
	}
}
