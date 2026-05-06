// R7.3 测试：覆盖 system 字段三种形态 × 三种模式的核心路径，包含幂等、
// cache_control 保留、未知字段保留与 Reason 封闭枚举校验。
package gateway

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

// 测试用 prefix。故意写成自定义文案而非 sub2api 的硬编码常量，确认 HUAKAI
// 引擎对 PrefixText 的配置化处理是健全的。
const testRewritePrefix = "你正在通过 HUAKAI 网关访问。"

func TestRewriteSystem_Table(t *testing.T) {
	tests := []struct {
		name        string
		input       []byte
		plan        SystemRewritePlan
		wantApplied bool
		wantReason  string
		wantErr     bool
		assertBody  func(t *testing.T, body []byte)
	}{
		{
			name:       "空 body 解析失败",
			input:      nil,
			plan:       SystemRewritePlan{PrefixText: testRewritePrefix},
			wantReason: reasonInvalidBody,
			wantErr:    true,
		},
		{
			name:        "缺省 system 注入字符串",
			input:       []byte(`{"model":"claude","messages":[]}`),
			plan:        SystemRewritePlan{PrefixText: testRewritePrefix},
			wantApplied: true,
			wantReason:  reasonInsertedString,
			assertBody: func(t *testing.T, body []byte) {
				if got := readSystemString(t, body); got != testRewritePrefix {
					t.Fatalf("system = %q", got)
				}
			},
		},
		{
			name:        "null system 等同缺省",
			input:       []byte(`{"system":null}`),
			plan:        SystemRewritePlan{PrefixText: testRewritePrefix},
			wantApplied: true,
			wantReason:  reasonInsertedString,
			assertBody: func(t *testing.T, body []byte) {
				if got := readSystemString(t, body); got != testRewritePrefix {
					t.Fatalf("system = %q", got)
				}
			},
		},
		{
			name:        "空字符串走重写路径",
			input:       []byte(`{"system":""}`),
			plan:        SystemRewritePlan{PrefixText: testRewritePrefix},
			wantApplied: true,
			wantReason:  reasonRewroteString,
			assertBody: func(t *testing.T, body []byte) {
				if got := readSystemString(t, body); got != testRewritePrefix {
					t.Fatalf("system = %q", got)
				}
			},
		},
		{
			name:        "字符串前缀注入",
			input:       []byte(`{"system":"Foo"}`),
			plan:        SystemRewritePlan{PrefixText: testRewritePrefix},
			wantApplied: true,
			wantReason:  reasonRewroteString,
			assertBody: func(t *testing.T, body []byte) {
				want := testRewritePrefix + "\n\nFoo"
				if got := readSystemString(t, body); got != want {
					t.Fatalf("system = %q, want %q", got, want)
				}
			},
		},
		{
			name:        "已带前缀的字符串保持原样",
			input:       []byte(`{"system":"你正在通过 HUAKAI 网关访问。\n\nFoo","messages":[]}`),
			plan:        SystemRewritePlan{PrefixText: testRewritePrefix},
			wantApplied: false,
			wantReason:  reasonAlreadyPrefixed,
			assertBody: func(t *testing.T, body []byte) {
				if !bytes.Equal(body, []byte(`{"system":"你正在通过 HUAKAI 网关访问。\n\nFoo","messages":[]}`)) {
					t.Fatalf("body 已变动: %s", body)
				}
			},
		},
		{
			name:        "数组形态首块前置 prefix",
			input:       []byte(`{"system":[{"type":"text","text":"Foo"}]}`),
			plan:        SystemRewritePlan{PrefixText: testRewritePrefix},
			wantApplied: true,
			wantReason:  reasonRewroteArray,
			assertBody: func(t *testing.T, body []byte) {
				blocks := readSystemBlocks(t, body)
				if got := blockText(t, blocks[0]); got != testRewritePrefix {
					t.Fatalf("blocks[0].text = %q", got)
				}
				if got := blockText(t, blocks[1]); got != "Foo" {
					t.Fatalf("blocks[1].text = %q", got)
				}
			},
		},
		{
			name:        "数组首块已是 prefix 保持原样",
			input:       []byte(`{"system":[{"type":"text","text":"你正在通过 HUAKAI 网关访问。"},{"type":"text","text":"Foo"}]}`),
			plan:        SystemRewritePlan{PrefixText: testRewritePrefix},
			wantApplied: false,
			wantReason:  reasonAlreadyPrefixed,
		},
		{
			name:        "数组前置 prefix 时保留原首块的 cache_control",
			input:       []byte(`{"system":[{"type":"text","text":"Foo","cache_control":{"type":"ephemeral","ttl":"1h"}}]}`),
			plan:        SystemRewritePlan{PrefixText: testRewritePrefix},
			wantApplied: true,
			wantReason:  reasonRewroteArray,
			assertBody: func(t *testing.T, body []byte) {
				blocks := readSystemBlocks(t, body)
				if got := blockText(t, blocks[0]); got != testRewritePrefix {
					t.Fatalf("blocks[0].text = %q", got)
				}
				// 注入的 prefix 块自身不带 cache_control，由 R7.4 在后续步骤决定。
				var head map[string]json.RawMessage
				if err := json.Unmarshal(blocks[0], &head); err != nil {
					t.Fatal(err)
				}
				if _, has := head["cache_control"]; has {
					t.Fatalf("注入块不应带 cache_control: %s", blocks[0])
				}
				// 原首块的 cache_control 必须落在 idx=1。
				var prev map[string]json.RawMessage
				if err := json.Unmarshal(blocks[1], &prev); err != nil {
					t.Fatal(err)
				}
				if _, has := prev["cache_control"]; !has {
					t.Fatalf("原 cache_control 丢失: %s", blocks[1])
				}
			},
		},
		{
			name:        "数字形态视为不支持",
			input:       []byte(`{"system":3}`),
			plan:        SystemRewritePlan{PrefixText: testRewritePrefix},
			wantApplied: false,
			wantReason:  reasonUnsupported,
			assertBody: func(t *testing.T, body []byte) {
				if !bytes.Equal(body, []byte(`{"system":3}`)) {
					t.Fatalf("body 已变动: %s", body)
				}
			},
		},
		{
			name:        "对象形态视为不支持",
			input:       []byte(`{"system":{"type":"text","text":"Foo"}}`),
			plan:        SystemRewritePlan{PrefixText: testRewritePrefix},
			wantApplied: false,
			wantReason:  reasonUnsupported,
		},
		{
			name:        "ReplaceAll 覆写所有原内容",
			input:       []byte(`{"system":[{"type":"text","text":"Foo"}]}`),
			plan:        SystemRewritePlan{PrefixText: testRewritePrefix, Mode: SystemRewriteReplaceAll},
			wantApplied: true,
			wantReason:  reasonReplacedAll,
			assertBody: func(t *testing.T, body []byte) {
				if got := readSystemString(t, body); got != testRewritePrefix {
					t.Fatalf("system = %q", got)
				}
			},
		},
		{
			name:        "AppendAfter 拼接到字符串末尾",
			input:       []byte(`{"system":"Foo"}`),
			plan:        SystemRewritePlan{PrefixText: testRewritePrefix, Mode: SystemRewriteAppendAfter},
			wantApplied: true,
			wantReason:  reasonAppended,
			assertBody: func(t *testing.T, body []byte) {
				want := "Foo\n\n" + testRewritePrefix
				if got := readSystemString(t, body); got != want {
					t.Fatalf("system = %q, want %q", got, want)
				}
			},
		},
		{
			name:        "AppendAfter 给数组追加文本块",
			input:       []byte(`{"system":[{"type":"text","text":"Foo"}]}`),
			plan:        SystemRewritePlan{PrefixText: testRewritePrefix, Mode: SystemRewriteAppendAfter},
			wantApplied: true,
			wantReason:  reasonAppended,
			assertBody: func(t *testing.T, body []byte) {
				blocks := readSystemBlocks(t, body)
				if got := blockText(t, blocks[len(blocks)-1]); got != testRewritePrefix {
					t.Fatalf("末块 = %q", got)
				}
			},
		},
		{
			name:        "PrefixText 为空时整体 no-op",
			input:       []byte(`{"system":"Foo"}`),
			plan:        SystemRewritePlan{},
			wantApplied: false,
			wantReason:  reasonEmptyPrefix,
			assertBody: func(t *testing.T, body []byte) {
				if !bytes.Equal(body, []byte(`{"system":"Foo"}`)) {
					t.Fatalf("body 已变动: %s", body)
				}
			},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			got, err := RewriteSystem(tt.input, tt.plan)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err=%v, wantErr=%v", err, tt.wantErr)
			}
			if got.Applied != tt.wantApplied {
				t.Fatalf("Applied=%v, want=%v", got.Applied, tt.wantApplied)
			}
			if got.Reason != tt.wantReason {
				t.Fatalf("Reason=%q, want=%q", got.Reason, tt.wantReason)
			}
			if tt.assertBody != nil {
				tt.assertBody(t, got.Body)
			}
		})
	}
}

