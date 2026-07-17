package channelrewriteconfig

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"
)

func TestDecodePreservesPresenceAndNormalizesValues(t *testing.T) {
	values, err := Decode(
		json.RawMessage(`[" foo ","stream_options.include_obfuscation"]`),
		json.RawMessage(`{" temperature ":0.25,"metadata":{"source":"test"}}`),
		json.RawMessage(`[" sentinel "]`),
	)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if !values.SetBodyParamStrips || !values.SetParamOverride || !values.SetSensitiveWords {
		t.Fatalf("显式字段未保留 presence: %+v", values)
	}
	if !reflect.DeepEqual(values.BodyParamStrips, []string{"foo", "stream_options.include_obfuscation"}) ||
		!reflect.DeepEqual(values.SensitiveWords, []string{"sentinel"}) {
		t.Fatalf("字符串数组规范化错误: %+v", values)
	}
	var override map[string]any
	if err := json.Unmarshal(values.ParamOverride, &override); err != nil {
		t.Fatalf("解析规范化覆盖对象: %v", err)
	}
	if override["temperature"] != 0.25 {
		t.Fatalf("覆盖对象键值错误: %v", override)
	}
}

func TestDecodeOmittedAndExplicitEmptyAreDistinct(t *testing.T) {
	omitted, err := Decode(nil, nil, nil)
	if err != nil {
		t.Fatalf("Decode omitted: %v", err)
	}
	if omitted.SetBodyParamStrips || omitted.SetParamOverride || omitted.SetSensitiveWords {
		t.Fatalf("省略字段被误标为提交: %+v", omitted)
	}
	if len(omitted.BodyParamStrips) != 0 || string(omitted.ParamOverride) != `{}` || len(omitted.SensitiveWords) != 0 {
		t.Fatalf("create 缺省值错误: %+v", omitted)
	}

	empty, err := Decode(json.RawMessage(`[]`), json.RawMessage(`{}`), json.RawMessage(`[]`))
	if err != nil {
		t.Fatalf("Decode empty: %v", err)
	}
	if !empty.SetBodyParamStrips || !empty.SetParamOverride || !empty.SetSensitiveWords {
		t.Fatalf("显式空值未标为提交: %+v", empty)
	}
}

func TestDecodeRejectsInvalidShapes(t *testing.T) {
	cases := []struct {
		name  string
		field string
		body  json.RawMessage
		over  json.RawMessage
		words json.RawMessage
	}{
		{name: "剥离字段不是数组", field: "body_param_strips", body: json.RawMessage(`{"foo":true}`)},
		{name: "剥离字段元素不是字符串", field: "body_param_strips", body: json.RawMessage(`["foo",7]`)},
		{name: "剥离字段为空白", field: "body_param_strips", body: json.RawMessage(`[" "]`)},
		{name: "剥离字段为 null", field: "body_param_strips", body: json.RawMessage(`null`)},
		{name: "覆盖对象是数组", field: "param_override", over: json.RawMessage(`[]`)},
		{name: "覆盖对象是标量", field: "param_override", over: json.RawMessage(`"bad"`)},
		{name: "覆盖对象为 null", field: "param_override", over: json.RawMessage(`null`)},
		{name: "覆盖对象键为空", field: "param_override", over: json.RawMessage(`{" ":1}`)},
		{name: "覆盖对象规范化后重名", field: "param_override", over: json.RawMessage(`{"x":1," x ":2}`)},
		{name: "敏感词不是数组", field: "sensitive_words", words: json.RawMessage(`{"foo":true}`)},
		{name: "敏感词元素不是字符串", field: "sensitive_words", words: json.RawMessage(`["foo",false]`)},
		{name: "敏感词为 null", field: "sensitive_words", words: json.RawMessage(`null`)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Decode(tc.body, tc.over, tc.words)
			var validationErr *ValidationError
			if !errors.As(err, &validationErr) || validationErr.Field != tc.field {
				t.Fatalf("err=%v field=%q, want ValidationError field=%q", err, validationErrField(validationErr), tc.field)
			}
		})
	}
}

func validationErrField(err *ValidationError) string {
	if err == nil {
		return ""
	}
	return err.Field
}
