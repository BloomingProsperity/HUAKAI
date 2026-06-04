package proto

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// minimalValidEnvelope 构造一份恰好满足要求的最小 envelope。
func minimalValidEnvelope() *HCSFEnvelope {
	env := NewEmptyEnvelope()
	env.RequestMeta = RequestMeta{
		RequestID:      "req_test_001",
		ClientProtocol: "openai_chat",
		ProtocolFamily: "openai_chat",
		Model:          "gpt-4o-mini",
		IngressPath:    "/v1/chat/completions",
	}
	return env
}

// TestINV4_VersionLocked 验证 Version 必须是 "0.4"。
func TestINV4_VersionLocked(t *testing.T) {
	env := minimalValidEnvelope()
	if err := ValidateEnvelope(env); err != nil {
		t.Fatalf("baseline envelope must validate, got: %v", err)
	}

	env.Version = "0.3"
	err := ValidateEnvelope(env)
	if err == nil || !strings.Contains(err.Error(), "INV-4") {
		t.Fatalf("expected INV-4 violation, got: %v", err)
	}
}

// TestINV5_RequiredFields 验证 RequestMeta 必填字段。
func TestINV5_RequiredFields(t *testing.T) {
	cases := []struct {
		name string
		mut  func(m *RequestMeta)
	}{
		{"missing RequestID", func(m *RequestMeta) { m.RequestID = "" }},
		{"missing ClientProtocol", func(m *RequestMeta) { m.ClientProtocol = "" }},
		{"missing ProtocolFamily", func(m *RequestMeta) { m.ProtocolFamily = "" }},
		{"missing Model", func(m *RequestMeta) { m.Model = "" }},
		{"missing IngressPath", func(m *RequestMeta) { m.IngressPath = "" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := minimalValidEnvelope()
			tc.mut(&env.RequestMeta)
			err := ValidateEnvelope(env)
			if err == nil || !strings.Contains(err.Error(), "INV-5") {
				t.Fatalf("expected INV-5 violation for %s, got: %v", tc.name, err)
			}
		})
	}
}

// TestINV6_BufferedAndStreamMutex 验证 BufferedResponse + StreamEvents 至多一个非 nil。
func TestINV6_BufferedAndStreamMutex(t *testing.T) {
	env := minimalValidEnvelope()
	env.BufferedResponse = &CanonicalResponse{ID: "r", Model: "m"}
	env.StreamEvents = []CanonicalEvent{{Type: "message_start"}}
	err := ValidateEnvelope(env)
	if err == nil || !strings.Contains(err.Error(), "INV-6") {
		t.Fatalf("expected INV-6 violation, got: %v", err)
	}
}

// TestINV3_TaggedUnionConsistency 验证 Kind=text 必须 Text!=nil 且其它全 nil。
func TestINV3_TaggedUnionConsistency(t *testing.T) {
	env := minimalValidEnvelope()

	// Kind=text 但 Text=nil → 违反
	env.CapabilityGraph.Nodes = []CapabilityNode{{
		ID:          "n1",
		Kind:        CapabilityText,
		StreamReady: StreamReadyYes,
	}}
	err := ValidateEnvelope(env)
	if err == nil || !strings.Contains(err.Error(), "INV-3") {
		t.Fatalf("expected INV-3 (kind=text but Text==nil), got: %v", err)
	}

	// Kind=text 且 Text!=nil 且其它全 nil → 通过
	env.CapabilityGraph.Nodes = []CapabilityNode{{
		ID:          "n1",
		Kind:        CapabilityText,
		StreamReady: StreamReadyYes,
		Text:        &TextNode{Role: "user", Block: CanonicalContentBlock{Type: "text", Text: "hi"}},
	}}
	if err := ValidateEnvelope(env); err != nil {
		t.Fatalf("text node should validate, got: %v", err)
	}

	// Kind=text 但同时设置 Text + ToolUse → 违反
	env.CapabilityGraph.Nodes[0].ToolUse = &ToolUseNode{ToolCallID: "t1", Name: "x", Input: json.RawMessage(`{}`), Status: ToolNodeComplete}
	err = ValidateEnvelope(env)
	if err == nil || !strings.Contains(err.Error(), "INV-3") {
		t.Fatalf("expected INV-3 (multiple non-nil payloads), got: %v", err)
	}

	// Kind=tool_use 但 Text 非 nil（payload 不匹配）→ 违反
	env.CapabilityGraph.Nodes[0] = CapabilityNode{
		ID:          "n1",
		Kind:        CapabilityToolUse,
		StreamReady: StreamReadyYes,
		Text:        &TextNode{Role: "user", Block: CanonicalContentBlock{Type: "text"}},
	}
	err = ValidateEnvelope(env)
	if err == nil || !strings.Contains(err.Error(), "INV-3") {
		t.Fatalf("expected INV-3 (kind/payload mismatch), got: %v", err)
	}
}

// TestINV7_NoSilentDrop 验证 ProtocolLoss 必须有 Reason/Note/Verdict/Code 之一。
func TestINV7_NoSilentDrop(t *testing.T) {
	env := minimalValidEnvelope()
	env.CapabilityGraph.ProtocolLoss = []ProtocolLossEntry{{Field: "x"}} // 全 v0.3 v0.4 都空 → silent drop
	err := ValidateEnvelope(env)
	if err == nil || !strings.Contains(err.Error(), "INV-7") {
		t.Fatalf("expected INV-7 silent drop violation, got: %v", err)
	}

	// 加 Reason → 通过
	env.CapabilityGraph.ProtocolLoss[0].Reason = "explicit reason"
	if err := ValidateEnvelope(env); err != nil {
		t.Fatalf("with Reason set, should validate: %v", err)
	}
}

// TestINV8_EdgeReferentialIntegrity 验证 edge.From/To 必须存在于 Nodes。
func TestINV8_EdgeReferentialIntegrity(t *testing.T) {
	env := minimalValidEnvelope()
	env.CapabilityGraph.Nodes = []CapabilityNode{{
		ID: "n1", Kind: CapabilityText, StreamReady: StreamReadyYes,
		Text: &TextNode{Role: "user", Block: CanonicalContentBlock{Type: "text"}},
	}}
	env.CapabilityGraph.Edges = []CapabilityEdge{{
		ID: "e1", Type: EdgeProvides, From: "n1", To: "n_missing", Required: false,
	}}
	err := ValidateEnvelope(env)
	if err == nil || !strings.Contains(err.Error(), "INV-8") {
		t.Fatalf("expected INV-8 dangling edge, got: %v", err)
	}
}

// TestINV9_NoBidirectionalMutex 验证 EdgeMutuallyExclusive 不能 A↔B 双向。
func TestINV9_NoBidirectionalMutex(t *testing.T) {
	env := minimalValidEnvelope()
	env.CapabilityGraph.Nodes = []CapabilityNode{
		{ID: "a", Kind: CapabilityText, StreamReady: StreamReadyYes, Text: &TextNode{Role: "user", Block: CanonicalContentBlock{Type: "text"}}},
		{ID: "b", Kind: CapabilityText, StreamReady: StreamReadyYes, Text: &TextNode{Role: "assistant", Block: CanonicalContentBlock{Type: "text"}}},
	}
	env.CapabilityGraph.Edges = []CapabilityEdge{
		{ID: "e1", Type: EdgeMutuallyExclusive, From: "a", To: "b"},
		{ID: "e2", Type: EdgeMutuallyExclusive, From: "b", To: "a"},
	}
	err := ValidateEnvelope(env)
	if err == nil || !strings.Contains(err.Error(), "INV-9") {
		t.Fatalf("expected INV-9 bidirectional mutex, got: %v", err)
	}
}

// TestINV10_DataRetentionEnum 验证 DataRetentionLabel 严格枚举。
func TestINV10_DataRetentionEnum(t *testing.T) {
	env := minimalValidEnvelope()
	env.Policy.DataRetention.Value = "invented_label"
	err := ValidateEnvelope(env)
	if err == nil || !strings.Contains(err.Error(), "INV-10") {
		t.Fatalf("expected INV-10 enum violation, got: %v", err)
	}

	env.Policy.DataRetention.Value = DataRetentionUnknown
	if err := ValidateEnvelope(env); err != nil {
		t.Fatalf("with unknown label, should validate: %v", err)
	}
}

// TestINV11_MidStreamFallbackDefault 验证 P-0 默认 MidStreamFallbackNone；非 none 拒绝。
func TestINV11_MidStreamFallbackDefault(t *testing.T) {
	env := minimalValidEnvelope()
	env.StreamPlan.MidStreamFallbackPolicy = MidStreamFallbackContinuation
	err := ValidateEnvelope(env)
	if err == nil || !strings.Contains(err.Error(), "INV-11") {
		t.Fatalf("expected INV-11 violation, got: %v", err)
	}
}

// TestINV12_ExtensionsKeyPrefix 验证 Extensions key 前缀。
func TestINV12_ExtensionsKeyPrefix(t *testing.T) {
	env := minimalValidEnvelope()
	env.Extensions = map[string]json.RawMessage{
		"random_key": json.RawMessage(`"x"`),
	}
	err := ValidateEnvelope(env)
	if err == nil || !strings.Contains(err.Error(), "INV-12") {
		t.Fatalf("expected INV-12 violation, got: %v", err)
	}

	env.Extensions = map[string]json.RawMessage{
		"vendor:openai":      json.RawMessage(`"x"`),
		"experimental:quota": json.RawMessage(`{}`),
	}
	if err := ValidateEnvelope(env); err != nil {
		t.Fatalf("with valid prefixes, should validate: %v", err)
	}
}

// TestINV1_RoundTripDeepEqual 验证 marshal → unmarshal → marshal 字段顺序无关 deep equal。
func TestINV1_RoundTripDeepEqual(t *testing.T) {
	src := minimalValidEnvelope()
	src.CapabilityGraph.Nodes = []CapabilityNode{{
		ID: "n1", Kind: CapabilityText, StreamReady: StreamReadyYes,
		Text: &TextNode{Role: "user", Block: CanonicalContentBlock{Type: "text", Text: "hi"}},
	}}

	raw, err := json.Marshal(src)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var round HCSFEnvelope
	if err := json.Unmarshal(raw, &round); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !reflect.DeepEqual(*src, round) {
		t.Fatalf("round-trip mismatch:\n src=%+v\nback=%+v", src, round)
	}
}

// TestINV13_StreamPlanModeRequired 验证 StreamPlan.Mode 必填。
func TestINV13_StreamPlanModeRequired(t *testing.T) {
	env := minimalValidEnvelope()
	env.StreamPlan.Mode = ""
	err := ValidateEnvelope(env)
	if err == nil || !strings.Contains(err.Error(), "INV-13") {
		t.Fatalf("expected INV-13 (Mode required) violation, got: %v", err)
	}
}

// TestINV13_StreamPlanModeBogus 验证 StreamPlan.Mode 非合法枚举值被拒。
func TestINV13_StreamPlanModeBogus(t *testing.T) {
	env := minimalValidEnvelope()
	env.StreamPlan.Mode = StreamMode("bogus_mode")
	err := ValidateEnvelope(env)
	if err == nil || !strings.Contains(err.Error(), "INV-13") {
		t.Fatalf("expected INV-13 (bogus Mode) violation, got: %v", err)
	}
}

// TestINV13_StreamPlanModeAllowedValues 验证 buffered/streaming/replay 全部通过。
func TestINV13_StreamPlanModeAllowedValues(t *testing.T) {
	for _, mode := range []StreamMode{StreamModeBuffered, StreamModeStreaming, StreamModeReplay} {
		t.Run(string(mode), func(t *testing.T) {
			env := minimalValidEnvelope()
			env.StreamPlan.Mode = mode
			if err := ValidateEnvelope(env); err != nil {
				t.Fatalf("Mode=%q should validate, got: %v", mode, err)
			}
		})
	}
}

// TestINV6_NilVsEmptyStreamEvents 验证 区分 nil vs empty StreamEvents。
//
//   - BufferedResponse + StreamEvents:nil → 通过（仅 buffered 形态）
//   - BufferedResponse + StreamEvents:[] → 通过（显式空切片，非 replay payload）
//   - BufferedResponse + StreamEvents:[event{...}] → 报错
func TestINV6_NilVsEmptyStreamEvents(t *testing.T) {
	t.Run("buffered + nil events passes", func(t *testing.T) {
		env := minimalValidEnvelope()
		env.BufferedResponse = &CanonicalResponse{ID: "r", Model: "m"}
		env.StreamEvents = nil
		if err := ValidateEnvelope(env); err != nil {
			t.Fatalf("buffered + nil StreamEvents should validate, got: %v", err)
		}
	})
	t.Run("buffered + empty slice passes", func(t *testing.T) {
		env := minimalValidEnvelope()
		env.BufferedResponse = &CanonicalResponse{ID: "r", Model: "m"}
		env.StreamEvents = []CanonicalEvent{}
		if err := ValidateEnvelope(env); err != nil {
			t.Fatalf("buffered + empty StreamEvents should validate, got: %v", err)
		}
	})
	t.Run("buffered + non-empty events fails INV-6", func(t *testing.T) {
		env := minimalValidEnvelope()
		env.BufferedResponse = &CanonicalResponse{ID: "r", Model: "m"}
		env.StreamEvents = []CanonicalEvent{{Type: "message_start"}}
		err := ValidateEnvelope(env)
		if err == nil || !strings.Contains(err.Error(), "INV-6") {
			t.Fatalf("expected INV-6, got: %v", err)
		}
	})
}

