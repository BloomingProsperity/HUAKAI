// hcsf_passthrough_test.go — U7-B 契约测试：CanonicalRequest /
// CanonicalResponse / CanonicalEvent 各持一个 Passthrough 字段，
// json:"-" 不上 wire（向后兼容既有 JSON shape）。
//
// 这条契约让 adapters 可以在不改 wire format 的前提下携带未识别字段，
// U7-C / U7-D 接入时即填即用。
package proto

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// TestHCSFPassthrough_FieldExistsOnAllThreeTypes 验证 3 个主类型都有
// `Passthrough *PassthroughEnvelope` 字段，json tag 为 "-"。
func TestHCSFPassthrough_FieldExistsOnAllThreeTypes(t *testing.T) {
	cases := []struct {
		name string
		t    reflect.Type
	}{
		{"CanonicalRequest", reflect.TypeOf(CanonicalRequest{})},
		{"CanonicalResponse", reflect.TypeOf(CanonicalResponse{})},
		{"CanonicalEvent", reflect.TypeOf(CanonicalEvent{})},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f, ok := tc.t.FieldByName("Passthrough")
			if !ok {
				t.Fatalf("%s 缺 Passthrough 字段", tc.name)
			}
			if f.Tag.Get("json") != "-" {
				t.Errorf("%s.Passthrough json tag=%q want %q", tc.name, f.Tag.Get("json"), "-")
			}
			if f.Type != reflect.TypeOf((*PassthroughEnvelope)(nil)) {
				t.Errorf("%s.Passthrough 类型=%v want *PassthroughEnvelope", tc.name, f.Type)
			}
		})
	}
}

// TestHCSFPassthrough_NotInWireJSON 校验 json:"-" 真的让 Passthrough
// 不出现在序列化 JSON 中（向后兼容契约）。
func TestHCSFPassthrough_NotInWireJSON(t *testing.T) {
	req := CanonicalRequest{
		Model: "gpt-4o",
		Passthrough: &PassthroughEnvelope{Extra: map[string]json.RawMessage{
			"hidden": json.RawMessage(`"value"`),
		}},
	}
	out, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("Marshal err=%v", err)
	}
	if strings.Contains(string(out), "hidden") || strings.Contains(string(out), "Passthrough") {
		t.Errorf("Passthrough 不应出现在 wire JSON：%s", out)
	}

	evt := CanonicalEvent{
		Type: "message_start",
		Passthrough: &PassthroughEnvelope{Extra: map[string]json.RawMessage{
			"system_fingerprint": json.RawMessage(`"fp_x"`),
		}},
	}
	out2, _ := json.Marshal(evt)
	if strings.Contains(string(out2), "system_fingerprint") || strings.Contains(string(out2), "Passthrough") {
		t.Errorf("Event.Passthrough 不应出现在 wire：%s", out2)
	}
}

// TestHCSFPassthrough_NilPassthroughCompatibleWithExistingDeepEqual 校验
// 既有 reflect.DeepEqual 测试在 Passthrough=nil 时仍按值相等（向后兼容）。
func TestHCSFPassthrough_NilPassthroughCompatibleWithExistingDeepEqual(t *testing.T) {
	a := CanonicalEvent{Type: "x", MessageID: "m1"}
	b := CanonicalEvent{Type: "x", MessageID: "m1"}
	if !reflect.DeepEqual(a, b) {
		t.Error("两个 nil-Passthrough event 应 DeepEqual")
	}
}
