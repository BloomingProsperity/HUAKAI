// R7.2 变更器测试。
// 泳道：implementer-claude。
package gateway

import (
	"encoding/json"
	"strings"
	"testing"
)

// ---- 辅助函数 ----

// mustMarshal 把 v 编码为 JSON，出错则使测试失败。
func mustMarshal(t *testing.T, v interface{}) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("mustMarshal: %v", err)
	}
	return b
}

// bodyHasKey 在 keyPath 处的原始 JSON 对象包含 key 时返回 true。
// keyPath 对嵌套对象使用简单的点号表示法（此处未用到，但保留该
// 辅助函数以便阅读）。
func bodyHasKey(body []byte, key string) bool {
	return strings.Contains(string(body), `"`+key+`"`)
}

// ---- 测试 1：空 plan → 字节语义 ----

// TestApplyBreakpoints_EmptyPlan 验证空 plan 产生的 body 在逻辑内容上
// 往返一致（InspectCacheControl 结果相同），即使序列化在键顺序上有差异。
func TestApplyBreakpoints_EmptyPlan(t *testing.T) {
	body := []byte(`{
		"model": "claude-3-5-sonnet",
		"max_tokens": 64,
		"messages": [{"role": "user", "content": "hello"}]
	}`)

	before, err := InspectCacheControl(body)
	if err != nil {
		t.Fatal(err)
	}

	result, err := ApplyBreakpoints(body, BreakpointSuggestion{})
	if err != nil {
		t.Fatal(err)
	}

	after, err := InspectCacheControl(result.Body)
	if err != nil {
		t.Fatal(err)
	}

	if before.Count != after.Count {
		t.Fatalf("Count changed from %d to %d on empty plan", before.Count, after.Count)
	}
	if len(result.Applied) != 0 {
		t.Fatalf("Applied = %d; want 0", len(result.Applied))
	}
	if len(result.Skipped) != 0 {
		t.Fatalf("Skipped = %d; want 0", len(result.Skipped))
	}
}

// ---- 测试 2：1 个 system 断点 → InspectCacheControl 能识别它 ----

func TestApplyBreakpoints_OneSystemBreakpoint(t *testing.T) {
	body := []byte(`{
		"messages": [{"role": "user", "content": "hi"}],
		"system": [{"type": "text", "text": "policy"}]
	}`)

	before, err := InspectCacheControl(body)
	if err != nil {
		t.Fatal(err)
	}
	if before.Count != 0 {
		t.Fatalf("precondition: expected 0 breakpoints, got %d", before.Count)
	}

	plan := BreakpointSuggestion{
		Add: []CacheControlLocation{
			{Path: "system", Index: 0, Type: "ephemeral", TTL: ""},
		},
	}
	result, err := ApplyBreakpoints(body, plan)
	if err != nil {
		t.Fatal(err)
	}

	after, err := InspectCacheControl(result.Body)
	if err != nil {
		t.Fatal(err)
	}

	if after.Count != before.Count+1 {
		t.Fatalf("Count = %d, want %d", after.Count, before.Count+1)
	}
	if len(result.Applied) != 1 {
		t.Fatalf("Applied = %d; want 1", len(result.Applied))
	}
	if result.Applied[0].Path != "system" || result.Applied[0].Index != 0 {
		t.Fatalf("Applied[0] = %+v; want system[0]", result.Applied[0])
	}
}

// ---- 测试 3：上限 4 个 → 最终 count == 4 ----