// TestRewriteSystem_PreservesOtherFields 验证除 system 之外的字段（model /
// max_tokens / messages 等）不被丢弃或覆写。
func TestRewriteSystem_PreservesOtherFields(t *testing.T) {
	in := []byte(`{"model":"claude-opus-4-5","max_tokens":1024,"system":"hello","messages":[{"role":"user","content":"hi"}]}`)
	got, err := RewriteSystem(in, SystemRewritePlan{PrefixText: testRewritePrefix})
	if err != nil {
		t.Fatal(err)
	}
	if !got.Applied {
		t.Fatal("expected Applied=true")
	}
	var out map[string]json.RawMessage
	if err := json.Unmarshal(got.Body, &out); err != nil {
		t.Fatalf("结果 body 不合法 JSON: %v", err)
	}
	for _, k := range []string{"model", "max_tokens", "messages"} {
		if _, ok := out[k]; !ok {
			t.Errorf("字段 %q 丢失", k)
		}
	}
}

// TestRewriteSystem_Idempotent 验证 EnsurePrefix 模式连跑两次的幂等。
func TestRewriteSystem_Idempotent(t *testing.T) {
	in := []byte(`{"system":"original text","messages":[]}`)
	plan := SystemRewritePlan{PrefixText: testRewritePrefix}

	r1, err := RewriteSystem(in, plan)
	if err != nil || !r1.Applied {
		t.Fatalf("第一次：err=%v applied=%v", err, r1.Applied)
	}
	r2, err := RewriteSystem(r1.Body, plan)
	if err != nil {
		t.Fatal(err)
	}
	if r2.Applied {
		t.Fatalf("第二次应当 no-op：reason=%s", r2.Reason)
	}
	if r2.Reason != reasonAlreadyPrefixed {
		t.Errorf("第二次 reason=%q, want already_prefixed", r2.Reason)
	}
}

