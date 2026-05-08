// passthrough_test.go — U7-A 单测：PassthroughEnvelope + UnmarshalWithExtras
// + MergeExtrasInto 行为契约。
//
// 测试覆盖：
//   - happy: known + unknown 字段混合 → typed 装 known，Extra 装 unknown
//   - 字段冲突：typed key 与 unknown 同名 → typed 优先（merge 不覆盖）
//   - 嵌套 unknown：保留 RawMessage 嵌套结构
//   - 空 Extra / nil envelope: 原样不破
//   - 非 object JSON（数组 / 标量）: 不 panic
//   - 反射 cache: 同 typed 类型多次调用不重复反射
//   - 并发: -race 多 goroutine UnmarshalWithExtras 不 race
package proto

import (
	"bytes"
	"encoding/json"
	"reflect"
	"sync"
	"testing"
)

// 测试用 typed struct（模拟一个简化的 OpenAI chunk）
type passthroughTestTypedChunk struct {
	ID    string `json:"id,omitempty"`
	Model string `json:"model,omitempty"`
	Index int    `json:"index"`
}

func TestUnmarshalWithExtras_HappyKnownAndUnknown(t *testing.T) {
	raw := []byte(`{"id":"x","model":"gpt-4o","index":2,"system_fingerprint":"fp_abc","logprobs":null,"service_tier":"default"}`)
	var typed passthroughTestTypedChunk
	var env PassthroughEnvelope

	if err := UnmarshalWithExtras(raw, &typed, &env); err != nil {
		t.Fatalf("err=%v", err)
	}
	if typed.ID != "x" || typed.Model != "gpt-4o" || typed.Index != 2 {
		t.Errorf("typed=%+v", typed)
	}
	wantExtra := []string{"system_fingerprint", "logprobs", "service_tier"}
	for _, k := range wantExtra {
		if _, ok := env.Extra[k]; !ok {
			t.Errorf("Extra 缺 %q", k)
		}
	}
	if len(env.Extra) != len(wantExtra) {
		t.Errorf("Extra=%v 期望 %d 项", env.Extra, len(wantExtra))
	}
}

func TestUnmarshalWithExtras_NestedUnknownPreservesStructure(t *testing.T) {
	raw := []byte(`{"id":"x","prompt_filter_results":[{"index":0,"results":{"jailbreak":{"filtered":false}}}]}`)
	var typed passthroughTestTypedChunk
	var env PassthroughEnvelope

	if err := UnmarshalWithExtras(raw, &typed, &env); err != nil {
		t.Fatalf("err=%v", err)
	}
	pfr, ok := env.Extra["prompt_filter_results"]
	if !ok {
		t.Fatal("prompt_filter_results 应在 Extra")
	}
	// 验证嵌套结构能再 unmarshal 出来
	var nested []map[string]any
	if err := json.Unmarshal(pfr, &nested); err != nil {
		t.Fatalf("嵌套 unmarshal err=%v", err)
	}
	if len(nested) != 1 || nested[0]["index"].(float64) != 0 {
		t.Errorf("嵌套结构丢失：%v", nested)
	}
}

func TestUnmarshalWithExtras_NilEnvelope_FallsBackToPlainUnmarshal(t *testing.T) {
	raw := []byte(`{"id":"y","unknown":"dropped"}`)
	var typed passthroughTestTypedChunk

	if err := UnmarshalWithExtras(raw, &typed, nil); err != nil {
		t.Fatalf("err=%v", err)
	}
	if typed.ID != "y" {
		t.Errorf("typed.ID=%q", typed.ID)
	}
	// nil envelope 时未识别字段被丢弃（行为退化为 plain unmarshal）
}

func TestUnmarshalWithExtras_EmptyRaw(t *testing.T) {
	var typed passthroughTestTypedChunk
	var env PassthroughEnvelope
	if err := UnmarshalWithExtras(nil, &typed, &env); err != nil {
		t.Errorf("空 raw 不应报错，err=%v", err)
	}
	if env.Extra != nil {
		t.Errorf("空 raw 时 Extra 应为 nil")
	}
}