// TestINV8_EdgeIDRequired 验证 edge.ID 必填。
func TestINV8_EdgeIDRequired(t *testing.T) {
	env := minimalValidEnvelope()
	env.CapabilityGraph.Nodes = []CapabilityNode{
		{ID: "a", Kind: CapabilityText, StreamReady: StreamReadyYes, Text: &TextNode{Role: "user", Block: CanonicalContentBlock{Type: "text"}}},
		{ID: "b", Kind: CapabilityText, StreamReady: StreamReadyYes, Text: &TextNode{Role: "assistant", Block: CanonicalContentBlock{Type: "text"}}},
	}
	env.CapabilityGraph.Edges = []CapabilityEdge{
		{ID: "", Type: EdgeProvides, From: "a", To: "b"}, // 缺 ID
	}
	err := ValidateEnvelope(env)
	if err == nil || !strings.Contains(err.Error(), "INV-8") {
		t.Fatalf("expected INV-8 (edge ID required), got: %v", err)
	}
}

// TestINV8_EdgeIDDuplicate 验证 edge ID 不可重复。
func TestINV8_EdgeIDDuplicate(t *testing.T) {
	env := minimalValidEnvelope()
	env.CapabilityGraph.Nodes = []CapabilityNode{
		{ID: "a", Kind: CapabilityText, StreamReady: StreamReadyYes, Text: &TextNode{Role: "user", Block: CanonicalContentBlock{Type: "text"}}},
		{ID: "b", Kind: CapabilityText, StreamReady: StreamReadyYes, Text: &TextNode{Role: "assistant", Block: CanonicalContentBlock{Type: "text"}}},
	}
	env.CapabilityGraph.Edges = []CapabilityEdge{
		{ID: "e_dup", Type: EdgeProvides, From: "a", To: "b"},
		{ID: "e_dup", Type: EdgeRequires, From: "b", To: "a"},
	}
	err := ValidateEnvelope(env)
	if err == nil || !strings.Contains(err.Error(), "INV-8") {
		t.Fatalf("expected INV-8 (duplicate edge ID), got: %v", err)
	}
}

// TestINV8_EdgeTypeRequired 验证 edge.Type 必填。
func TestINV8_EdgeTypeRequired(t *testing.T) {
	env := minimalValidEnvelope()
	env.CapabilityGraph.Nodes = []CapabilityNode{
		{ID: "a", Kind: CapabilityText, StreamReady: StreamReadyYes, Text: &TextNode{Role: "user", Block: CanonicalContentBlock{Type: "text"}}},
		{ID: "b", Kind: CapabilityText, StreamReady: StreamReadyYes, Text: &TextNode{Role: "assistant", Block: CanonicalContentBlock{Type: "text"}}},
	}
	env.CapabilityGraph.Edges = []CapabilityEdge{
		{ID: "e1", Type: "", From: "a", To: "b"},
	}
	err := ValidateEnvelope(env)
	if err == nil || !strings.Contains(err.Error(), "INV-8") {
		t.Fatalf("expected INV-8 (edge Type required), got: %v", err)
	}
}

// TestINV8_EdgeTypeBogus 验证 edge.Type 不在 AllEdgeTypes 被拒。
func TestINV8_EdgeTypeBogus(t *testing.T) {
	env := minimalValidEnvelope()
	env.CapabilityGraph.Nodes = []CapabilityNode{
		{ID: "a", Kind: CapabilityText, StreamReady: StreamReadyYes, Text: &TextNode{Role: "user", Block: CanonicalContentBlock{Type: "text"}}},
		{ID: "b", Kind: CapabilityText, StreamReady: StreamReadyYes, Text: &TextNode{Role: "assistant", Block: CanonicalContentBlock{Type: "text"}}},
	}
	env.CapabilityGraph.Edges = []CapabilityEdge{
		{ID: "e1", Type: CapabilityEdgeType("bogus_kind"), From: "a", To: "b"},
	}
	err := ValidateEnvelope(env)
	if err == nil || !strings.Contains(err.Error(), "INV-8") {
		t.Fatalf("expected INV-8 (edge Type not in AllEdgeTypes), got: %v", err)
	}
}

// TestINV7_EdgeProtocolLossSilentDrop 验证 edge 自身 ProtocolLoss 也被 silent drop 检查。
func TestINV7_EdgeProtocolLossSilentDrop(t *testing.T) {
	env := minimalValidEnvelope()
	env.CapabilityGraph.Nodes = []CapabilityNode{
		{ID: "a", Kind: CapabilityText, StreamReady: StreamReadyYes, Text: &TextNode{Role: "user", Block: CanonicalContentBlock{Type: "text"}}},
		{ID: "b", Kind: CapabilityText, StreamReady: StreamReadyYes, Text: &TextNode{Role: "assistant", Block: CanonicalContentBlock{Type: "text"}}},
	}
	env.CapabilityGraph.Edges = []CapabilityEdge{
		{
			ID: "e1", Type: EdgeLoses, From: "a", To: "b",
			ProtocolLoss: []ProtocolLossEntry{{Field: "x"}}, // 全字段空 → silent drop
		},
	}
	err := ValidateEnvelope(env)
	if err == nil || !strings.Contains(err.Error(), "INV-7") {
		t.Fatalf("expected INV-7 (edge ProtocolLoss silent drop), got: %v", err)
	}

	// 加 Reason → 通过
	env.CapabilityGraph.Edges[0].ProtocolLoss[0].Reason = "downgrade due to vendor"
	if err := ValidateEnvelope(env); err != nil {
		t.Fatalf("with edge.ProtocolLoss.Reason set, should validate: %v", err)
	}
}

// TestINV3_ProjectionCapabilityRequired 验证 ProviderProjection.CapabilityResults[i].Capability 必填。
func TestINV3_ProjectionCapabilityRequired(t *testing.T) {
	env := minimalValidEnvelope()
	env.ProviderProjection.CapabilityResults = []CapabilityProjection{
		{Capability: "", Verdict: ProjectionPreserved}, // Capability 缺失
	}
	err := ValidateEnvelope(env)
	if err == nil || !strings.Contains(err.Error(), "INV-3") {
		t.Fatalf("expected INV-3 (Capability required), got: %v", err)
	}
}

// TestINV3_ProjectionCapabilityNotInEnum 验证 Capability 必须在 AllCapabilityKinds。
func TestINV3_ProjectionCapabilityNotInEnum(t *testing.T) {
	env := minimalValidEnvelope()
	env.ProviderProjection.CapabilityResults = []CapabilityProjection{
		{Capability: CapabilityKind("invented"), Verdict: ProjectionPreserved},
	}
	err := ValidateEnvelope(env)
	if err == nil || !strings.Contains(err.Error(), "INV-3") {
		t.Fatalf("expected INV-3 (Capability not in enum), got: %v", err)
	}
}

// TestINV7_ProjectionVerdictRequired 验证 ProviderProjection.CapabilityResults[i].Verdict 必填。
func TestINV7_ProjectionVerdictRequired(t *testing.T) {
	env := minimalValidEnvelope()
	env.ProviderProjection.CapabilityResults = []CapabilityProjection{
		{Capability: CapabilityText, Verdict: ""},
	}
	err := ValidateEnvelope(env)
	if err == nil || !strings.Contains(err.Error(), "INV-7") {
		t.Fatalf("expected INV-7 (Verdict required), got: %v", err)
	}
}

// TestINV7_ProjectionVerdictNotInEnum 验证 Verdict 必须在 ProjectionVerdict 枚举内。
func TestINV7_ProjectionVerdictNotInEnum(t *testing.T) {
	env := minimalValidEnvelope()
	env.ProviderProjection.CapabilityResults = []CapabilityProjection{
		{Capability: CapabilityText, Verdict: ProjectionVerdict("yolo")},
	}
	err := ValidateEnvelope(env)
	if err == nil || !strings.Contains(err.Error(), "INV-7") {
		t.Fatalf("expected INV-7 (Verdict not in enum), got: %v", err)
	}
}

// TestINV7_ProjectionPreservedHappyPath 验证合法 projection（preserved + 合法 capability）通过。
func TestINV7_ProjectionPreservedHappyPath(t *testing.T) {
	env := minimalValidEnvelope()
	env.ProviderProjection.CapabilityResults = []CapabilityProjection{
		{Capability: CapabilityText, Verdict: ProjectionPreserved},
	}
	if err := ValidateEnvelope(env); err != nil {
		t.Fatalf("preserved projection should validate, got: %v", err)
	}
}

// TestINV7_ProtocolLossEntryV04SilentDrop 验证 v0.4 entry（Severity 已设）必须有 Reason 或 Code。
func TestINV7_ProtocolLossEntryV04SilentDrop(t *testing.T) {
	cases := []struct {
		name   string
		entry  ProtocolLossEntry
		silent bool
	}{
		{
			name:   "v0.4 severity-only verdict-only is silent drop",
			entry:  ProtocolLossEntry{Severity: ProtocolLossWarning, Verdict: VerdictLossy},
			silent: true,
		},
		{
			name:   "v0.4 severity + reason is OK",
			entry:  ProtocolLossEntry{Severity: ProtocolLossWarning, Reason: "downgrade explained"},
			silent: false,
		},
		{
			name:   "v0.4 severity + code is OK",
			entry:  ProtocolLossEntry{Severity: ProtocolLossError, Code: "unsupported_capability"},
			silent: false,
		},
		{
			name:   "v0.3 verdict-only kept compatible (no severity)",
			entry:  ProtocolLossEntry{Verdict: VerdictLossy},
			silent: false,
		},
		{
			name:   "v0.3 note-only kept compatible",
			entry:  ProtocolLossEntry{Note: "old adapter explanation"},
			silent: false,
		},
		{
			name:   "completely empty is silent drop",
			entry:  ProtocolLossEntry{Field: "x"},
			silent: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.entry.IsSilentDrop(); got != tc.silent {
				t.Fatalf("IsSilentDrop(%+v) = %v, want %v", tc.entry, got, tc.silent)
			}
		})
	}
}

// roundTripBytesStable 工具函数：marshal → unmarshal → marshal 字节稳定 + DeepEqual。
//
// 用于 M3 全集 round-trip 校验：
//
//   - json.Marshal(src) 必须 == json.Marshal(round)（字段顺序无关字节稳定）
//   - reflect.DeepEqual(*src, round) 必须为 true
//   - 在 marshal 之前调用 ValidateEnvelope(src)，确保 fixture 合法
func roundTripBytesStable(t *testing.T, src *HCSFEnvelope) {
	t.Helper()
	if err := ValidateEnvelope(src); err != nil {
		t.Fatalf("fixture failed ValidateEnvelope: %v", err)
	}
	raw1, err := json.Marshal(src)
	if err != nil {
		t.Fatalf("marshal src: %v", err)
	}
	var round HCSFEnvelope
	if err := json.Unmarshal(raw1, &round); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !reflect.DeepEqual(*src, round) {
		t.Fatalf("DeepEqual mismatch:\n src=%+v\nback=%+v", src, round)
	}
	raw2, err := json.Marshal(&round)
	if err != nil {
		t.Fatalf("marshal round: %v", err)
	}
	if string(raw1) != string(raw2) {
		t.Fatalf("INV-1 byte-stable mismatch:\n raw1=%s\n raw2=%s", raw1, raw2)
	}
}