func TestApplyBreakpoints_MaxFourCap(t *testing.T) {
	// body 起始有 2 个断点；plan 再加 4 个 → 只能成功插入 2 个。
	body := []byte(`{
		"system": [
			{"type": "text", "text": "s0", "cache_control": {"type": "ephemeral"}},
			{"type": "text", "text": "s1", "cache_control": {"type": "ephemeral"}}
		],
		"messages": [{"role": "user", "content": [
			{"type": "text", "text": "m0"},
			{"type": "text", "text": "m1"}
		]}],
		"tools": [
			{"name": "t0", "description": "x", "input_schema": {"type": "object"}},
			{"name": "t1", "description": "y", "input_schema": {"type": "object"}}
		]
	}`)

	before, err := InspectCacheControl(body)
	if err != nil {
		t.Fatal(err)
	}
	if before.Count != 2 {
		t.Fatalf("precondition: expected 2 breakpoints, got %d", before.Count)
	}

	plan := BreakpointSuggestion{
		Add: []CacheControlLocation{
			{Path: "tools", Index: 0, Type: "ephemeral"},
			{Path: "tools", Index: 1, Type: "ephemeral"},
			{Path: "messages", Index: 0, Type: "ephemeral"},
			// 第 4 个会把总数推到 6 —— 这 4 个里只有前 2 个能成功插入。
			{Path: "system", Index: 0, Type: "ephemeral"}, // 已有 CC → 以不同原因跳过
		},
	}
	result, err := ApplyBreakpoints(body, plan)
	if err != nil {
		t.Fatal(err)
	}

	after, err := InspectCacheControl(result.Body)
	if err != nil {
		t.Fatal(err)
	}

	if after.Count != 4 {
		t.Fatalf("Count = %d; want 4 (cap)", after.Count)
	}
}

// ---- 测试 4：TTL="1h" → JSON 含 "ttl":"1h" ----

func TestApplyBreakpoints_TTLOneHour(t *testing.T) {
	body := []byte(`{
		"messages": [{"role": "user", "content": [
			{"type": "text", "text": "long context"}
		]}]
	}`)

	plan := BreakpointSuggestion{
		Add: []CacheControlLocation{
			{Path: "messages", Index: 0, Type: "ephemeral", TTL: "1h"},
		},
	}
	result, err := ApplyBreakpoints(body, plan)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Applied) != 1 {
		t.Fatalf("Applied = %d; want 1", len(result.Applied))
	}

	// 原始 JSON 必须含值为 "1h" 的 ttl 键。
	if !strings.Contains(string(result.Body), `"ttl":"1h"`) &&
		!strings.Contains(string(result.Body), `"ttl": "1h"`) {
		t.Fatalf("result body does not contain ttl:1h; got: %s", result.Body)
	}

	// Inspect 必须正确解析出 TTL。
	after, err := InspectCacheControl(result.Body)
	if err != nil {
		t.Fatal(err)
	}
	if after.Count != 1 {
		t.Fatalf("Count = %d; want 1", after.Count)
	}
	if after.Locations[0].TTL != "1h" {
		t.Fatalf("TTL = %q; want \"1h\"", after.Locations[0].TTL)
	}
}

// ---- 测试 5：已占用 → Skipped + body 计数不变 ----

func TestApplyBreakpoints_AlreadyOccupied(t *testing.T) {
	body := []byte(`{
		"messages": [{"role": "user", "content": [
			{"type": "text", "text": "x", "cache_control": {"type": "ephemeral"}}
		]}]
	}`)

	before, err := InspectCacheControl(body)
	if err != nil {
		t.Fatal(err)
	}

	plan := BreakpointSuggestion{
		Add: []CacheControlLocation{
			{Path: "messages", Index: 0, Type: "ephemeral"},
		},
	}
	result, err := ApplyBreakpoints(body, plan)
	if err != nil {
		t.Fatal(err)
	}

	if len(result.Applied) != 0 {
		t.Fatalf("Applied = %d; want 0 (already occupied)", len(result.Applied))
	}
	if len(result.Skipped) != 1 {
		t.Fatalf("Skipped = %d; want 1", len(result.Skipped))
	}
	if result.Skipped[0].Reason != skipReasonAlreadyHas {
		t.Fatalf("Skipped reason = %q; want %q", result.Skipped[0].Reason, skipReasonAlreadyHas)
	}

	after, err := InspectCacheControl(result.Body)
	if err != nil {
		t.Fatal(err)
	}
	if after.Count != before.Count {
		t.Fatalf("Count changed from %d to %d; should be unchanged", before.Count, after.Count)
	}
}

// ---- 测试 6：path/index 未找到 → Skipped "location not found" ----