func TestUnmarshalWithExtras_NonObjectJSON(t *testing.T) {
	// 顶层是数组：typed unmarshal 失败，extras 不适用
	raw := []byte(`[1, 2, 3]`)
	var typed passthroughTestTypedChunk
	var env PassthroughEnvelope

	err := UnmarshalWithExtras(raw, &typed, &env)
	if err == nil {
		t.Fatal("数组到 struct 应报错")
	}
	if env.Extra != nil {
		t.Errorf("非 object 不应填 Extra")
	}
}

func TestUnmarshalWithExtras_TypedNil_AllFieldsToExtra(t *testing.T) {
	// typed=nil：所有顶层字段都进 Extra
	raw := []byte(`{"a":1,"b":"two"}`)
	var env PassthroughEnvelope
	if err := UnmarshalWithExtras(raw, nil, &env); err != nil {
		t.Fatalf("err=%v", err)
	}
	if len(env.Extra) != 2 {
		t.Errorf("Extra=%v 期望 2 项", env.Extra)
	}
}

func TestMergeExtrasInto_HappyMerge(t *testing.T) {
	typedJSON := []byte(`{"id":"x","model":"gpt-4o"}`)
	env := &PassthroughEnvelope{Extra: map[string]json.RawMessage{
		"system_fingerprint": json.RawMessage(`"fp_abc"`),
		"service_tier":       json.RawMessage(`"default"`),
	}}
	merged, err := MergeExtrasInto(typedJSON, env)
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	var got map[string]json.RawMessage
	if err := json.Unmarshal(merged, &got); err != nil {
		t.Fatalf("merged unmarshal err=%v", err)
	}
	for _, k := range []string{"id", "model", "system_fingerprint", "service_tier"} {
		if _, ok := got[k]; !ok {
			t.Errorf("merged 缺 %q", k)
		}
	}
}

func TestMergeExtrasInto_TypedWinsOnConflict(t *testing.T) {
	typedJSON := []byte(`{"id":"typed_value"}`)
	env := &PassthroughEnvelope{Extra: map[string]json.RawMessage{
		"id":    json.RawMessage(`"extras_value"`),
		"extra": json.RawMessage(`"e"`),
	}}
	merged, _ := MergeExtrasInto(typedJSON, env)
	var got map[string]json.RawMessage
	_ = json.Unmarshal(merged, &got)
	if !bytes.Equal(got["id"], json.RawMessage(`"typed_value"`)) {
		t.Errorf("冲突时 typed 应优先，得 %q", got["id"])
	}
	if !bytes.Equal(got["extra"], json.RawMessage(`"e"`)) {
		t.Errorf("非冲突 extra 应保留，得 %q", got["extra"])
	}
}

func TestMergeExtrasInto_NilEnvelope_ReturnsTypedAsIs(t *testing.T) {
	typedJSON := []byte(`{"id":"x"}`)
	merged, err := MergeExtrasInto(typedJSON, nil)
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	if !bytes.Equal(merged, typedJSON) {
		t.Errorf("nil env 应原样返回，得 %q", merged)
	}
}

func TestMergeExtrasInto_EmptyExtras_ReturnsTypedAsIs(t *testing.T) {
	typedJSON := []byte(`{"id":"x"}`)
	env := &PassthroughEnvelope{}
	merged, err := MergeExtrasInto(typedJSON, env)
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	if !bytes.Equal(merged, typedJSON) {
		t.Errorf("空 Extra 应原样返回，得 %q", merged)
	}
}

func TestMergeExtrasInto_TypedNotObject_ReturnsAsIs(t *testing.T) {
	// typedJSON 是数组，extras 不能合并到非 object，应原样返回
	typedJSON := []byte(`[1,2,3]`)
	env := &PassthroughEnvelope{Extra: map[string]json.RawMessage{
		"x": json.RawMessage(`"y"`),
	}}
	merged, err := MergeExtrasInto(typedJSON, env)
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	if !bytes.Equal(merged, typedJSON) {
		t.Errorf("数组应原样返回，得 %q", merged)
	}
}

