// auto_inject_test.go — Track C 自动 cache_control 注入测试。
package cache_routing

import (
	"encoding/json"
	"strings"
	"testing"
)

// 用 strings.Repeat 生成保证大于 4096 字节阈值的 prompt（生产典型 system
// prompt 数千字符常见）。
var longSystemPrompt = strings.Repeat(
	"You are a helpful AI assistant. This is a long system prompt to trigger cache. ",
	100,
)

func TestAutoInject_StringSystemAboveThreshold_GetsWrappedWithCC(t *testing.T) {
	body, _ := json.Marshal(map[string]any{
		"model":      "claude-3",
		"system":     longSystemPrompt,
		"messages":   []any{},
		"max_tokens": 100,
	})
	if len(longSystemPrompt) < DefaultMinSystemBytesForCache {
		t.Fatalf("test setup error: prompt 长度 %d < 阈值 %d, 加更多内容",
			len(longSystemPrompt), DefaultMinSystemBytesForCache)
	}

	out := AutoInjectSystemCacheControl(body, 0)
	if !strings.Contains(string(out), `"cache_control"`) {
		t.Errorf("长 system 应注入 cache_control: %s", out)
	}
	if !strings.Contains(string(out), `"ephemeral"`) {
		t.Errorf("应是 ephemeral 类型: %s", out)
	}
	// 原 string 应被保留在 text 字段里
	var top map[string]json.RawMessage
	_ = json.Unmarshal(out, &top)
	var sysArr []map[string]any
	if err := json.Unmarshal(top["system"], &sysArr); err != nil {
		t.Fatalf("system 应是 array: %v", err)
	}
	if len(sysArr) != 1 || sysArr[0]["type"] != "text" {
		t.Errorf("system blocks shape: %v", sysArr)
	}
}

func TestAutoInject_StringSystemBelowThreshold_Untouched(t *testing.T) {
	short := "You are helpful."
	body, _ := json.Marshal(map[string]any{
		"system": short,
		"messages": []any{},
	})
	out := AutoInjectSystemCacheControl(body, 0)
	if strings.Contains(string(out), "cache_control") {
		t.Errorf("短 system 不应注入: %s", out)
	}
	// system 字段应保持 string 形态不被 wrap
	var top map[string]any
	_ = json.Unmarshal(out, &top)
	if got, ok := top["system"].(string); !ok || got != short {
		t.Errorf("system 应保持原 string: %v", top["system"])
	}
}

func TestAutoInject_ArraySystemAboveThreshold_LastBlockGetsCC(t *testing.T) {
	// 用足够长的 text 触发 阈值
	bigText := strings.Repeat("very long text content. ", 200)
	body, _ := json.Marshal(map[string]any{
		"system": []map[string]any{
			{"type": "text", "text": bigText},
		},
	})
	out := AutoInjectSystemCacheControl(body, 0)
	if !strings.Contains(string(out), "cache_control") {
		t.Errorf("array system 应注入末 block: %s", string(out)[:200])
	}
}

func TestAutoInject_RespectsExistingCacheControl(t *testing.T) {
	bigText := strings.Repeat("long content. ", 500)
	body, _ := json.Marshal(map[string]any{
		"system": []map[string]any{
			{"type": "text", "text": bigText, "cache_control": map[string]any{"type": "ephemeral", "ttl": "1h"}},
		},
	})
	before := string(body)
	out := string(AutoInjectSystemCacheControl(body, 0))
	// caller 已声明 → 不应覆盖
	if !strings.Contains(out, `"ttl":"1h"`) {
		t.Errorf("应保留 caller 的 1h TTL, 得 %s", out)
	}
	// 字符级未必相等(顺序), 但不应新增 cache_control
	if strings.Count(out, `"cache_control"`) != strings.Count(before, `"cache_control"`) {
		t.Errorf("不应新增 cache_control marker")
	}
}

func TestAutoInject_NoSystemField_NoOp(t *testing.T) {
	body := []byte(`{"model":"claude-3","messages":[],"max_tokens":100}`)
	out := AutoInjectSystemCacheControl(body, 0)
	if string(out) != string(body) {
		t.Errorf("无 system 字段应原样: got %s", out)
	}
}

func TestAutoInject_InvalidJSON_NoOp(t *testing.T) {
	body := []byte(`not json`)
	out := AutoInjectSystemCacheControl(body, 0)
	if string(out) != string(body) {
		t.Errorf("非 JSON 应原样")
	}
}

