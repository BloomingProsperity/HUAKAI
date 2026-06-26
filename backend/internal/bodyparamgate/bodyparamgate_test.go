package bodyparamgate

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestStripBodyParams(t *testing.T) {
	body := []byte(`{"model":"x","service_tier":"flex","messages":[{"role":"user","content":"hi"}]}`)

	passthrough, err := StripBodyParams(body, nil)
	if err != nil {
		t.Fatalf("empty strips: %v", err)
	}
	if !bytes.Equal(passthrough, body) {
		t.Fatalf("empty strips changed bytes: got=%s want=%s", passthrough, body)
	}

	stripped, err := StripBodyParams(body, []string{"service_tier"})
	if err != nil {
		t.Fatalf("strip service_tier: %v", err)
	}
	obj := decodeTestObject(t, stripped)
	if _, ok := obj["service_tier"]; ok {
		t.Fatalf("service_tier still present after strip: %s", stripped)
	}
	if obj["model"] != "x" {
		t.Fatalf("model not preserved: got=%v body=%s", obj["model"], stripped)
	}
	if _, ok := obj["messages"].([]any); !ok {
		t.Fatalf("messages not preserved as array: %s", stripped)
	}
	// 变异:把 StripBodyParams 改成空操作会导致 service_tier 仍然存在。
	// 守卫:nil strip 列表必须逐字节保留原始 body。
}

func TestApplyParamOverride(t *testing.T) {
	body := []byte(`{"model":"x","temperature":0.9,"messages":[{"role":"user","content":"hi"}]}`)

	passthrough, err := ApplyParamOverride(body, nil)
	if err != nil {
		t.Fatalf("empty override: %v", err)
	}
	if !bytes.Equal(passthrough, body) {
		t.Fatalf("empty override changed bytes: got=%s want=%s", passthrough, body)
	}

	overridden, err := ApplyParamOverride(body, map[string]json.RawMessage{
		"temperature": json.RawMessage(`0`),
	})
	if err != nil {
		t.Fatalf("override temperature: %v", err)
	}
	obj := decodeTestObject(t, overridden)
	if obj["temperature"] != float64(0) {
		t.Fatalf("temperature=%v want 0 body=%s", obj["temperature"], overridden)
	}
	if obj["model"] != "x" {
		t.Fatalf("model not preserved: got=%v body=%s", obj["model"], overridden)
	}
	if _, ok := obj["messages"].([]any); !ok {
		t.Fatalf("messages not preserved as array: %s", overridden)
	}
	// 变异:把 ApplyParamOverride 改成空操作会导致 temperature 仍是 0.9。
	// 守卫:nil override 必须逐字节保留原始 body。
}

func TestNestedStrip(t *testing.T) {
	body := []byte(`{"model":"x","stream_options":{"include_obfuscation":true,"include_usage":true},"messages":[{"role":"user","content":"hi"}]}`)

	stripped, err := StripBodyParams(body, []string{"stream_options.include_obfuscation"})
	if err != nil {
		t.Fatalf("strip nested include_obfuscation: %v", err)
	}
	obj := decodeTestObject(t, stripped)
	streamOptions, ok := obj["stream_options"].(map[string]any)
	if !ok {
		t.Fatalf("stream_options missing or non-object: %s", stripped)
	}
	if _, ok := streamOptions["include_obfuscation"]; ok {
		t.Fatalf("include_obfuscation still present: %s", stripped)
	}
	if _, ok := streamOptions["include_usage"]; !ok {
		t.Fatalf("include_usage sibling stripped: %s", stripped)
	}
	// 变异:删除整个 stream_options 对象会把 include_usage 也一并移除。
}

func decodeTestObject(t *testing.T, body []byte) map[string]any {
	t.Helper()
	var obj map[string]any
	if err := json.Unmarshal(body, &obj); err != nil {
		t.Fatalf("decode object: %v body=%s", err, body)
	}
	return obj
}