// TestRewriteSystem_PreservesUnknownBlockFields 验证数组中未知字段（如
// citations / cache_creation 等）不被丢失。
func TestRewriteSystem_PreservesUnknownBlockFields(t *testing.T) {
	in := []byte(`{"system":[{"type":"text","text":"Foo","unknown_field":"keep_me"}]}`)
	got, err := RewriteSystem(in, SystemRewritePlan{PrefixText: testRewritePrefix})
	if err != nil || !got.Applied {
		t.Fatalf("err=%v applied=%v", err, got.Applied)
	}
	if !strings.Contains(string(got.Body), `"unknown_field":"keep_me"`) {
		t.Errorf("未知字段 unknown_field 丢失：%s", got.Body)
	}
}

// --- 测试辅助 ---------------------------------------------------------------

func readSystemString(t *testing.T, body []byte) string {
	t.Helper()
	var root struct {
		System string `json:"system"`
	}
	if err := json.Unmarshal(body, &root); err != nil {
		t.Fatalf("readSystemString: %v", err)
	}
	return root.System
}

func readSystemBlocks(t *testing.T, body []byte) []json.RawMessage {
	t.Helper()
	var root struct {
		System []json.RawMessage `json:"system"`
	}
	if err := json.Unmarshal(body, &root); err != nil {
		t.Fatalf("readSystemBlocks: %v", err)
	}
	return root.System
}

func blockText(t *testing.T, raw json.RawMessage) string {
	t.Helper()
	var b struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &b); err != nil {
		t.Fatal(err)
	}
	return b.Text
}