// makeFullCapabilityNodes 构造 15 concrete CapabilityKind payload 的 node 集合。
//
// 每个 node 都满足 tagged-union（恰好一个 payload 非 nil）；ID 命名 `n_<kind>_1`。
// 跨 15 capability：text / tool_use / tool_result / thinking / cache_control /
// structured_output / computer_use / file / image / audio / video / live_session /
// batch / mcp_server / data_retention（与 AllCapabilityKinds 同序）。
func makeFullCapabilityNodes() []CapabilityNode {
	intPtr := func(v int) *int { return &v }
	boolPtr := func(v bool) *bool { return &v }
	_ = intPtr
	_ = boolPtr

	return []CapabilityNode{
		{
			ID:          "n_text_1",
			Kind:        CapabilityText,
			StreamReady: StreamReadyYes,
			Source: &NodeSourceRef{
				MessageIndex: intPtr(0),
				BlockIndex:   intPtr(0),
				RequestField: "messages[0].content[0]",
			},
			Text: &TextNode{
				Role:  "user",
				Block: CanonicalContentBlock{Type: "text", Text: "hello world"},
			},
		},
		{
			ID:          "n_tool_use_1",
			Kind:        CapabilityToolUse,
			StreamReady: StreamReadyPartial,
			ToolUse: &ToolUseNode{
				ToolCallID:         "tool_abc123",
				OriginalToolCallID: "call_xyz",
				Name:               "search",
				DisplayName:        "Web Search",
				Input:              json.RawMessage(`{"q":"hcsf"}`),
				PartialInput:       json.RawMessage(`{"q":"hc"}`),
				Status:             ToolNodePartial,
			},
		},
		{
			ID:          "n_tool_result_1",
			Kind:        CapabilityToolResult,
			StreamReady: StreamReadyYes,
			ToolResult: &ToolResultNode{
				ToolCallID: "tool_abc123",
				Content: []CanonicalContentBlock{
					{Type: "text", Text: "result snippet"},
				},
				Status:  ToolNodeComplete,
				IsError: false,
			},
		},
		{
			ID:          "n_thinking_1",
			Kind:        CapabilityThinking,
			StreamReady: StreamReadyYes,
			Thinking: &ThinkingNode{
				BudgetTokens: 4096,
				Blocks: []CanonicalContentBlock{
					{Type: "text", Text: "let me reason about this"},
				},
				HiddenTokens: 128,
				Signature:    "sig_anthropic_v1",
				Redaction:    RedactionPublic,
			},
		},
		{
			ID:          "n_cache_control_1",
			Kind:        CapabilityCacheControl,
			StreamReady: StreamReadyNo,
			CacheControl: &CacheControlNode{
				Scope:                    CacheScopeMessage,
				BreakpointRefs:           []string{"n_text_1"},
				CacheKeyHint:             "hash:abcdef",
				CacheCreationInputTokens: 1024,
				CacheReadInputTokens:     0,
				SanitizeSystemMetadata:   true,
				LocalityHint:             "account_pin",
			},
		},
		{
			ID:          "n_structured_output_1",
			Kind:        CapabilityStructuredOutput,
			StreamReady: StreamReadyPartial,
			StructuredOutput: &StructuredOutputNode{
				Mode:             StructuredOutputJSONSchema,
				Strict:           true,
				Schema:           json.RawMessage(`{"type":"object","properties":{"x":{"type":"number"}}}`),
				ParserMode:       "provider",
				FailureRecovery:  "retry",
				FallbackStrategy: "tool",
			},
		},
		{
			ID:          "n_computer_use_1",
			Kind:        CapabilityComputerUse,
			StreamReady: StreamReadyNo,
			ComputerUse: &ComputerUseNode{
				Environment:   "browser",
				Action:        "click",
				Input:         json.RawMessage(`{"x":100,"y":200}`),
				ScreenshotRef: "n_image_1",
				Approval:      ApprovalGranted,
				AuditLabel:    "browser_session_42",
			},
		},
		{
			ID:          "n_file_1",
			Kind:        CapabilityFile,
			StreamReady: StreamReadyNo,
			File: &FileNode{
				SourceKind: DataSourceFileID,
				MediaType:  "application/pdf",
				Locator:    DataLocator{Kind: DataSourceFileID, Value: "file_abc"},
				SizeBytes:  204800,
				Digest:     "sha256:deadbeef",
				Retention:  "regional_asserted",
			},
		},
		{
			ID:          "n_image_1",
			Kind:        CapabilityImage,
			StreamReady: StreamReadyNo,
			Image: &ImageNode{
				SourceKind: DataSourceURL,
				MediaType:  "image/png",
				Locator:    DataLocator{Kind: DataSourceURL, Value: "https://example.test/image.png"},
				Dimensions: &MediaDimensions{Width: 1024, Height: 768},
			},
		},
		{
			ID:          "n_audio_1",
			Kind:        CapabilityAudio,
			StreamReady: StreamReadyPartial,
			Audio: &AudioNode{
				Transport:        MediaTransportInline,
				Format:           "wav",
				Locator:          DataLocator{Kind: DataSourceInlineBase64, Value: "UklGRg=="},
				SampleRateHz:     16000,
				Channels:         1,
				DurationMillis:   3500,
				TranscriptPolicy: TranscriptRequested,
				LiveCompatible:   true,
			},
		},
		{
			ID:          "n_video_1",
			Kind:        CapabilityVideo,
			StreamReady: StreamReadyNo,
			Video: &VideoNode{
				SourceKind: DataSourceURL,
				MediaType:  "video/mp4",
				Locator:    DataLocator{Kind: DataSourceURL, Value: "https://example.test/v.mp4"},
				Dimensions: &MediaDimensions{Width: 1920, Height: 1080},
				TimeRange:  &TimeRange{StartMillis: 0, EndMillis: 60000},
				Codec:      "h264",
				SizeBytes:  10485760,
			},
		},
		{
			ID:          "n_live_session_1",
			Kind:        CapabilityLiveSession,
			StreamReady: StreamReadyYes,
			LiveSession: &LiveSessionNode{
				SessionID:     "live_42",
				Transport:     LiveTransportWSS,
				ConnectParams: json.RawMessage(`{"region":"us-east-1"}`),
				Modalities:    []string{"text", "audio"},
				ToolNodeIDs:   []string{"n_tool_use_1"},
				ResumeToken:   "resume_xyz",
				CloseReason:   "",
			},
		},
		{
			ID:          "n_batch_1",
			Kind:        CapabilityBatch,
			StreamReady: StreamReadyNo,
			Batch: &BatchNode{
				JobID:           "batch_001",
				Endpoint:        "/v1/batches",
				InputRef:        "n_file_1",
				Validation:      BatchValidated,
				OutputRef:       "",
				ErrorRef:        "",
				RetryPolicy:     &RetryPolicy{MaxAttempts: 3, Backoff: "exponential"},
				CostAttribution: "tenant_42",
			},
		},
		{
			ID:          "n_mcp_server_1",
			Kind:        CapabilityMCPServer,
			StreamReady: StreamReadyYes,
			MCPServer: &MCPServerNode{
				ServerLabel:       "internal_kb",
				ServerURI:         "mcp://internal.test/kb",
				AllowedOperations: []string{"search", "fetch"},
				ApprovalRequired:  true,
				AuthRef:           "secret_ref_001",
				InvocationNodeIDs: []string{"n_tool_use_1"},
				ResultNodeIDs:     []string{"n_tool_result_1"},
			},
		},
		{
			ID:          "n_data_retention_1",
			Kind:        CapabilityDataRetention,
			StreamReady: StreamReadyNo,
			DataRetention: &DataRetentionNode{
				Value:        DataRetentionRegionalAsserted,
				Enforcement:  "asserted",
				Region:       "us-east-1",
				RequestStore: boolPtr(false),
				NoTrain:      true,
				EvidenceRef:  "vendor_proof_001",
				AuditLabel:   "regional_us_east_1",
			},
		},
	}
}

// makeFullCapabilityFixture 构造完整 envelope：15 nodes + 5 edges + 3 ProtocolLossEntry。
//
// 边集包含 5 种 EdgeType 各一条；ProtocolLoss 集包含 v0.4 entry / v0.3 兼容 entry / 边自身 loss。
func makeFullCapabilityFixture() *HCSFEnvelope {
	env := minimalValidEnvelope()
	env.CapabilityGraph.Nodes = makeFullCapabilityNodes()
	env.CapabilityGraph.Edges = []CapabilityEdge{
		// requires：tool_result requires tool_use（spec §2.4 典型例子）
		{
			ID: "e_requires_1", Type: EdgeRequires,
			From: "n_tool_result_1", To: "n_tool_use_1",
			Required: true,
			Reason:   "tool_result must reference tool_use call_id",
		},
		// provides：mcp_server provides tool_use 调用通道
		{
			ID: "e_provides_1", Type: EdgeProvides,
			From: "n_mcp_server_1", To: "n_tool_use_1",
			Required: false,
			Reason:   "mcp_server provides tool_use invocation surface",
		},
		// mutually_exclusive：file 与 image 的同 message slot 不可共存（举例）
		{
			ID: "e_mutex_1", Type: EdgeMutuallyExclusive,
			From: "n_file_1", To: "n_image_1",
			Required: false,
		},
		// loses：thinking 在 openai_chat projection 上 lossy → redaction 退化
		{
			ID: "e_loses_1", Type: EdgeLoses,
			From: "n_thinking_1", To: "n_text_1",
			Required: false,
			Reason:   "thinking downgraded to text under non-supporting upstream",
			ProtocolLoss: []ProtocolLossEntry{
				{
					Field:    "thinking.signature",
					Vendor:   "openai_chat",
					Severity: ProtocolLossWarning,
					Reason:   "vendor lacks signature passthrough; signature dropped on edge projection",
					Code:     "downgrade_thinking_to_text",
				},
			},
		},
		// requires_native：computer_use 必须走 native passthrough
		{
			ID: "e_requires_native_1", Type: EdgeRequiresNative,
			From: "n_computer_use_1", To: "n_text_1",
			Required: true,
			Reason:   "computer_use only viable via /v1/native/* passthrough",
		},
	}
	// 图级 ProtocolLoss：3 条覆盖 v0.4 + v0.3 多源
	env.CapabilityGraph.ProtocolLoss = []ProtocolLossEntry{
		{
			Field:      "cache_control.locality_hint",
			Vendor:     "openai_chat",
			Severity:   ProtocolLossInfo,
			Reason:     "locality hint advisory only; non-supporting upstream ignores",
			Suggestion: "promote to PASR account_pin in P-8",
			Capability: CapabilityCacheControl,
			NodeID:     "n_cache_control_1",
			Code:       "advisory_locality_hint",
			Details:    map[string]string{"hint": "account_pin"},
		},
		// v0.3 兼容路径：仅 Verdict + Note
		{
			Feature:   "structured_output_strict",
			Direction: "canonical_to_upstream",
			Verdict:   VerdictLossy,
			Note:      "legacy v0.3 entry preserved for adapter compatibility",
		},
		// v0.4：error severity + native required
		{
			Field:      "live_session.modalities",
			Vendor:     "bedrock",
			Severity:   ProtocolLossError,
			Reason:     "bedrock has no live_session equivalent",
			NativePath: "/v1/native/anthropic/live",
			Capability: CapabilityLiveSession,
			NodeID:     "n_live_session_1",
			Code:       "unsupported_capability",
		},
	}
	// projection：每个 capability 有一条 projection 结果（preserved 路径，无需 ProtocolLoss）
	env.ProviderProjection = ProviderProjection{
		TargetVendor:   "anthropic",
		TargetProtocol: UpstreamProtocolAnthropic,
		CapabilityResults: []CapabilityProjection{
			{Capability: CapabilityText, NodeID: "n_text_1", Verdict: ProjectionPreserved},
			{Capability: CapabilityToolUse, NodeID: "n_tool_use_1", Verdict: ProjectionPreserved},
			{Capability: CapabilityToolResult, NodeID: "n_tool_result_1", Verdict: ProjectionPreserved},
			{Capability: CapabilityThinking, NodeID: "n_thinking_1", Verdict: ProjectionPreserved},
			{Capability: CapabilityCacheControl, NodeID: "n_cache_control_1", Verdict: ProjectionPreserved},
			{
				Capability: CapabilityStructuredOutput,
				NodeID:     "n_structured_output_1",
				Verdict:    ProjectionLossy,
				ProtocolLoss: []ProtocolLossEntry{
					{
						Field:    "structured_output.strict",
						Vendor:   "anthropic",
						Severity: ProtocolLossWarning,
						Reason:   "anthropic enforces strict via tool_strategy; advisory loss recorded",
						Code:     "downgrade_strict_mode",
					},
				},
			},
			{
				Capability: CapabilityComputerUse,
				NodeID:     "n_computer_use_1",
				Verdict:    ProjectionNativeRequired,
				NativePath: "/v1/native/anthropic/computer-use",
				ProtocolLoss: []ProtocolLossEntry{
					{
						Field:      "computer_use",
						Vendor:     "anthropic",
						Severity:   ProtocolLossError,
						Reason:     "computer_use must pass through native route",
						NativePath: "/v1/native/anthropic/computer-use",
						Code:       "native_required",
					},
				},
			},
			{Capability: CapabilityFile, NodeID: "n_file_1", Verdict: ProjectionPreserved},
			{Capability: CapabilityImage, NodeID: "n_image_1", Verdict: ProjectionPreserved},
			{Capability: CapabilityAudio, NodeID: "n_audio_1", Verdict: ProjectionPreserved},
			{Capability: CapabilityVideo, NodeID: "n_video_1", Verdict: ProjectionPreserved},
			{Capability: CapabilityLiveSession, NodeID: "n_live_session_1", Verdict: ProjectionPreserved},
			{Capability: CapabilityBatch, NodeID: "n_batch_1", Verdict: ProjectionPreserved},
			{Capability: CapabilityMCPServer, NodeID: "n_mcp_server_1", Verdict: ProjectionPreserved},
			{Capability: CapabilityDataRetention, NodeID: "n_data_retention_1", Verdict: ProjectionPreserved},
		},
		OverallVerdict: ProjectionLossy,
	}
	// Policy 必须与 fixture 中 data_retention node 的 D12 词汇兼容
	env.Policy.DataRetention = DataRetentionNode{
		Value:       DataRetentionRegionalAsserted,
		Enforcement: "asserted",
		Region:      "us-east-1",
		AuditLabel:  "regional_us_east_1",
	}
	env.Policy.Audit = AuditPolicy{Visibility: AuditVisible, Label: "fixture_full"}
	return env
}

// TestINV1_FullCapabilityRoundTrip 验证 15 capability 全集 round-trip 字节稳定 + DeepEqual。
//
// 每个子 case 独立挑选一个 CapabilityKind 作为"主角"（原 fixture + node ID hint），
// 但 fixture 本身始终包含 15 nodes 全集；这样既验证全集 round-trip 又便于调试 specific kind。
func TestINV1_FullCapabilityRoundTrip(t *testing.T) {
	cases := []struct {
		kind CapabilityKind
	}{
		{CapabilityText},
		{CapabilityToolUse},
		{CapabilityToolResult},
		{CapabilityThinking},
		{CapabilityCacheControl},
		{CapabilityStructuredOutput},
		{CapabilityComputerUse},
		{CapabilityFile},
		{CapabilityImage},
		{CapabilityAudio},
		{CapabilityVideo},
		{CapabilityLiveSession},
		{CapabilityBatch},
		{CapabilityMCPServer},
		{CapabilityDataRetention},
	}

	for _, tc := range cases {
		t.Run(string(tc.kind), func(t *testing.T) {
			src := makeFullCapabilityFixture()
			src.RequestMeta.RequestID = "req_full_" + string(tc.kind)
			roundTripBytesStable(t, src)
		})
	}
}

// TestINV1_BufferedResponseRoundTrip 验证 BufferedResponse envelope round-trip。
//
// 形态：buffered（StreamPlan.Mode=buffered）+ BufferedResponse 非 nil + StreamEvents 必须 nil/空。
func TestINV1_BufferedResponseRoundTrip(t *testing.T) {
	src := minimalValidEnvelope()
	src.StreamPlan.Mode = StreamModeBuffered
	src.BufferedResponse = &CanonicalResponse{
		ID:    "resp_001",
		Model: "gpt-4o-mini",
		Content: []CanonicalContentBlock{
			{Type: "text", Text: "buffered response body"},
		},
		Usage: CanonicalUsage{
			InputTokens:              42,
			OutputTokens:              17,
			TotalTokens:               59,
			CacheCreationInputTokens: 8,
			CacheReadInputTokens:     0,
		},
		StopReason: CanonicalStopEndTurn,
	}
	// 单 text node 让 fixture 覆盖 messages → response 链路
	src.CapabilityGraph.Nodes = []CapabilityNode{{
		ID: "n_text_1", Kind: CapabilityText, StreamReady: StreamReadyYes,
		Text: &TextNode{Role: "assistant", Block: CanonicalContentBlock{Type: "text", Text: "buffered response body"}},
	}}

	roundTripBytesStable(t, src)
}

