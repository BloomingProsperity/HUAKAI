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
