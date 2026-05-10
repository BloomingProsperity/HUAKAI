package proto

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// minimalValidEnvelope 构造一份恰好满足 INV-4/5/10/11 的最小 envelope。
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

// TestINV13_StreamPlanModeRequired 验证 INV-13：StreamPlan.Mode 必填。
func TestINV13_StreamPlanModeRequired(t *testing.T) {
	env := minimalValidEnvelope()
	env.StreamPlan.Mode = ""
	err := ValidateEnvelope(env)
	if err == nil || !strings.Contains(err.Error(), "INV-13") {
		t.Fatalf("expected INV-13 (Mode required) violation, got: %v", err)
	}
}

// TestINV13_StreamPlanModeBogus 验证 INV-13：StreamPlan.Mode 非合法枚举值被拒。
func TestINV13_StreamPlanModeBogus(t *testing.T) {
	env := minimalValidEnvelope()
	env.StreamPlan.Mode = StreamMode("bogus_mode")
	err := ValidateEnvelope(env)
	if err == nil || !strings.Contains(err.Error(), "INV-13") {
		t.Fatalf("expected INV-13 (bogus Mode) violation, got: %v", err)
	}
}

// TestINV13_StreamPlanModeAllowedValues 验证 INV-13：buffered/streaming/replay 全部通过。
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

// TestINV6_NilVsEmptyStreamEvents 验证 INV-6 区分 nil vs empty StreamEvents。
//
//   - BufferedResponse + StreamEvents:nil → 通过（仅 buffered 形态）
//   - BufferedResponse + StreamEvents:[] → 通过（显式空切片，非 replay payload）
//   - BufferedResponse + StreamEvents:[event{...}] → 报 INV-6
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
// 用于 M3 全集 INV-1 round-trip 校验：
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
// 每个 node 都满足 INV-3 tagged-union（恰好一个 payload 非 nil）；ID 命名 `n_<kind>_1`。
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
				OutputRef:       "file_output_001",
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
// INV-12 要求 key 前缀必须是 vendor: 或 experimental:；本测试覆盖三种典型 payload 形态：
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