func TestApplyBreakpoints_LocationNotFound(t *testing.T) {
	body := []byte(`{
		"messages": [{"role": "user", "content": "hi"}]
	}`)

	plan := BreakpointSuggestion{
		Add: []CacheControlLocation{
			{Path: "tools", Index: 99, Type: "ephemeral"},       // tools 不存在
			{Path: "system", Index: 5, Type: "ephemeral"},       // system 不存在
			{Path: "messages", Index: 99, Type: "ephemeral"},    // 下标越界
		},
	}
	result, err := ApplyBreakpoints(body, plan)
	if err != nil {
		t.Fatal(err)
	}

	if len(result.Applied) != 0 {
		t.Fatalf("Applied = %d; want 0", len(result.Applied))
	}
	if len(result.Skipped) != 3 {
		t.Fatalf("Skipped = %d; want 3", len(result.Skipped))
	}
	for _, s := range result.Skipped {
		if s.Reason != skipReasonNotFound {
			t.Fatalf("Skipped reason = %q; want %q", s.Reason, skipReasonNotFound)
		}
	}
}

// ---- 测试 7：plan.Add > 4 → 先填满 Applied，多出的 Skipped "would exceed cap" ----

func TestApplyBreakpoints_PlanExceedsCap(t *testing.T) {
	// 5 个不同位置；只应有 4 个进入 Applied（上限 4，起始 count=0）。
	body := []byte(`{
		"system": [
			{"type": "text", "text": "s0"},
			{"type": "text", "text": "s1"}
		],
		"messages": [
			{"role": "user", "content": [{"type": "text", "text": "m0"}]},
			{"role": "user", "content": [{"type": "text", "text": "m1"}]}
		],
		"tools": [
			{"name": "t0", "description": "x", "input_schema": {"type": "object"}}
		]
	}`)

	plan := BreakpointSuggestion{
		Add: []CacheControlLocation{
			{Path: "system", Index: 0, Type: "ephemeral"},
			{Path: "system", Index: 1, Type: "ephemeral"},
			{Path: "messages", Index: 0, Type: "ephemeral"},
			{Path: "messages", Index: 1, Type: "ephemeral"},
			{Path: "tools", Index: 0, Type: "ephemeral"}, // 第 5 个 → 会超过上限
		},
	}
	result, err := ApplyBreakpoints(body, plan)
	if err != nil {
		t.Fatal(err)
	}

	if len(result.Applied) != 4 {
		t.Fatalf("Applied = %d; want 4", len(result.Applied))
	}
	if len(result.Skipped) != 1 {
		t.Fatalf("Skipped = %d; want 1", len(result.Skipped))
	}
	if result.Skipped[0].Reason != skipReasonExceedsCap {
		t.Fatalf("Skipped reason = %q; want %q", result.Skipped[0].Reason, skipReasonExceedsCap)
	}

	after, err := InspectCacheControl(result.Body)
	if err != nil {
		t.Fatal(err)
	}
	if after.Count != 4 {
		t.Fatalf("after.Count = %d; want 4", after.Count)
	}
}

// ---- 测试 8：往返 —— Inspect → Suggest → Apply → Inspect，count += len(Applied) ----

func TestApplyBreakpoints_RoundTrip(t *testing.T) {
	body := []byte(`{
		"model": "claude-sonnet-4-6",
		"max_tokens": 256,
		"system": [{"type": "text", "text": "You are helpful."}],
		"messages": [
			{"role": "user", "content": [{"type": "text", "text": "What is Go?"}]},
			{"role": "assistant", "content": "Go is a language."},
			{"role": "user", "content": [{"type": "text", "text": "Tell me more."}]}
		],
		"tools": [
			{"name": "search", "description": "web search", "input_schema": {"type": "object"}}
		]
	}`)

	before, err := InspectCacheControl(body)
	if err != nil {
		t.Fatal(err)
	}

	suggestion, err := SuggestBreakpoints(body, before, nil)
	if err != nil {
		t.Fatal(err)
	}

	if len(suggestion.Add) == 0 {
		t.Skip("no candidates suggested; round-trip test not applicable")
	}

	result, err := ApplyBreakpoints(body, suggestion)
	if err != nil {
		t.Fatal(err)
	}

	after, err := InspectCacheControl(result.Body)
	if err != nil {
		t.Fatal(err)
	}

	want := before.Count + len(result.Applied)
	if after.Count != want {
		t.Fatalf("after.Count = %d; want before.Count(%d) + Applied(%d) = %d",
			after.Count, before.Count, len(result.Applied), want)
	}
}