// TestINV1_StreamEventsRoundTrip 验证 StreamEvents replay envelope round-trip。
//
// 形态：replay（StreamPlan.Mode=replay）+ StreamEvents 列出全量事件 + BufferedResponse 必须 nil。
func TestINV1_StreamEventsRoundTrip(t *testing.T) {
	src := minimalValidEnvelope()
	src.StreamPlan.Mode = StreamModeReplay
	src.StreamPlan.EventClasses = []string{
		"message_start", "content_block_start", "content_block_delta",
		"content_block_stop", "message_delta", "message_stop",
	}
	src.StreamEvents = []CanonicalEvent{
		{Type: "message_start", MessageID: "msg_001", Model: "gpt-4o-mini"},
		{
			Type:         "content_block_start",
			Index:        0,
			ContentBlock: &CanonicalContentBlock{Type: "text"},
		},
		{
			Type:  "content_block_delta",
			Index: 0,
			Delta: &CanonicalContentDelta{Type: "text_delta", Text: "Hello"},
		},
		{
			Type:  "content_block_delta",
			Index: 0,
			Delta: &CanonicalContentDelta{Type: "text_delta", Text: " world"},
		},
		{Type: "content_block_stop", Index: 0},
		{
			Type:  "message_delta",
			Usage: &CanonicalUsage{InputTokens: 10, OutputTokens: 2, TotalTokens: 12},
		},
		{Type: "message_stop", StopReason: CanonicalStopEndTurn},
	}

	roundTripBytesStable(t, src)
}

// TestINV1_ExtensionsRoundTrip 验证 Extensions（vendor: + experimental: 双 key + 嵌套 JSON）round-trip。
//
// 要求 key 前缀必须是 vendor: 或 experimental；本测试覆盖三种典型 payload 形态：
//
//   - 嵌套 object（vendor: prefix）
//   - 嵌套数组（experimental: prefix）
//   - 标量（vendor: prefix）
func TestINV1_ExtensionsRoundTrip(t *testing.T) {
	src := minimalValidEnvelope()
	src.Extensions = map[string]json.RawMessage{
		"vendor:openai": json.RawMessage(
			`{"system_fingerprint":"fp_abc","service_tier":"priority","cache_creation_input_tokens":42}`,
		),
		"experimental:quota": json.RawMessage(
			`[{"window":"1m","limit":1000},{"window":"1h","limit":50000}]`,
		),
		"vendor:anthropic":     json.RawMessage(`"prompt-caching-2024-07-31"`),
		"experimental:routing": json.RawMessage(`{"strategy":"locality+headroom","weights":{"locality":0.6,"headroom":0.4}}`),
	}
	// 加一个 text node 让 capability_graph 非空，路径更接近真实 envelope
	src.CapabilityGraph.Nodes = []CapabilityNode{{
		ID: "n_text_1", Kind: CapabilityText, StreamReady: StreamReadyYes,
		Text: &TextNode{Role: "user", Block: CanonicalContentBlock{Type: "text", Text: "with extensions"}},
	}}

	roundTripBytesStable(t, src)
}

// TestINV2_NilSliceCompat 验证 nil/empty slice 序列化兼容（带 omitempty 字段省略）。
func TestINV2_NilSliceCompat(t *testing.T) {
	src := minimalValidEnvelope()
	src.CapabilityGraph.Nodes = nil      // null vs []
	src.CapabilityGraph.Edges = nil
	src.Messages = nil
	src.ProviderProjection.CapabilityResults = nil
	src.StreamPlan.EventClasses = nil

	raw, err := json.Marshal(src)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !json.Valid(raw) {
		t.Fatalf("invalid JSON: %s", raw)
	}
	// 不强制 nil slice 在 wire 上等于 [] —— 但 Unmarshal 后 round-trip 应稳定。
	var round HCSFEnvelope
	if err := json.Unmarshal(raw, &round); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	raw2, err := json.Marshal(round)
	if err != nil {
		t.Fatalf("marshal2: %v", err)
	}
	if string(raw) != string(raw2) {
		t.Fatalf("INV-2 second-round JSON not stable:\n1=%s\n2=%s", raw, raw2)
	}
}

// --------------------------------------------------------------------------
// capability payload enum 守门
// --------------------------------------------------------------------------