func TestAutoInject_EmptyBody_NoOp(t *testing.T) {
	out := AutoInjectSystemCacheControl(nil, 0)
	if len(out) != 0 {
		t.Errorf("nil body 应原样")
	}
	out = AutoInjectSystemCacheControl([]byte{}, 0)
	if len(out) != 0 {
		t.Errorf("空 body 应原样")
	}
}

func TestAutoInject_CustomMinBytes(t *testing.T) {
	short := "short"
	body, _ := json.Marshal(map[string]any{"system": short})
	// minBytes=1 强制注入即使短
	out := AutoInjectSystemCacheControl(body, 1)
	if !strings.Contains(string(out), "cache_control") {
		t.Errorf("自定义 minBytes=1 应允许短 prompt 注入")
	}
}

func TestHasCacheControlMarker(t *testing.T) {
	cases := []struct {
		body string
		want bool
	}{
		{`{"system":"x"}`, false},
		{`{"system":[{"type":"text","cache_control":{}}]}`, true},
		{``, false},
		{`{"messages":[{"content":"cache_control mention but not key"}]}`, false},
	}
	for _, c := range cases {
		got := HasCacheControlMarker([]byte(c.body))
		if got != c.want {
			t.Errorf("HasCacheControlMarker(%q)=%v want %v", c.body, got, c.want)
		}
	}
}

func TestAutoInject_MessagesUnchanged(t *testing.T) {
	bigText := strings.Repeat("x", 5000)
	body, _ := json.Marshal(map[string]any{
		"system":   bigText,
		"messages": []map[string]string{{"role": "user", "content": "hi"}},
	})
	out := AutoInjectSystemCacheControl(body, 0)
	var top map[string]json.RawMessage
	_ = json.Unmarshal(out, &top)
	// messages 应原样 (不参与 cache_control)
	var msgs []map[string]string
	_ = json.Unmarshal(top["messages"], &msgs)
	if len(msgs) != 1 || msgs[0]["role"] != "user" || msgs[0]["content"] != "hi" {
		t.Errorf("messages 应原样, 得 %v", msgs)
	}
}

// codex BLOCKING B2 回归: system 数组含 null block 时不能 panic。
func TestAutoInject_ArrayWithNullBlock_NoPanic(t *testing.T) {
	bigText := strings.Repeat("very long content. ", 200)
	body, _ := json.Marshal(map[string]any{
		"system": []any{
			map[string]any{"type": "text", "text": bigText},
			nil,
		},
	})
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("should not panic on null block: %v", r)
		}
	}()
	out := AutoInjectSystemCacheControl(body, 0)
	if out == nil {
		t.Errorf("应返回 body 而不是 nil")
	}
}

// codex BLOCKING B2 变体: system 是 [null] 单 null 元素。
func TestAutoInject_ArrayOnlyNull_NoPanic(t *testing.T) {
	body := []byte(`{"system":[null]}`)
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("should not panic on [null]: %v", r)
		}
	}()
	out := AutoInjectSystemCacheControl(body, 1)
	if string(out) != string(body) {
		t.Errorf("[null] 应原样")
	}
}

// codex BLOCKING B2 变体: system 末块是 string ("plain text" 形态, 非 object)。
func TestAutoInject_ArrayLastBlockNonObject_NoPanic(t *testing.T) {
	bigText := strings.Repeat("a long string token. ", 250)
	body, _ := json.Marshal(map[string]any{
		"system": []any{
			map[string]any{"type": "text", "text": bigText},
			"a string final block not an object",
		},
	})
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("should not panic on non-object last block: %v", r)
		}
	}()
	out := AutoInjectSystemCacheControl(body, 0)
	if out == nil || len(out) == 0 {
		t.Errorf("应返回 body")
	}
}

// codex BLOCKING B3 加固: 任意 block 已有 cache_control → 全 body no-op,
// 不只末块。
func TestAutoInject_EarlierBlockHasCC_FullSkip(t *testing.T) {
	bigText := strings.Repeat("padding text. ", 500)
	body, _ := json.Marshal(map[string]any{
		"system": []map[string]any{
			{"type": "text", "text": bigText, "cache_control": map[string]any{"type": "ephemeral"}},
			{"type": "text", "text": "tail block"},
		},
	})
	beforeCount := strings.Count(string(body), `"cache_control"`)
	out := AutoInjectSystemCacheControl(body, 0)
	afterCount := strings.Count(string(out), `"cache_control"`)
	if afterCount != beforeCount {
		t.Errorf("早期 block 已有 cc 时不应再注入末块: before=%d after=%d", beforeCount, afterCount)
	}
}