// ---- 测试 9：非法 JSON → error ----

func TestApplyBreakpoints_InvalidJSON(t *testing.T) {
	_, err := ApplyBreakpoints([]byte(`{not valid json`), BreakpointSuggestion{})
	if err == nil {
		t.Fatal("expected error for invalid JSON; got nil")
	}
}

// ---- 测试 10：TTL 顺序 —— WithTTLOrdering 正确重排 ----

func TestApplyBreakpointsWithTTLOrdering_Reorders(t *testing.T) {
	// plan 中短 TTL 在前、长 TTL 在后（顺序错误）。
	// WithTTLOrdering 应重排为长 TTL 在前。
	body := []byte(`{
		"system": [
			{"type": "text", "text": "s0"},
			{"type": "text", "text": "s1"}
		],
		"messages": [{"role": "user", "content": [{"type": "text", "text": "hi"}]}]
	}`)

	// 故意打乱顺序：短 TTL 在前，长 TTL 在后。
	plan := BreakpointSuggestion{
		Add: []CacheControlLocation{
			{Path: "messages", Index: 0, Type: "ephemeral", TTL: ""},   // 短
			{Path: "system", Index: 1, Type: "ephemeral", TTL: "1h"},   // 长
		},
	}

	result, err := ApplyBreakpointsWithTTLOrdering(body, plan)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result.Applied) != 2 {
		t.Fatalf("Applied = %d; want 2", len(result.Applied))
	}

	// 第一个被插入的位置应是长 TTL（"1h"）那个。
	if result.Applied[0].TTL != "1h" {
		t.Fatalf("Applied[0].TTL = %q; want \"1h\" (should be applied first after reorder)",
			result.Applied[0].TTL)
	}
	if result.Applied[1].TTL != "" {
		t.Fatalf("Applied[1].TTL = %q; want \"\" (short TTL applied second)",
			result.Applied[1].TTL)
	}

	// 最终 body 必须通过 TTL 顺序校验。
	after, err := InspectCacheControl(result.Body)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateTTLOrdering(after); err != nil {
		t.Fatalf("TTL ordering violated in result: %v", err)
	}
}

// ---- 附加：TTL="" → JSON 中无 ttl 键 ----

func TestApplyBreakpoints_TTLDefaultNoKey(t *testing.T) {
	body := []byte(`{
		"messages": [{"role": "user", "content": [
			{"type": "text", "text": "content"}
		]}]
	}`)

	plan := BreakpointSuggestion{
		Add: []CacheControlLocation{
			{Path: "messages", Index: 0, Type: "ephemeral", TTL: ""},
		},
	}
	result, err := ApplyBreakpoints(body, plan)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Applied) != 1 {
		t.Fatalf("Applied = %d; want 1", len(result.Applied))
	}

	// 不得含 "ttl" 键。
	if strings.Contains(string(result.Body), `"ttl"`) {
		t.Fatalf("body should not contain ttl key for default TTL; got: %s", result.Body)
	}
}

// ---- 附加：ApplyBreakpoints 不改动输入 body ----

func TestApplyBreakpoints_DoesNotMutateInput(t *testing.T) {
	body := []byte(`{"messages":[{"role":"user","content":[{"type":"text","text":"hi"}]}]}`)
	original := make([]byte, len(body))
	copy(original, body)

	plan := BreakpointSuggestion{
		Add: []CacheControlLocation{
			{Path: "messages", Index: 0, Type: "ephemeral"},
		},
	}
	_, err := ApplyBreakpoints(body, plan)
	if err != nil {
		t.Fatal(err)
	}

	// body 切片必须保持不变。
	if string(body) != string(original) {
		t.Fatalf("input body was mutated:\nbefore: %s\nafter:  %s", original, body)
	}
}
