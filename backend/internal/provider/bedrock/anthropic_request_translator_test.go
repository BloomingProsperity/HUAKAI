// anthropic_request_translator_test.go — Bedrock 闭环翻译器测试。
package bedrock

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestTranslate_HappyPath_StripsModelAndStream(t *testing.T) {
	in := []byte(`{
		"model":"claude-3-5-sonnet-20241022",
		"messages":[{"role":"user","content":"hi"}],
		"max_tokens":1024,
		"stream":true
	}`)
	got, err := TranslateAnthropicAPIToBedrock(in)
	if err != nil {
		t.Fatalf("err=%v", err)
	}

	// model + stream 应被剥离
	if strings.Contains(string(got.Body), `"model"`) {
		t.Errorf("body 不应含 model 字段: %s", got.Body)
	}
	if strings.Contains(string(got.Body), `"stream"`) {
		t.Errorf("body 不应含 stream 字段: %s", got.Body)
	}

	// 提取的元数据
	if got.UpstreamModelID != "claude-3-5-sonnet-20241022" {
		t.Errorf("UpstreamModelID=%q", got.UpstreamModelID)
	}
	if !got.Stream {
		t.Errorf("Stream 应 true")
	}

	// anthropic_version 应被注入
	if !strings.Contains(string(got.Body), `"anthropic_version":"bedrock-2023-05-31"`) {
		t.Errorf("body 应含 anthropic_version: %s", got.Body)
	}

	// messages + max_tokens 应保留
	for _, want := range []string{`"messages"`, `"max_tokens"`, `"hi"`, `1024`} {
		if !strings.Contains(string(got.Body), want) {
			t.Errorf("body 缺 %q: %s", want, got.Body)
		}
	}
}

func TestTranslate_RespectsCallerOverrideOfAnthropicVersion(t *testing.T) {
	// caller 已显式声明 → 不被覆盖
	in := []byte(`{"messages":[],"anthropic_version":"vCustomTest","max_tokens":100}`)
	got, err := TranslateAnthropicAPIToBedrock(in)
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	if !strings.Contains(string(got.Body), `"vCustomTest"`) {
		t.Errorf("caller 显式 anthropic_version 应保留: %s", got.Body)
	}
	if strings.Contains(string(got.Body), AnthropicVersionBedrock) {
		t.Errorf("既有 caller 值不应被覆盖: %s", got.Body)
	}
}

func TestTranslate_EmptyBody(t *testing.T) {
	_, err := TranslateAnthropicAPIToBedrock(nil)
	if !errors.Is(err, ErrEmptyAnthropicBody) {
		t.Errorf("err=%v want ErrEmptyAnthropicBody", err)
	}
	_, err = TranslateAnthropicAPIToBedrock([]byte{})
	if !errors.Is(err, ErrEmptyAnthropicBody) {
		t.Errorf("空 byte slice err=%v want ErrEmptyAnthropicBody", err)
	}
}

func TestTranslate_InvalidJSON(t *testing.T) {
	cases := []string{
		`{not json`,
		`[1,2,3]`,             // 数组顶层非 object
		`"just a string"`,     // 标量
		`12345`,               // number
		`null`,                // null
	}
	for _, in := range cases {
		t.Run(in, func(t *testing.T) {
			_, err := TranslateAnthropicAPIToBedrock([]byte(in))
			if !errors.Is(err, ErrInvalidAnthropicJSON) {
				t.Errorf("err=%v want ErrInvalidAnthropicJSON", err)
			}
		})
	}
}

func TestTranslate_PreservesAllOtherFields_U7Consistency(t *testing.T) {
	// vendor 加新字段时透传不丢——与 U7 passthrough field matrix 一致语义
	in := []byte(`{
		"model":"claude-3",
		"messages":[{"role":"user","content":"x"}],
		"max_tokens":512,
		"system":"You are helpful",
		"temperature":0.7,
		"top_p":0.9,
		"top_k":40,
		"stop_sequences":["END"],
		"tools":[{"name":"calc","description":"calculator","input_schema":{}}],
		"vendor_future_field":"some_value",
		"metadata":{"user_id":"u123"}
	}`)
	got, err := TranslateAnthropicAPIToBedrock(in)
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	for _, field := range []string{
		"messages", "max_tokens", "system", "temperature", "top_p", "top_k",
		"stop_sequences", "tools", "vendor_future_field", "metadata",
	} {
		if !strings.Contains(string(got.Body), `"`+field+`"`) {
			t.Errorf("body 应保留 %q: %s", field, got.Body)
		}
	}
	// 不应含被剥离的
	for _, dropped := range []string{`"model"`, `"stream"`} {
		if strings.Contains(string(got.Body), dropped) {
			t.Errorf("body 不应含 %q: %s", dropped, got.Body)
		}
	}
}

func TestTranslate_NoModelField_DoesNotPanic(t *testing.T) {
	// caller 未传 model（合法——caller 可能在 Extra 里另外指定）
	in := []byte(`{"messages":[],"max_tokens":100}`)
	got, err := TranslateAnthropicAPIToBedrock(in)
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	if got.UpstreamModelID != "" {
		t.Errorf("无 model 字段 UpstreamModelID 应空，得 %q", got.UpstreamModelID)
	}
}

func TestTranslate_NoStreamField_DefaultsToFalse(t *testing.T) {
	in := []byte(`{"model":"claude-3","messages":[],"max_tokens":100}`)
	got, err := TranslateAnthropicAPIToBedrock(in)
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	if got.Stream {
		t.Errorf("无 stream 字段应 false")
	}
}

func TestTranslate_OutputIsValidJSON(t *testing.T) {
	in := []byte(`{"model":"x","messages":[{"role":"user","content":"hi"}],"max_tokens":100,"stream":true}`)
	got, err := TranslateAnthropicAPIToBedrock(in)
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	var parsed map[string]any
	if err := json.Unmarshal(got.Body, &parsed); err != nil {
		t.Fatalf("输出非合法 JSON: %v body=%s", err, got.Body)
	}
	if parsed["anthropic_version"] != AnthropicVersionBedrock {
		t.Errorf("anthropic_version=%v", parsed["anthropic_version"])
	}
}

func TestTranslate_NestedStructurePreserved(t *testing.T) {
	in := []byte(`{
		"model":"claude-3",
		"max_tokens":100,
		"messages":[
			{"role":"user","content":[{"type":"text","text":"deep"},{"type":"image","source":{"type":"base64","data":"..."}}]}
		]
	}`)
	got, err := TranslateAnthropicAPIToBedrock(in)
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	// 嵌套结构通过 RawMessage 保持完整
	if !bytes.Contains(got.Body, []byte(`"type":"image"`)) {
		t.Errorf("嵌套 type=image 应保留: %s", got.Body)
	}
	if !bytes.Contains(got.Body, []byte(`"source"`)) {
		t.Errorf("嵌套 source 应保留: %s", got.Body)
	}
}