// TestKnownJSONFields_ReflectsCorrectly 校验 reflect cache 行为。
func TestKnownJSONFields_ReflectsCorrectly(t *testing.T) {
	typed := passthroughTestTypedChunk{}
	known := knownJSONFields(&typed)
	want := map[string]struct{}{"id": {}, "model": {}, "index": {}}
	if !reflect.DeepEqual(known, want) {
		t.Errorf("known=%v want=%v", known, want)
	}

	// 二次调用应命中 cache（无副作用）
	known2 := knownJSONFields(&typed)
	if !reflect.DeepEqual(known, known2) {
		t.Errorf("cache 不一致")
	}
}

// TestKnownJSONFields_HandlesDashTag 验证 `json:"-"` 字段不进 known set
// 因为它本身不上 wire，与 unknown 字段无关。
func TestKnownJSONFields_HandlesDashTag(t *testing.T) {
	type withDash struct {
		Visible string `json:"visible"`
		Hidden  string `json:"-"`
	}
	known := knownJSONFields(&withDash{})
	if _, ok := known["visible"]; !ok {
		t.Error("visible 应在 known")
	}
	if _, ok := known["-"]; ok {
		t.Error(`json:"-" 字段不应在 known`)
	}
	if _, ok := known["Hidden"]; ok {
		t.Error("Go field name 不应作为 fallback 进入")
	}
}

// TestUnmarshalWithExtras_Concurrent 验证 -race 下 typeCache 并发安全。
func TestUnmarshalWithExtras_Concurrent(t *testing.T) {
	raw := []byte(`{"id":"x","unknown":"u","model":"m"}`)
	const n = 64
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			var typed passthroughTestTypedChunk
			var env PassthroughEnvelope
			if err := UnmarshalWithExtras(raw, &typed, &env); err != nil {
				t.Errorf("err=%v", err)
				return
			}
			if typed.ID != "x" {
				t.Errorf("typed.ID=%q", typed.ID)
			}
			if _, ok := env.Extra["unknown"]; !ok {
				t.Errorf("unknown 字段应进 Extra")
			}
		}()
	}
	wg.Wait()
}

// TestPassthroughRoundtrip_RealOpenAIFields 用真实 OpenAI 新字段做 roundtrip
// 烟测：模拟 vendor 加 system_fingerprint / service_tier / logprobs / 等
// 已知但 HUAKAI typed struct 未声明的字段。
func TestPassthroughRoundtrip_RealOpenAIFields(t *testing.T) {
	// 这些字段是 OpenAI 在 2024-2025 加的，HUAKAI 当前 typed 结构未声明
	upstream := []byte(`{
		"id":"chatcmpl-x",
		"model":"gpt-4o-2024-11-20",
		"index":0,
		"system_fingerprint":"fp_b04fe7ce4f",
		"service_tier":"scale",
		"logprobs":null,
		"prompt_filter_results":[{"index":0,"content_filter_results":{"hate":{"filtered":false}}}]
	}`)

	var typed passthroughTestTypedChunk
	var env PassthroughEnvelope
	if err := UnmarshalWithExtras(upstream, &typed, &env); err != nil {
		t.Fatalf("upstream unmarshal err=%v", err)
	}

	// 模拟 client adapter 重新 marshal 一个简单 typed 输出
	clientTyped := struct {
		ID    string `json:"id"`
		Model string `json:"model"`
	}{ID: typed.ID, Model: typed.Model}
	clientJSON, _ := json.Marshal(clientTyped)

	merged, err := MergeExtrasInto(clientJSON, &env)
	if err != nil {
		t.Fatalf("merge err=%v", err)
	}

	// 验证：merged 同时含 typed 字段 + 全部 vendor extras
	var got map[string]json.RawMessage
	_ = json.Unmarshal(merged, &got)

	required := []string{
		"id", "model", // typed
		"system_fingerprint", "service_tier", "logprobs", "prompt_filter_results", // vendor
	}
	for _, k := range required {
		if _, ok := got[k]; !ok {
			t.Errorf("merged 缺 %q（vendor 字段透传失败）", k)
		}
	}
	// 嵌套结构完整性
	if !bytes.Contains(got["prompt_filter_results"], []byte("content_filter_results")) {
		t.Errorf("嵌套结构丢失：%s", got["prompt_filter_results"])
	}
}