// TestINV14_StreamReadyEnum 验证每个 node 的 StreamReady 必须在 {yes,no,partial} 内。
func TestINV14_StreamReadyEnum(t *testing.T) {
	cases := []struct {
		name      string
		value     StreamReadiness
		expectInv string
	}{
		{"empty string", "", "INV-14"},
		{"bogus", "almost", "INV-14"},
		{"yes accepted", StreamReadyYes, ""},
		{"no accepted", StreamReadyNo, ""},
		{"partial accepted", StreamReadyPartial, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := minimalValidEnvelope()
			env.CapabilityGraph.Nodes = []CapabilityNode{{
				ID:          "n1",
				Kind:        CapabilityText,
				StreamReady: tc.value,
				Text:        &TextNode{Role: "user", Block: CanonicalContentBlock{Type: "text", Text: "hi"}},
			}}
			err := ValidateEnvelope(env)
			if tc.expectInv == "" {
				if err != nil {
					t.Fatalf("expected pass, got: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.expectInv) {
				t.Fatalf("expected %s violation, got: %v", tc.expectInv, err)
			}
		})
	}
}

// TestINV16_ToolUseStatusEnum 验证 ToolUse.Status enum 守门（D1 部分）。
func TestINV16_ToolUseStatusEnum(t *testing.T) {
	cases := []struct {
		name      string
		value     ToolNodeStatus
		expectInv string
	}{
		{"empty string", "", "INV-16"},
		{"bogus", "in_flight", "INV-16"},
		{"pending accepted", ToolNodePending, ""},
		{"partial accepted", ToolNodePartial, ""},
		{"complete accepted", ToolNodeComplete, ""},
		{"error accepted", ToolNodeError, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := minimalValidEnvelope()
			env.CapabilityGraph.Nodes = []CapabilityNode{{
				ID:          "n1",
				Kind:        CapabilityToolUse,
				StreamReady: StreamReadyYes,
				ToolUse: &ToolUseNode{
					ToolCallID: "t1",
					Name:       "calc",
					Input:      json.RawMessage(`{}`),
					Status:     tc.value,
				},
			}}
			err := ValidateEnvelope(env)
			if tc.expectInv == "" {
				if err != nil {
					t.Fatalf("expected pass, got: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.expectInv) {
				t.Fatalf("expected %s violation, got: %v", tc.expectInv, err)
			}
		})
	}
}

// TestINV18_ToolResultStatusEnum 验证 ToolResult.Status 限定 {complete, error}（D1 部分）。
//
// 与 ToolUse 不同：pending/partial 在 ToolResult 上语义不成立 — 结果必须落地。
func TestINV18_ToolResultStatusEnum(t *testing.T) {
	cases := []struct {
		name      string
		value     ToolNodeStatus
		expectInv string
	}{
		{"empty string", "", "INV-18"},
		{"pending rejected", ToolNodePending, "INV-18"},
		{"partial rejected", ToolNodePartial, "INV-18"},
		{"bogus", "queued", "INV-18"},
		{"complete accepted", ToolNodeComplete, ""},
		{"error accepted", ToolNodeError, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := minimalValidEnvelope()
			env.CapabilityGraph.Nodes = []CapabilityNode{
				{
					ID: "n_tu", Kind: CapabilityToolUse, StreamReady: StreamReadyYes,
					ToolUse: &ToolUseNode{ToolCallID: "t1", Name: "calc", Input: json.RawMessage(`{}`), Status: ToolNodeComplete},
				},
				{
					ID: "n_tr", Kind: CapabilityToolResult, StreamReady: StreamReadyNo,
					ToolResult: &ToolResultNode{
						ToolCallID: "t1",
						Content:    []CanonicalContentBlock{},
						Status:     tc.value,
						IsError:    tc.value == ToolNodeError,
					},
				},
			}
			env.CapabilityGraph.Edges = []CapabilityEdge{
				{ID: "e_req", Type: EdgeRequires, From: "n_tr", To: "n_tu", Required: true},
			}
			err := ValidateEnvelope(env)
			if tc.expectInv == "" {
				if err != nil {
					t.Fatalf("expected pass, got: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.expectInv) {
				t.Fatalf("expected %s violation, got: %v", tc.expectInv, err)
			}
		})
	}
}

// TestINV23_AudioTransportEnum 验证 Audio.Transport enum 守门（D1 仅守 transport，
// transport↔Locator.Kind 映射延后 D2）。
func TestINV23_AudioTransportEnum(t *testing.T) {
	cases := []struct {
		name      string
		value     MediaTransport
		expectInv string
	}{
		{"empty string", "", "INV-23"},
		{"bogus", "rtmp", "INV-23"},
		{"inline accepted", MediaTransportInline, ""},
		{"file accepted", MediaTransportFile, ""},
		{"url accepted", MediaTransportURL, ""},
		{"stream accepted", MediaTransportStream, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := minimalValidEnvelope()
			env.CapabilityGraph.Nodes = []CapabilityNode{{
				ID:          "n1",
				Kind:        CapabilityAudio,
				StreamReady: StreamReadyPartial,
				Audio: &AudioNode{
					Transport: tc.value,
					Format:    "pcm16",
					Locator:   DataLocator{Kind: DataSourceInlineBase64, Value: "BASE64==="},
				},
			}}
			err := ValidateEnvelope(env)
			if tc.expectInv == "" {
				if err != nil {
					t.Fatalf("expected pass, got: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.expectInv) {
				t.Fatalf("expected %s violation, got: %v", tc.expectInv, err)
			}
		})
	}
}

// TestINV39_ThinkingRedactionEnum 验证 Thinking.Redaction enum 守门。
func TestINV39_ThinkingRedactionEnum(t *testing.T) {
	cases := []struct {
		name      string
		value     RedactionClass
		expectInv string
	}{
		{"empty string", "", "INV-39"},
		{"bogus", "secret", "INV-39"},
		{"public accepted", RedactionPublic, ""},
		{"redacted accepted", RedactionRedacted, ""},
		{"hidden accepted", RedactionHidden, ""},
		{"provider_only accepted", RedactionProviderOnly, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := minimalValidEnvelope()
			env.CapabilityGraph.Nodes = []CapabilityNode{{
				ID:          "n1",
				Kind:        CapabilityThinking,
				StreamReady: StreamReadyPartial,
				Thinking: &ThinkingNode{
					BudgetTokens: 1024,
					Blocks:       []CanonicalContentBlock{},
					Redaction:    tc.value,
				},
			}}
			err := ValidateEnvelope(env)
			if tc.expectInv == "" {
				if err != nil {
					t.Fatalf("expected pass, got: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.expectInv) {
				t.Fatalf("expected %s violation, got: %v", tc.expectInv, err)
			}
		})
	}
}

// --------------------------------------------------------------------------
// 复合 payload validator（收口/17/18 收口/25/27/29/34/36/37 部分/39 收口）
// --------------------------------------------------------------------------

// TestINV15_TextNodePayload 验证 TextNode.Role + Block.Type。
func TestINV15_TextNodePayload(t *testing.T) {
	cases := []struct {
		name      string
		role      string
		blockType string
		expectInv string
	}{
		{"empty role", "", "text", "INV-15"},
		{"bogus role", "moderator", "text", "INV-15"},
		{"block type empty", "user", "", "INV-15"},
		{"block type wrong", "user", "image", "INV-15"},
		{"user accepted", "user", "text", ""},
		{"assistant accepted", "assistant", "text", ""},
		{"system accepted", "system", "text", ""},
		{"tool accepted", "tool", "text", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := minimalValidEnvelope()
			env.CapabilityGraph.Nodes = []CapabilityNode{{
				ID:          "n1",
				Kind:        CapabilityText,
				StreamReady: StreamReadyYes,
				Text:        &TextNode{Role: tc.role, Block: CanonicalContentBlock{Type: tc.blockType, Text: "hi"}},
			}}
			err := ValidateEnvelope(env)
			if tc.expectInv == "" {
				if err != nil {
					t.Fatalf("expected pass, got: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.expectInv) {
				t.Fatalf("expected %s violation, got: %v", tc.expectInv, err)
			}
		})
	}
}

// TestINV16_ToolUseRequiredFields 验证 ToolUse 必填 + Input JSON 形态守门。
func TestINV16_ToolUseRequiredFields(t *testing.T) {
	cases := []struct {
		name       string
		toolCallID string
		toolName   string
		input      json.RawMessage
		expectInv  string
	}{
		{"missing tool_call_id", "", "calc", json.RawMessage(`{}`), "INV-16"},
		{"missing name", "t1", "", json.RawMessage(`{}`), "INV-16"},
		{"input empty bytes", "t1", "calc", json.RawMessage{}, "INV-16"},
		{"input invalid JSON", "t1", "calc", json.RawMessage(`{garbage`), "INV-16"},
		{"input is array", "t1", "calc", json.RawMessage(`[1,2]`), "INV-16"},
		{"input is string", "t1", "calc", json.RawMessage(`"hi"`), "INV-16"},
		{"input is number", "t1", "calc", json.RawMessage(`42`), "INV-16"},
		{"input object accepted", "t1", "calc", json.RawMessage(`{"x":1}`), ""},
		{"input null accepted", "t1", "calc", json.RawMessage(`null`), ""},
		{"input empty object accepted", "t1", "calc", json.RawMessage(`{}`), ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := minimalValidEnvelope()
			env.CapabilityGraph.Nodes = []CapabilityNode{{
				ID:          "n1",
				Kind:        CapabilityToolUse,
				StreamReady: StreamReadyYes,
				ToolUse: &ToolUseNode{
					ToolCallID: tc.toolCallID,
					Name:       tc.toolName,
					Input:      tc.input,
					Status:     ToolNodeComplete,
				},
			}}
			err := ValidateEnvelope(env)
			if tc.expectInv == "" {
				if err != nil {
					t.Fatalf("expected pass, got: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.expectInv) {
				t.Fatalf("expected %s violation, got: %v", tc.expectInv, err)
			}
		})
	}
}

// TestINV17_ToolUsePartialInputState 验证 PartialInput 仅 status=pending/partial 允许。
func TestINV17_ToolUsePartialInputState(t *testing.T) {
	cases := []struct {
		name      string
		status    ToolNodeStatus
		partial   json.RawMessage
		expectInv string
	}{
		{"partial input on complete", ToolNodeComplete, json.RawMessage(`{"x":1}`), "INV-17"},
		{"partial input on error", ToolNodeError, json.RawMessage(`{"x":1}`), "INV-17"},
		{"partial input on pending", ToolNodePending, json.RawMessage(`{"x":1}`), ""},
		{"partial input on partial", ToolNodePartial, json.RawMessage(`{"x":1}`), ""},
		{"null partial on complete", ToolNodeComplete, json.RawMessage(`null`), ""},
		{"empty partial on complete", ToolNodeComplete, nil, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := minimalValidEnvelope()
			env.CapabilityGraph.Nodes = []CapabilityNode{{
				ID:          "n1",
				Kind:        CapabilityToolUse,
				StreamReady: StreamReadyYes,
				ToolUse: &ToolUseNode{
					ToolCallID:   "t1",
					Name:         "calc",
					Input:        json.RawMessage(`{}`),
					PartialInput: tc.partial,
					Status:       tc.status,
				},
			}}
			err := ValidateEnvelope(env)
			if tc.expectInv == "" {
				if err != nil {
					t.Fatalf("expected pass, got: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.expectInv) {
				t.Fatalf("expected %s violation, got: %v", tc.expectInv, err)
			}
		})
	}
}

// TestINV18_ToolResultRequiredAndIsError 验证 ToolResult 必填 + IsError↔Status 一致。
func TestINV18_ToolResultRequiredAndIsError(t *testing.T) {
	cases := []struct {
		name       string
		toolCallID string
		content    []CanonicalContentBlock
		status     ToolNodeStatus
		isError    bool
		expectInv  string
	}{
		{"missing tool_call_id", "", []CanonicalContentBlock{}, ToolNodeComplete, false, "INV-18"},
		{"nil content", "t1", nil, ToolNodeComplete, false, "INV-18"},
		{"error status without is_error", "t1", []CanonicalContentBlock{}, ToolNodeError, false, "INV-18"},
		{"complete status with is_error", "t1", []CanonicalContentBlock{}, ToolNodeComplete, true, "INV-18"},
		{"complete + false accepted", "t1", []CanonicalContentBlock{}, ToolNodeComplete, false, ""},
		{"error + true accepted", "t1", []CanonicalContentBlock{}, ToolNodeError, true, ""},
		{"empty content accepted", "t1", []CanonicalContentBlock{}, ToolNodeComplete, false, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := minimalValidEnvelope()
			// 配合 D3 ToolResult 节点必须有同 envelope 内的 ToolUse + requires edge。
			// 仅当 tc.toolCallID 非空时建立配对，否则 ToolResult 单独存在以触发校验错误 必填守门。
			env.CapabilityGraph.Nodes = []CapabilityNode{}
			if tc.toolCallID != "" {
				env.CapabilityGraph.Nodes = append(env.CapabilityGraph.Nodes, CapabilityNode{
					ID: "n_tu", Kind: CapabilityToolUse, StreamReady: StreamReadyYes,
					ToolUse: &ToolUseNode{ToolCallID: tc.toolCallID, Name: "calc", Input: json.RawMessage(`{}`), Status: ToolNodeComplete},
				})
				env.CapabilityGraph.Edges = []CapabilityEdge{
					{ID: "e_req", Type: EdgeRequires, From: "n_tr", To: "n_tu", Required: true},
				}
			}
			env.CapabilityGraph.Nodes = append(env.CapabilityGraph.Nodes, CapabilityNode{
				ID:          "n_tr",
				Kind:        CapabilityToolResult,
				StreamReady: StreamReadyNo,
				ToolResult: &ToolResultNode{
					ToolCallID: tc.toolCallID,
					Content:    tc.content,
					Status:     tc.status,
					IsError:    tc.isError,
				},
			})
			err := ValidateEnvelope(env)
			if tc.expectInv == "" {
				if err != nil {
					t.Fatalf("expected pass, got: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.expectInv) {
				t.Fatalf("expected %s violation, got: %v", tc.expectInv, err)
			}
		})
	}
}

// TestINV25_CachePayload 验证 Cache.Scope enum + LocalityHint 白名单。
//
// Scope=block/message 要求 BreakpointRefs 非空 + 可解析；
// 本测试用 helper 自动补 target 节点。
func TestINV25_CachePayload(t *testing.T) {
	cases := []struct {
		name      string
		scope     CacheScope
		locality  string
		expectInv string
	}{
		{"empty scope", "", "", "INV-25"},
		{"bogus scope", "global_segment", "", "INV-25"},
		{"bogus locality hint", CacheScopeRequest, "regional", "INV-25"},
		{"request + empty locality accepted", CacheScopeRequest, "", ""},
		{"block + account_pin accepted", CacheScopeBlock, "account_pin", ""},
		{"message + account_pin accepted", CacheScopeMessage, "account_pin", ""},
		{"session + account_recent accepted", CacheScopeSession, "account_recent", ""},
		{"vendor + global accepted", CacheScopeVendor, "global", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := minimalValidEnvelope()
			refs := []string{}
			needsTarget := tc.scope == CacheScopeBlock || tc.scope == CacheScopeMessage
			if needsTarget {
				refs = []string{"n_target"}
			}
			env.CapabilityGraph.Nodes = []CapabilityNode{{
				ID:          "n1",
				Kind:        CapabilityCacheControl,
				StreamReady: StreamReadyYes,
				CacheControl: &CacheControlNode{
					Scope:          tc.scope,
					BreakpointRefs: refs,
					LocalityHint:   tc.locality,
				},
			}}
			if needsTarget {
				env.CapabilityGraph.Nodes = append(env.CapabilityGraph.Nodes, CapabilityNode{
					ID: "n_target", Kind: CapabilityText, StreamReady: StreamReadyYes,
					Text: &TextNode{Role: "user", Block: CanonicalContentBlock{Type: "text", Text: "x"}},
				})
			}
			err := ValidateEnvelope(env)
			if tc.expectInv == "" {
				if err != nil {
					t.Fatalf("expected pass, got: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.expectInv) {
				t.Fatalf("expected %s violation, got: %v", tc.expectInv, err)
			}
		})
	}
}

// TestINV27_BatchPayload 验证 BatchNode 必填 + Validation enum。
func TestINV27_BatchPayload(t *testing.T) {
	cases := []struct {
		name       string
		jobID      string
		endpoint   string
		inputRef   string
		validation BatchStatus
		expectInv  string
	}{
		{"missing job_id", "", "/v1/batch", "n_file", BatchPending, "INV-27"},
		{"missing endpoint", "job_1", "", "n_file", BatchPending, "INV-27"},
		{"missing input_ref", "job_1", "/v1/batch", "", BatchPending, "INV-27"},
		{"bogus validation", "job_1", "/v1/batch", "n_file", "queued", "INV-27"},
		{"empty validation", "job_1", "/v1/batch", "n_file", "", "INV-27"},
		{"pending accepted", "job_1", "/v1/batch", "n_file", BatchPending, ""},
		{"validated accepted", "job_1", "/v1/batch", "n_file", BatchValidated, ""},
		{"failed accepted", "job_1", "/v1/batch", "n_file", BatchFailed, ""},
		{"complete accepted", "job_1", "/v1/batch", "n_file", BatchComplete, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := minimalValidEnvelope()
			// 配合 D5 BatchNode.InputRef 必须解析到 FileNode。
			env.CapabilityGraph.Nodes = []CapabilityNode{
				{
					ID: "n_file", Kind: CapabilityFile, StreamReady: StreamReadyNo,
					File: &FileNode{
						SourceKind: DataSourceFileID,
						MediaType:  "application/jsonl",
						Locator:    DataLocator{Kind: DataSourceFileID, Value: "openai_file_xyz"},
					},
				},
				{
					ID: "n_batch", Kind: CapabilityBatch, StreamReady: StreamReadyNo,
					Batch: &BatchNode{
						JobID:      tc.jobID,
						Endpoint:   tc.endpoint,
						InputRef:   tc.inputRef,
						Validation: tc.validation,
					},
				},
			}
			err := ValidateEnvelope(env)
			if tc.expectInv == "" {
				if err != nil {
					t.Fatalf("expected pass, got: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.expectInv) {
				t.Fatalf("expected %s violation, got: %v", tc.expectInv, err)
			}
		})
	}
}

// TestINV29_RetryPolicy 验证 RetryPolicy 数值 + Backoff 白名单。
func TestINV29_RetryPolicy(t *testing.T) {
	cases := []struct {
		name      string
		policy    *RetryPolicy
		expectInv string
	}{
		{"nil policy accepted", nil, ""},
		{"negative max_attempts", &RetryPolicy{MaxAttempts: -1, Backoff: "fixed"}, "INV-29"},
		{"bogus backoff", &RetryPolicy{MaxAttempts: 3, Backoff: "polynomial"}, "INV-29"},
		{"fixed accepted", &RetryPolicy{MaxAttempts: 3, Backoff: "fixed"}, ""},
		{"exponential accepted", &RetryPolicy{MaxAttempts: 5, Backoff: "exponential"}, ""},
		{"provider_default accepted", &RetryPolicy{MaxAttempts: 0, Backoff: "provider_default"}, ""},
		{"zero attempts empty backoff accepted", &RetryPolicy{MaxAttempts: 0, Backoff: ""}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := minimalValidEnvelope()
			// 配合 D5 BatchNode.InputRef 必须解析到 FileNode。
			env.CapabilityGraph.Nodes = []CapabilityNode{
				{
					ID: "n_file", Kind: CapabilityFile, StreamReady: StreamReadyNo,
					File: &FileNode{
						SourceKind: DataSourceFileID,
						MediaType:  "application/jsonl",
						Locator:    DataLocator{Kind: DataSourceFileID, Value: "openai_file_xyz"},
					},
				},
				{
					ID: "n_batch", Kind: CapabilityBatch, StreamReady: StreamReadyNo,
					Batch: &BatchNode{
						JobID:       "job_1",
						Endpoint:    "/v1/batch",
						InputRef:    "n_file",
						Validation:  BatchPending,
						RetryPolicy: tc.policy,
					},
				},
			}
			err := ValidateEnvelope(env)
			if tc.expectInv == "" {
				if err != nil {
					t.Fatalf("expected pass, got: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.expectInv) {
				t.Fatalf("expected %s violation, got: %v", tc.expectInv, err)
			}
		})
	}
}

// TestINV34_ComputerUsePayload 验证 ComputerUse Environment 白名单 + Action 必填 + Approval enum。
func TestINV34_ComputerUsePayload(t *testing.T) {
	cases := []struct {
		name      string
		env       string
		action    string
		approval  ApprovalState
		expectInv string
	}{
		{"bogus environment", "tablet", "screenshot", ApprovalRequired, "INV-34"},
		{"empty environment", "", "screenshot", ApprovalRequired, "INV-34"},
		{"empty action", "browser", "", ApprovalNotRequired, "INV-34"},
		{"bogus approval", "browser", "screenshot", "auto", "INV-34"},
		{"empty approval", "browser", "screenshot", "", "INV-34"},
		{"browser + granted accepted", "browser", "screenshot", ApprovalGranted, ""},
		{"desktop + denied accepted", "desktop", "click", ApprovalDenied, ""},
		{"mobile + required accepted", "mobile", "tap", ApprovalRequired, ""},
		{"other + not_required accepted", "other", "noop", ApprovalNotRequired, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := minimalValidEnvelope()
			env.CapabilityGraph.Nodes = []CapabilityNode{{
				ID:          "n1",
				Kind:        CapabilityComputerUse,
				StreamReady: StreamReadyNo,
				ComputerUse: &ComputerUseNode{
					Environment: tc.env,
					Action:      tc.action,
					Approval:    tc.approval,
				},
			}}
			err := ValidateEnvelope(env)
			if tc.expectInv == "" {
				if err != nil {
					t.Fatalf("expected pass, got: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.expectInv) {
				t.Fatalf("expected %s violation, got: %v", tc.expectInv, err)
			}
		})
	}
}

// TestINV36_StructuredOutputMode 验证 Mode enum 守门。
func TestINV36_StructuredOutputMode(t *testing.T) {
	cases := []struct {
		name      string
		mode      StructuredOutputMode
		schema    json.RawMessage
		expectInv string
	}{
		{"empty mode", "", json.RawMessage(`null`), "INV-36"},
		{"bogus mode", "json_strict", json.RawMessage(`null`), "INV-36"},
		{"json_mode accepted", StructuredOutputJSONMode, json.RawMessage(`null`), ""},
		{"tool_strategy accepted", StructuredOutputToolStrategy, json.RawMessage(`null`), ""},
		{"provider_native accepted", StructuredOutputProviderNative, json.RawMessage(`null`), ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := minimalValidEnvelope()
			env.CapabilityGraph.Nodes = []CapabilityNode{{
				ID:          "n1",
				Kind:        CapabilityStructuredOutput,
				StreamReady: StreamReadyNo,
				StructuredOutput: &StructuredOutputNode{
					Mode:   tc.mode,
					Schema: tc.schema,
				},
			}}
			err := ValidateEnvelope(env)
			if tc.expectInv == "" {
				if err != nil {
					t.Fatalf("expected pass, got: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.expectInv) {
				t.Fatalf("expected %s violation, got: %v", tc.expectInv, err)
			}
		})
	}
}

// TestINV37_StructuredSchemaJSONObject 验证 mode=json_schema 时 Schema 必须 JSON object。
func TestINV37_StructuredSchemaJSONObject(t *testing.T) {
	cases := []struct {
		name      string
		schema    json.RawMessage
		expectInv string
	}{
		{"null schema rejected", json.RawMessage(`null`), "INV-37"},
		{"array schema rejected", json.RawMessage(`[]`), "INV-37"},
		{"string schema rejected", json.RawMessage(`"obj"`), "INV-37"},
		{"invalid JSON rejected", json.RawMessage(`{garbage`), "INV-37"},
		{"empty bytes rejected", json.RawMessage{}, "INV-37"},
		{"object accepted", json.RawMessage(`{"type":"object"}`), ""},
		{"empty object accepted", json.RawMessage(`{}`), ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := minimalValidEnvelope()
			env.CapabilityGraph.Nodes = []CapabilityNode{{
				ID:          "n1",
				Kind:        CapabilityStructuredOutput,
				StreamReady: StreamReadyNo,
				StructuredOutput: &StructuredOutputNode{
					Mode:   StructuredOutputJSONSchema,
					Schema: tc.schema,
				},
			}}
			err := ValidateEnvelope(env)
			if tc.expectInv == "" {
				if err != nil {
					t.Fatalf("expected pass, got: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.expectInv) {
				t.Fatalf("expected %s violation, got: %v", tc.expectInv, err)
			}
		})
	}
}

// TestINV39_ThinkingNumericNonNeg 验证 Thinking BudgetTokens/HiddenTokens 非负（D2 收口）。
func TestINV39_ThinkingNumericNonNeg(t *testing.T) {
	cases := []struct {
		name      string
		budget    int
		hidden    int
		expectInv string
	}{
		{"negative budget", -1, 0, "INV-39"},
		{"negative hidden", 0, -100, "INV-39"},
		{"zero accepted", 0, 0, ""},
		{"positive accepted", 1024, 512, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := minimalValidEnvelope()
			env.CapabilityGraph.Nodes = []CapabilityNode{{
				ID:          "n1",
				Kind:        CapabilityThinking,
				StreamReady: StreamReadyPartial,
				Thinking: &ThinkingNode{
					BudgetTokens: tc.budget,
					HiddenTokens: tc.hidden,
					Blocks:       []CanonicalContentBlock{},
					Redaction:    RedactionPublic,
				},
			}}
			err := ValidateEnvelope(env)
			if tc.expectInv == "" {
				if err != nil {
					t.Fatalf("expected pass, got: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.expectInv) {
				t.Fatalf("expected %s violation, got: %v", tc.expectInv, err)
			}
		})
	}
}

// --------------------------------------------------------------------------
// 条件必填 + Policy/graph 一致性
// --------------------------------------------------------------------------

// drNode 是构造 data_retention 节点的辅助 helper（测试用）。
func drNode(value DataRetentionLabel, enforce, region, evidence, label string, store *bool) CapabilityNode {
	return CapabilityNode{
		ID:          "n_dr",
		Kind:        CapabilityDataRetention,
		StreamReady: StreamReadyYes,
		DataRetention: &DataRetentionNode{
			Value:        value,
			Enforcement:  enforce,
			Region:       region,
			RequestStore: store,
			EvidenceRef:  evidence,
			AuditLabel:   label,
		},
	}
}

func boolPtr(b bool) *bool { return &b }

// TestINV30_DataRetentionRequestStoreFalse 验证 value=request_store_false 必须显式 RequestStore=false。
func TestINV30_DataRetentionRequestStoreFalse(t *testing.T) {
	cases := []struct {
		name        string
		store       *bool
		expectInv   string
	}{
		{"nil request_store", nil, "INV-30"},
		{"request_store=true", boolPtr(true), "INV-30"},
		{"request_store=false accepted", boolPtr(false), ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := minimalValidEnvelope()
			env.CapabilityGraph.Nodes = []CapabilityNode{drNode(
				DataRetentionRequestStoreFalse, "asserted", "", "", "label_x", tc.store,
			)}
			env.Policy.DataRetention = DataRetentionNode{Value: DataRetentionRequestStoreFalse, Enforcement: "asserted", RequestStore: tc.store, AuditLabel: "label_x"}
			err := ValidateEnvelope(env)
			if tc.expectInv == "" {
				if err != nil {
					t.Fatalf("expected pass, got: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.expectInv) {
				t.Fatalf("expected %s violation, got: %v", tc.expectInv, err)
			}
		})
	}
}

// TestINV31_DataRetentionRegionalAsserted 验证 regional_asserted 必须有 Region。
func TestINV31_DataRetentionRegionalAsserted(t *testing.T) {
	env := minimalValidEnvelope()
	env.CapabilityGraph.Nodes = []CapabilityNode{drNode(
		DataRetentionRegionalAsserted, "asserted", "", "", "label_x", nil,
	)}
	env.Policy.DataRetention = DataRetentionNode{Value: DataRetentionRegionalAsserted, Enforcement: "asserted", AuditLabel: "label_x"}
	err := ValidateEnvelope(env)
	if err == nil || !strings.Contains(err.Error(), "INV-31") {
		t.Fatalf("expected INV-31 missing region, got: %v", err)
	}

	env.CapabilityGraph.Nodes[0].DataRetention.Region = "us-west-2"
	env.Policy.DataRetention.Region = "us-west-2"
	if err := ValidateEnvelope(env); err != nil {
		t.Fatalf("with region set, should validate: %v", err)
	}
}

// TestINV31_DataRetentionZDRVerified 验证 zdr_verified 必须有 EvidenceRef + Enforcement=verified。
func TestINV31_DataRetentionZDRVerified(t *testing.T) {
	env := minimalValidEnvelope()
	env.CapabilityGraph.Nodes = []CapabilityNode{drNode(
		DataRetentionZDRVerified, "asserted", "", "evid_x", "label_x", nil,
	)}
	env.Policy.DataRetention = DataRetentionNode{Value: DataRetentionZDRVerified, Enforcement: "asserted", EvidenceRef: "evid_x", AuditLabel: "label_x"}
	err := ValidateEnvelope(env)
	if err == nil || !strings.Contains(err.Error(), "INV-31") {
		t.Fatalf("expected INV-31 enforcement!=verified, got: %v", err)
	}

	env.CapabilityGraph.Nodes[0].DataRetention.Enforcement = "verified"
	env.Policy.DataRetention.Enforcement = "verified"
	if err := ValidateEnvelope(env); err != nil {
		t.Fatalf("with enforcement=verified, should validate: %v", err)
	}

	env.CapabilityGraph.Nodes[0].DataRetention.EvidenceRef = ""
	env.Policy.DataRetention.EvidenceRef = ""
	err = ValidateEnvelope(env)
	if err == nil || !strings.Contains(err.Error(), "INV-31") {
		t.Fatalf("expected INV-31 missing evidence_ref, got: %v", err)
	}
}

// TestINV32_DataRetentionProviderContractRequired 验证 provider_contract_required 三联约束。
func TestINV32_DataRetentionProviderContractRequired(t *testing.T) {
	env := minimalValidEnvelope()
	env.CapabilityGraph.Nodes = []CapabilityNode{drNode(
		DataRetentionProviderContractRequired, "asserted", "", "evid_x", "label_x", nil,
	)}
	env.Policy.DataRetention = DataRetentionNode{Value: DataRetentionProviderContractRequired, Enforcement: "asserted", EvidenceRef: "evid_x", AuditLabel: "label_x"}
	err := ValidateEnvelope(env)
	if err == nil || !strings.Contains(err.Error(), "INV-32") {
		t.Fatalf("expected INV-32 enforcement mismatch, got: %v", err)
	}

	env.CapabilityGraph.Nodes[0].DataRetention.Enforcement = "contract_required"
	env.Policy.DataRetention.Enforcement = "contract_required"
	if err := ValidateEnvelope(env); err != nil {
		t.Fatalf("with enforcement=contract_required, should validate: %v", err)
	}

	env.CapabilityGraph.Nodes[0].DataRetention.EvidenceRef = ""
	env.Policy.DataRetention.EvidenceRef = ""
	err = ValidateEnvelope(env)
	if err == nil || !strings.Contains(err.Error(), "INV-32") {
		t.Fatalf("expected INV-32 missing evidence_ref, got: %v", err)
	}
}

// TestINV33_DataRetentionPolicyConsistency 验证 graph data_retention node 与 Policy 一致。
func TestINV33_DataRetentionPolicyConsistency(t *testing.T) {
	t.Run("value mismatch", func(t *testing.T) {
		env := minimalValidEnvelope()
		env.CapabilityGraph.Nodes = []CapabilityNode{drNode(
			DataRetentionRequestStoreFalse, "asserted", "", "", "label_x", boolPtr(false),
		)}
		env.Policy.DataRetention = DataRetentionNode{Value: DataRetentionUnknown, Enforcement: "unknown", AuditLabel: "x"}
		err := ValidateEnvelope(env)
		if err == nil || !strings.Contains(err.Error(), "INV-33") {
			t.Fatalf("expected INV-33 value mismatch, got: %v", err)
		}
	})
	t.Run("enforcement mismatch", func(t *testing.T) {
		env := minimalValidEnvelope()
		env.CapabilityGraph.Nodes = []CapabilityNode{drNode(
			DataRetentionRequestStoreFalse, "verified", "", "", "label_x", boolPtr(false),
		)}
		env.Policy.DataRetention = DataRetentionNode{Value: DataRetentionRequestStoreFalse, Enforcement: "asserted", RequestStore: boolPtr(false), AuditLabel: "label_x"}
		err := ValidateEnvelope(env)
		if err == nil || !strings.Contains(err.Error(), "INV-33") {
			t.Fatalf("expected INV-33 enforcement mismatch, got: %v", err)
		}
	})
	t.Run("two data_retention nodes rejected", func(t *testing.T) {
		env := minimalValidEnvelope()
		env.CapabilityGraph.Nodes = []CapabilityNode{
			drNode(DataRetentionRequestStoreFalse, "asserted", "", "", "label_x", boolPtr(false)),
			func() CapabilityNode {
				n := drNode(DataRetentionUnknown, "unknown", "", "", "label_y", nil)
				n.ID = "n_dr_2"
				return n
			}(),
		}
		env.Policy.DataRetention = DataRetentionNode{Value: DataRetentionRequestStoreFalse, Enforcement: "asserted", RequestStore: boolPtr(false), AuditLabel: "label_x"}
		err := ValidateEnvelope(env)
		if err == nil || !strings.Contains(err.Error(), "INV-33") {
			t.Fatalf("expected INV-33 multi-node rejection, got: %v", err)
		}
	})
}

// TestINV40_ThinkingRedactionVisibleBlocks 验证 hidden/provider_only 不得携带可见 text。
func TestINV40_ThinkingRedactionVisibleBlocks(t *testing.T) {
	cases := []struct {
		name      string
		redaction RedactionClass
		blocks    []CanonicalContentBlock
		expectInv string
	}{
		{"hidden with visible text", RedactionHidden, []CanonicalContentBlock{{Type: "text", Text: "secret"}}, "INV-40"},
		{"provider_only with visible text", RedactionProviderOnly, []CanonicalContentBlock{{Type: "text", Text: "x"}}, "INV-40"},
		{"hidden with empty blocks accepted", RedactionHidden, []CanonicalContentBlock{}, ""},
		{"hidden with empty text accepted", RedactionHidden, []CanonicalContentBlock{{Type: "text", Text: ""}}, ""},
		{"public with visible text accepted", RedactionPublic, []CanonicalContentBlock{{Type: "text", Text: "ok"}}, ""},
		{"redacted with visible text accepted", RedactionRedacted, []CanonicalContentBlock{{Type: "text", Text: "redacted"}}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := minimalValidEnvelope()
			env.CapabilityGraph.Nodes = []CapabilityNode{{
				ID:          "n_t",
				Kind:        CapabilityThinking,
				StreamReady: StreamReadyPartial,
				Thinking:    &ThinkingNode{BudgetTokens: 0, Blocks: tc.blocks, Redaction: tc.redaction},
			}}
			err := ValidateEnvelope(env)
			if tc.expectInv == "" {
				if err != nil {
					t.Fatalf("expected pass, got: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.expectInv) {
				t.Fatalf("expected %s violation, got: %v", tc.expectInv, err)
			}
		})
	}
}

// TestINV45_ProtocolLossEntryFields 验证 ProtocolLossEntry 4 处的 Severity/NodeID/Capability 守门。
func TestINV45_ProtocolLossEntryFields(t *testing.T) {
	t.Run("invalid severity", func(t *testing.T) {
		env := minimalValidEnvelope()
		env.CapabilityGraph.ProtocolLoss = []ProtocolLossEntry{{Severity: "critical", Reason: "x"}}
		err := ValidateEnvelope(env)
		if err == nil || !strings.Contains(err.Error(), "INV-45") {
			t.Fatalf("expected INV-45, got: %v", err)
		}
	})
	t.Run("unresolved node_id", func(t *testing.T) {
		env := minimalValidEnvelope()
		env.CapabilityGraph.ProtocolLoss = []ProtocolLossEntry{{NodeID: "n_missing", Reason: "x"}}
		err := ValidateEnvelope(env)
		if err == nil || !strings.Contains(err.Error(), "INV-45") {
			t.Fatalf("expected INV-45 unresolved node, got: %v", err)
		}
	})
	t.Run("invalid capability", func(t *testing.T) {
		env := minimalValidEnvelope()
		env.CapabilityGraph.ProtocolLoss = []ProtocolLossEntry{{Capability: "made_up_kind", Reason: "x"}}
		err := ValidateEnvelope(env)
		if err == nil || !strings.Contains(err.Error(), "INV-45") {
			t.Fatalf("expected INV-45 capability, got: %v", err)
		}
	})
	t.Run("valid entries accepted", func(t *testing.T) {
		env := minimalValidEnvelope()
		env.CapabilityGraph.Nodes = []CapabilityNode{{
			ID: "n_t", Kind: CapabilityText, StreamReady: StreamReadyYes,
			Text: &TextNode{Role: "user", Block: CanonicalContentBlock{Type: "text", Text: "x"}},
		}}
		env.CapabilityGraph.ProtocolLoss = []ProtocolLossEntry{
			{Severity: ProtocolLossWarning, NodeID: "n_t", Capability: CapabilityText, Reason: "ok"},
		}
		if err := ValidateEnvelope(env); err != nil {
			t.Fatalf("expected pass, got: %v", err)
		}
	})
}

// --------------------------------------------------------------------------
// 跨 node/projection 引用完整性（modalities/42/43/46）
// --------------------------------------------------------------------------

// TestINV26_CacheBreakpointRefs 验证 Cache.BreakpointRefs 解析 + Scope 条件。
func TestINV26_CacheBreakpointRefs(t *testing.T) {
	target := CapabilityNode{
		ID: "n_target", Kind: CapabilityText, StreamReady: StreamReadyYes,
		Text: &TextNode{Role: "user", Block: CanonicalContentBlock{Type: "text", Text: "x"}},
	}
	cases := []struct {
		name      string
		scope     CacheScope
		refs      []string
		expectInv string
	}{
		{"block scope empty refs rejected", CacheScopeBlock, []string{}, "INV-26"},
		{"message scope empty refs rejected", CacheScopeMessage, []string{}, "INV-26"},
		{"empty string ref rejected", CacheScopeBlock, []string{""}, "INV-26"},
		{"unresolved ref rejected", CacheScopeBlock, []string{"n_missing"}, "INV-26"},
		{"request scope empty refs accepted", CacheScopeRequest, []string{}, ""},
		{"session scope empty refs accepted", CacheScopeSession, []string{}, ""},
		{"resolved ref accepted", CacheScopeBlock, []string{"n_target"}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := minimalValidEnvelope()
			env.CapabilityGraph.Nodes = []CapabilityNode{
				{
					ID:          "n_cache",
					Kind:        CapabilityCacheControl,
					StreamReady: StreamReadyYes,
					CacheControl: &CacheControlNode{
						Scope:          tc.scope,
						BreakpointRefs: tc.refs,
					},
				},
				target,
			}
			err := ValidateEnvelope(env)
			if tc.expectInv == "" {
				if err != nil {
					t.Fatalf("expected pass, got: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.expectInv) {
				t.Fatalf("expected %s violation, got: %v", tc.expectInv, err)
			}
		})
	}
}

// TestINV35_ComputerScreenshotRef 验证 ScreenshotRef 解析到 image/file。
func TestINV35_ComputerScreenshotRef(t *testing.T) {
	textTarget := CapabilityNode{
		ID: "n_text", Kind: CapabilityText, StreamReady: StreamReadyYes,
		Text: &TextNode{Role: "user", Block: CanonicalContentBlock{Type: "text", Text: "x"}},
	}
	imageTarget := CapabilityNode{
		ID: "n_image", Kind: CapabilityImage, StreamReady: StreamReadyNo,
		Image: &ImageNode{
			SourceKind: DataSourceURL,
			MediaType:  "image/png",
			Locator:    DataLocator{Kind: DataSourceURL, Value: "https://x/y.png"},
		},
	}
	fileTarget := CapabilityNode{
		ID: "n_file", Kind: CapabilityFile, StreamReady: StreamReadyNo,
		File: &FileNode{
			SourceKind: DataSourceFileID,
			MediaType:  "application/pdf",
			Locator:    DataLocator{Kind: DataSourceFileID, Value: "file_x"},
		},
	}
	cases := []struct {
		name         string
		screenshotID string
		extra        []CapabilityNode
		expectInv    string
	}{
		{"unresolved ref", "n_missing", []CapabilityNode{textTarget}, "INV-35"},
		{"ref to text node", "n_text", []CapabilityNode{textTarget}, "INV-35"},
		{"ref to image accepted", "n_image", []CapabilityNode{imageTarget}, ""},
		{"ref to file accepted", "n_file", []CapabilityNode{fileTarget}, ""},
		{"empty ref accepted", "", nil, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := minimalValidEnvelope()
			env.CapabilityGraph.Nodes = append([]CapabilityNode{{
				ID:          "n_cu",
				Kind:        CapabilityComputerUse,
				StreamReady: StreamReadyNo,
				ComputerUse: &ComputerUseNode{
					Environment:   "browser",
					Action:        "screenshot",
					Approval:      ApprovalGranted,
					ScreenshotRef: tc.screenshotID,
				},
			}}, tc.extra...)
			err := ValidateEnvelope(env)
			if tc.expectInv == "" {
				if err != nil {
					t.Fatalf("expected pass, got: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.expectInv) {
				t.Fatalf("expected %s violation, got: %v", tc.expectInv, err)
			}
		})
	}
}

// TestINV41_LiveSessionModalities 验证 modalities ⊂ {text,audio,video}。
func TestINV41_LiveSessionModalities(t *testing.T) {
	cases := []struct {
		name       string
		modalities []string
		expectInv  string
	}{
		{"bogus modality", []string{"haptic"}, "INV-41"},
		{"empty string modality", []string{"text", ""}, "INV-41"},
		{"text accepted", []string{"text"}, ""},
		{"text+audio accepted", []string{"text", "audio"}, ""},
		{"text+audio+video accepted", []string{"text", "audio", "video"}, ""},
		{"empty slice accepted", []string{}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := minimalValidEnvelope()
			env.CapabilityGraph.Nodes = []CapabilityNode{{
				ID:          "n_live",
				Kind:        CapabilityLiveSession,
				StreamReady: StreamReadyYes,
				LiveSession: &LiveSessionNode{
					SessionID:  "sess_001",
					Transport:  LiveTransportWSS,
					Modalities: tc.modalities,
				},
			}}
			err := ValidateEnvelope(env)
			if tc.expectInv == "" {
				if err != nil {
					t.Fatalf("expected pass, got: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.expectInv) {
				t.Fatalf("expected %s violation, got: %v", tc.expectInv, err)
			}
		})
	}
}

// TestINV42_MCPServerLabel 验证 MCPServer.ServerLabel 必填。
func TestINV42_MCPServerLabel(t *testing.T) {
	cases := []struct {
		name      string
		label     string
		expectInv string
	}{
		{"empty label", "", "INV-42"},
		{"non-empty accepted", "fixture-mcp", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := minimalValidEnvelope()
			env.CapabilityGraph.Nodes = []CapabilityNode{{
				ID:          "n_mcp",
				Kind:        CapabilityMCPServer,
				StreamReady: StreamReadyNo,
				MCPServer: &MCPServerNode{
					ServerLabel:       tc.label,
					AllowedOperations: []string{},
				},
			}}
			err := ValidateEnvelope(env)
			if tc.expectInv == "" {
				if err != nil {
					t.Fatalf("expected pass, got: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.expectInv) {
				t.Fatalf("expected %s violation, got: %v", tc.expectInv, err)
			}
		})
	}
}

// TestINV43_ProjectionNodeRefs 验证 projection.NodeID 解析 + Capability == node.Kind。
func TestINV43_ProjectionNodeRefs(t *testing.T) {
	cases := []struct {
		name           string
		projNodeID     string
		projCapability CapabilityKind
		expectInv      string
	}{
		{"unresolved node_id", "n_missing", CapabilityText, "INV-43"},
		{"capability mismatch", "n_text", CapabilityImage, "INV-43"},
		{"matched accepted", "n_text", CapabilityText, ""},
		{"empty node_id accepted", "", CapabilityText, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := minimalValidEnvelope()
			env.CapabilityGraph.Nodes = []CapabilityNode{{
				ID:          "n_text",
				Kind:        CapabilityText,
				StreamReady: StreamReadyYes,
				Text:        &TextNode{Role: "user", Block: CanonicalContentBlock{Type: "text", Text: "x"}},
			}}
			env.ProviderProjection.CapabilityResults = []CapabilityProjection{{
				Capability: tc.projCapability,
				NodeID:     tc.projNodeID,
				Verdict:    ProjectionPreserved,
			}}
			err := ValidateEnvelope(env)
			if tc.expectInv == "" {
				if err != nil {
					t.Fatalf("expected pass, got: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.expectInv) {
				t.Fatalf("expected %s violation, got: %v", tc.expectInv, err)
			}
		})
	}
}

// TestINV46_NodeSourceRefBounds 验证 source 索引非负 + 范围内。
func TestINV46_NodeSourceRefBounds(t *testing.T) {
	intPtr := func(v int) *int { return &v }
	cases := []struct {
		name      string
		source    *NodeSourceRef
		msgs      []CanonicalMessage
		stream    []CanonicalEvent
		expectInv string
	}{
		{"negative message_index", &NodeSourceRef{MessageIndex: intPtr(-1)}, nil, nil, "INV-46"},
		{"negative block_index", &NodeSourceRef{BlockIndex: intPtr(-1)}, nil, nil, "INV-46"},
		{"negative event_index", &NodeSourceRef{EventIndex: intPtr(-1)}, nil, nil, "INV-46"},
		{"message_index out of range",
			&NodeSourceRef{MessageIndex: intPtr(5)},
			[]CanonicalMessage{{Role: "user"}, {Role: "assistant"}},
			nil,
			"INV-46"},
		{"event_index out of range",
			&NodeSourceRef{EventIndex: intPtr(3)},
			nil,
			[]CanonicalEvent{{Type: "message_start"}},
			"INV-46"},
		{"source nil accepted", nil, nil, nil, ""},
		{"within bounds accepted",
			&NodeSourceRef{MessageIndex: intPtr(0), BlockIndex: intPtr(2)},
			[]CanonicalMessage{{Role: "user"}},
			nil,
			""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := minimalValidEnvelope()
			if tc.msgs != nil {
				env.Messages = tc.msgs
			}
			if tc.stream != nil {
				env.StreamEvents = tc.stream
				env.StreamPlan.Mode = StreamModeReplay
			}
			env.CapabilityGraph.Nodes = []CapabilityNode{{
				ID:          "n1",
				Kind:        CapabilityText,
				StreamReady: StreamReadyYes,
				Source:      tc.source,
				Text:        &TextNode{Role: "user", Block: CanonicalContentBlock{Type: "text", Text: "x"}},
			}}
			err := ValidateEnvelope(env)
			if tc.expectInv == "" {
				if err != nil {
					t.Fatalf("expected pass, got: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.expectInv) {
				t.Fatalf("expected %s violation, got: %v", tc.expectInv, err)
			}
		})
	}
}

// TestINV41_LiveSessionTransportEnum 验证 Live.Transport enum 守门。
func TestINV41_LiveSessionTransportEnum(t *testing.T) {
	cases := []struct {
		name      string
		value     LiveTransport
		expectInv string
	}{
		{"empty string", "", "INV-41"},
		{"bogus", "grpc", "INV-41"},
		{"http rejected", "http", "INV-41"},
		{"wss accepted", LiveTransportWSS, ""},
		{"sse accepted", LiveTransportSSE, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := minimalValidEnvelope()
			env.CapabilityGraph.Nodes = []CapabilityNode{{
				ID:          "n1",
				Kind:        CapabilityLiveSession,
				StreamReady: StreamReadyYes,
				LiveSession: &LiveSessionNode{
					SessionID:  "sess_001",
					Transport:  tc.value,
					Modalities: []string{"text"},
				},
			}}
			err := ValidateEnvelope(env)
			if tc.expectInv == "" {
				if err != nil {
					t.Fatalf("expected pass, got: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.expectInv) {
				t.Fatalf("expected %s violation, got: %v", tc.expectInv, err)
			}
		})
	}
}

// --------------------------------------------------------------------------
// P-1 D5：fixture sweep + 剩余 strict cross-ref + 新枚举
// （strict/Format/ToolNodeIDs/）
// --------------------------------------------------------------------------

// TestINV19_ToolResultRefStrict 验证 ToolResult.ToolCallID 必须匹配 ToolUse + requires edge。
func TestINV19_ToolResultRefStrict(t *testing.T) {
	mkToolUse := func(id, callID string) CapabilityNode {
		return CapabilityNode{
			ID: id, Kind: CapabilityToolUse, StreamReady: StreamReadyYes,
			ToolUse: &ToolUseNode{ToolCallID: callID, Name: "calc", Input: json.RawMessage(`{}`), Status: ToolNodeComplete},
		}
	}
	mkToolResult := func(id, callID string) CapabilityNode {
		return CapabilityNode{
			ID: id, Kind: CapabilityToolResult, StreamReady: StreamReadyNo,
			ToolResult: &ToolResultNode{
				ToolCallID: callID, Content: []CanonicalContentBlock{}, Status: ToolNodeComplete, IsError: false,
			},
		}
	}

	t.Run("unresolved tool_call_id", func(t *testing.T) {
		env := minimalValidEnvelope()
		env.CapabilityGraph.Nodes = []CapabilityNode{mkToolResult("n_tr", "t_missing")}
		err := ValidateEnvelope(env)
		if err == nil || !strings.Contains(err.Error(), "INV-19") {
			t.Fatalf("expected INV-19 unresolved, got: %v", err)
		}
	})
	t.Run("missing requires edge", func(t *testing.T) {
		env := minimalValidEnvelope()
		env.CapabilityGraph.Nodes = []CapabilityNode{mkToolUse("n_tu", "t1"), mkToolResult("n_tr", "t1")}
		err := ValidateEnvelope(env)
		if err == nil || !strings.Contains(err.Error(), "INV-19") {
			t.Fatalf("expected INV-19 missing edge, got: %v", err)
		}
	})
	t.Run("duplicate tool_call_id rejected", func(t *testing.T) {
		env := minimalValidEnvelope()
		env.CapabilityGraph.Nodes = []CapabilityNode{
			mkToolUse("n_tu1", "t1"),
			mkToolUse("n_tu2", "t1"),
		}
		err := ValidateEnvelope(env)
		if err == nil || !strings.Contains(err.Error(), "INV-19") {
			t.Fatalf("expected INV-19 duplicate, got: %v", err)
		}
	})
	t.Run("matched + edge accepted", func(t *testing.T) {
		env := minimalValidEnvelope()
		env.CapabilityGraph.Nodes = []CapabilityNode{mkToolUse("n_tu", "t1"), mkToolResult("n_tr", "t1")}
		env.CapabilityGraph.Edges = []CapabilityEdge{{ID: "e1", Type: EdgeRequires, From: "n_tr", To: "n_tu", Required: true}}
		if err := ValidateEnvelope(env); err != nil {
			t.Fatalf("expected pass, got: %v", err)
		}
	})
}

// TestINV23_AudioFormatWhitelist 验证 Audio.Format 必须在 P-1 白名单内（D5 收口）。
func TestINV23_AudioFormatWhitelist(t *testing.T) {
	cases := []struct {
		name      string
		format    string
		expectInv string
	}{
		{"empty format", "", "INV-23"},
		{"bogus format", "raw_pcm32", "INV-23"},
		{"wav accepted", "wav", ""},
		{"mp3 accepted", "mp3", ""},
		{"opus accepted", "opus", ""},
		{"pcm16 accepted", "pcm16", ""},
		{"flac accepted", "flac", ""},
		{"m4a accepted", "m4a", ""},
		{"webm accepted", "webm", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := minimalValidEnvelope()
			env.CapabilityGraph.Nodes = []CapabilityNode{{
				ID: "n_audio", Kind: CapabilityAudio, StreamReady: StreamReadyPartial,
				Audio: &AudioNode{
					Transport: MediaTransportInline,
					Format:    tc.format,
					Locator:   DataLocator{Kind: DataSourceInlineBase64, Value: "ABC="},
				},
			}}
			err := ValidateEnvelope(env)
			if tc.expectInv == "" {
				if err != nil {
					t.Fatalf("expected pass, got: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.expectInv) {
				t.Fatalf("expected %s violation, got: %v", tc.expectInv, err)
			}
		})
	}
}

// TestINV28_BatchFileRefs 验证 Batch.InputRef/OutputRef/ErrorRef 必须解析到 FileNode。
func TestINV28_BatchFileRefs(t *testing.T) {
	mkFile := func(id string) CapabilityNode {
		return CapabilityNode{
			ID: id, Kind: CapabilityFile, StreamReady: StreamReadyNo,
			File: &FileNode{
				SourceKind: DataSourceFileID,
				MediaType:  "application/jsonl",
				Locator:    DataLocator{Kind: DataSourceFileID, Value: "ext_id"},
			},
		}
	}
	mkText := func(id string) CapabilityNode {
		return CapabilityNode{
			ID: id, Kind: CapabilityText, StreamReady: StreamReadyYes,
			Text: &TextNode{Role: "user", Block: CanonicalContentBlock{Type: "text", Text: "x"}},
		}
	}
	mkBatch := func(input, output, errRef string) CapabilityNode {
		return CapabilityNode{
			ID: "n_batch", Kind: CapabilityBatch, StreamReady: StreamReadyNo,
			Batch: &BatchNode{
				JobID:      "job_1",
				Endpoint:   "/v1/batches",
				InputRef:   input,
				OutputRef:  output,
				ErrorRef:   errRef,
				Validation: BatchValidated,
			},
		}
	}

	t.Run("input_ref unresolved", func(t *testing.T) {
		env := minimalValidEnvelope()
		env.CapabilityGraph.Nodes = []CapabilityNode{mkBatch("n_missing", "", "")}
		err := ValidateEnvelope(env)
		if err == nil || !strings.Contains(err.Error(), "INV-28") {
			t.Fatalf("expected INV-28, got: %v", err)
		}
	})
	t.Run("input_ref points to text node", func(t *testing.T) {
		env := minimalValidEnvelope()
		env.CapabilityGraph.Nodes = []CapabilityNode{mkText("n_text"), mkBatch("n_text", "", "")}
		err := ValidateEnvelope(env)
		if err == nil || !strings.Contains(err.Error(), "INV-28") {
			t.Fatalf("expected INV-28 wrong kind, got: %v", err)
		}
	})
	t.Run("output_ref unresolved", func(t *testing.T) {
		env := minimalValidEnvelope()
		env.CapabilityGraph.Nodes = []CapabilityNode{mkFile("n_file_in"), mkBatch("n_file_in", "n_missing", "")}
		err := ValidateEnvelope(env)
		if err == nil || !strings.Contains(err.Error(), "INV-28") {
			t.Fatalf("expected INV-28 output_ref unresolved, got: %v", err)
		}
	})
	t.Run("all three refs accepted", func(t *testing.T) {
		env := minimalValidEnvelope()
		env.CapabilityGraph.Nodes = []CapabilityNode{
			mkFile("n_in"), mkFile("n_out"), mkFile("n_err"),
			mkBatch("n_in", "n_out", "n_err"),
		}
		if err := ValidateEnvelope(env); err != nil {
			t.Fatalf("expected pass, got: %v", err)
		}
	})
}

// TestINV41_LiveSessionToolNodeRefs 验证 ToolNodeIDs 必须解析到 tool_use/computer_use/mcp_server。
func TestINV41_LiveSessionToolNodeRefs(t *testing.T) {
	mkTool := func(id string) CapabilityNode {
		return CapabilityNode{
			ID: id, Kind: CapabilityToolUse, StreamReady: StreamReadyYes,
			ToolUse: &ToolUseNode{ToolCallID: id + "_tc", Name: "x", Input: json.RawMessage(`{}`), Status: ToolNodeComplete},
		}
	}
	mkText := func(id string) CapabilityNode {
		return CapabilityNode{
			ID: id, Kind: CapabilityText, StreamReady: StreamReadyYes,
			Text: &TextNode{Role: "user", Block: CanonicalContentBlock{Type: "text", Text: "x"}},
		}
	}
	mkLive := func(refs []string) CapabilityNode {
		return CapabilityNode{
			ID: "n_live", Kind: CapabilityLiveSession, StreamReady: StreamReadyYes,
			LiveSession: &LiveSessionNode{
				SessionID:   "s1",
				Transport:   LiveTransportWSS,
				Modalities:  []string{"text"},
				ToolNodeIDs: refs,
			},
		}
	}

	t.Run("ref unresolved", func(t *testing.T) {
		env := minimalValidEnvelope()
		env.CapabilityGraph.Nodes = []CapabilityNode{mkLive([]string{"n_missing"})}
		err := ValidateEnvelope(env)
		if err == nil || !strings.Contains(err.Error(), "INV-41") {
			t.Fatalf("expected INV-41 unresolved, got: %v", err)
		}
	})
	t.Run("ref to text node rejected", func(t *testing.T) {
		env := minimalValidEnvelope()
		env.CapabilityGraph.Nodes = []CapabilityNode{mkText("n_text"), mkLive([]string{"n_text"})}
		err := ValidateEnvelope(env)
		if err == nil || !strings.Contains(err.Error(), "INV-41") {
			t.Fatalf("expected INV-41 wrong kind, got: %v", err)
		}
	})
	t.Run("ref to tool_use accepted", func(t *testing.T) {
		env := minimalValidEnvelope()
		env.CapabilityGraph.Nodes = []CapabilityNode{mkTool("n_tu"), mkLive([]string{"n_tu"})}
		if err := ValidateEnvelope(env); err != nil {
			t.Fatalf("expected pass, got: %v", err)
		}
	})
}

// TestINV42_MCPServerNodeRefs 验证 InvocationNodeIDs / ResultNodeIDs 引用解析。
func TestINV42_MCPServerNodeRefs(t *testing.T) {
	mkTool := func(id string) CapabilityNode {
		return CapabilityNode{
			ID: id, Kind: CapabilityToolUse, StreamReady: StreamReadyYes,
			ToolUse: &ToolUseNode{ToolCallID: id + "_tc", Name: "x", Input: json.RawMessage(`{}`), Status: ToolNodeComplete},
		}
	}
	mkResult := func(id, callID string) CapabilityNode {
		return CapabilityNode{
			ID: id, Kind: CapabilityToolResult, StreamReady: StreamReadyNo,
			ToolResult: &ToolResultNode{ToolCallID: callID, Content: []CanonicalContentBlock{}, Status: ToolNodeComplete},
		}
	}
	mkText := func(id string) CapabilityNode {
		return CapabilityNode{
			ID: id, Kind: CapabilityText, StreamReady: StreamReadyYes,
			Text: &TextNode{Role: "user", Block: CanonicalContentBlock{Type: "text", Text: "x"}},
		}
	}
	mkMCP := func(inv, res []string) CapabilityNode {
		return CapabilityNode{
			ID: "n_mcp", Kind: CapabilityMCPServer, StreamReady: StreamReadyPartial,
			MCPServer: &MCPServerNode{
				ServerLabel:       "x",
				AllowedOperations: []string{},
				InvocationNodeIDs: inv,
				ResultNodeIDs:     res,
			},
		}
	}

	t.Run("invocation unresolved", func(t *testing.T) {
		env := minimalValidEnvelope()
		env.CapabilityGraph.Nodes = []CapabilityNode{mkMCP([]string{"n_missing"}, nil)}
		err := ValidateEnvelope(env)
		if err == nil || !strings.Contains(err.Error(), "INV-42") {
			t.Fatalf("expected INV-42 invocation unresolved, got: %v", err)
		}
	})
	t.Run("invocation points to text rejected", func(t *testing.T) {
		env := minimalValidEnvelope()
		env.CapabilityGraph.Nodes = []CapabilityNode{mkText("n_text"), mkMCP([]string{"n_text"}, nil)}
		err := ValidateEnvelope(env)
		if err == nil || !strings.Contains(err.Error(), "INV-42") {
			t.Fatalf("expected INV-42 invocation wrong kind, got: %v", err)
		}
	})
	t.Run("result points to tool_use rejected", func(t *testing.T) {
		env := minimalValidEnvelope()
		env.CapabilityGraph.Nodes = []CapabilityNode{mkTool("n_tu"), mkMCP(nil, []string{"n_tu"})}
		err := ValidateEnvelope(env)
		if err == nil || !strings.Contains(err.Error(), "INV-42") {
			t.Fatalf("expected INV-42 result wrong kind, got: %v", err)
		}
	})
	t.Run("matched accepted", func(t *testing.T) {
		env := minimalValidEnvelope()
		env.CapabilityGraph.Nodes = []CapabilityNode{
			mkTool("n_tu"),
			mkResult("n_tr", "n_tu_tc"),
			mkMCP([]string{"n_tu"}, []string{"n_tr"}),
		}
		env.CapabilityGraph.Edges = []CapabilityEdge{{ID: "e1", Type: EdgeRequires, From: "n_tr", To: "n_tu", Required: true}}
		if err := ValidateEnvelope(env); err != nil {
			t.Fatalf("expected pass, got: %v", err)
		}
	})
}

// TestINV49_VideoTimeRangeMonotonic 验证 Video.TimeRange 非负 + end >= start。
func TestINV49_VideoTimeRangeMonotonic(t *testing.T) {
	mkVideo := func(tr *TimeRange) CapabilityNode {
		return CapabilityNode{
			ID: "n_video", Kind: CapabilityVideo, StreamReady: StreamReadyNo,
			Video: &VideoNode{
				SourceKind: DataSourceURL,
				MediaType:  "video/mp4",
				Locator:    DataLocator{Kind: DataSourceURL, Value: "https://x/y.mp4"},
				TimeRange:  tr,
			},
		}
	}
	cases := []struct {
		name      string
		tr        *TimeRange
		expectInv string
	}{
		{"nil time_range accepted", nil, ""},
		{"negative start", &TimeRange{StartMillis: -1, EndMillis: 10}, "INV-49"},
		{"negative end", &TimeRange{StartMillis: 0, EndMillis: -1}, "INV-49"},
		{"end < start", &TimeRange{StartMillis: 100, EndMillis: 50}, "INV-49"},
		{"zero end accepted", &TimeRange{StartMillis: 100, EndMillis: 0}, ""},
		{"end > start accepted", &TimeRange{StartMillis: 100, EndMillis: 200}, ""},
		{"equal accepted", &TimeRange{StartMillis: 100, EndMillis: 100}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := minimalValidEnvelope()
			env.CapabilityGraph.Nodes = []CapabilityNode{mkVideo(tc.tr)}
			err := ValidateEnvelope(env)
			if tc.expectInv == "" {
				if err != nil {
					t.Fatalf("expected pass, got: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.expectInv) {
				t.Fatalf("expected %s violation, got: %v", tc.expectInv, err)
			}
		})
	}
}
