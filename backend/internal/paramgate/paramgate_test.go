package paramgate

import (
	"encoding/json"
	"testing"
)

func TestParamGateOptIn(t *testing.T) {
	body := []byte(`{
		"model":"gpt-4o",
		"service_tier":"priority",
		"inference_geo":"eu",
		"speed":"fast",
		"safety_identifier":"safe-1",
		"stream_options":{"include_obfuscation":true,"include_usage":true},
		"store":true
	}`)

	passthrough, err := StripGatedFields(body, GateConfig{})
	if err != nil {
		t.Fatalf("all-false config: %v", err)
	}
	defaultObj := decodeObject(t, passthrough)
	for _, key := range []string{"service_tier", "inference_geo", "speed", "safety_identifier", "stream_options", "store"} {
		if _, ok := defaultObj[key]; !ok {
			t.Fatalf("all-false config stripped %q; body=%s", key, passthrough)
		}
	}

	stripped, err := StripGatedFields(body, GateConfig{StripServiceTier: true})
	if err != nil {
		t.Fatalf("StripServiceTier: %v", err)
	}
	strippedObj := decodeObject(t, stripped)
	if _, ok := strippedObj["service_tier"]; ok {
		t.Fatalf("service_tier still present after opt-in strip: %s", stripped)
	}
	if _, ok := strippedObj["inference_geo"]; !ok {
		t.Fatalf("unenabled field inference_geo was stripped: %s", stripped)
	}

	nested, err := StripGatedFields(body, GateConfig{StripStreamOptionsIncludeObfuscation: true})
	if err != nil {
		t.Fatalf("StripStreamOptionsIncludeObfuscation: %v", err)
	}
	nestedObj := decodeObject(t, nested)
	streamOptions, ok := nestedObj["stream_options"].(map[string]any)
	if !ok {
		t.Fatalf("stream_options missing or non-object: %s", nested)
	}
	if _, ok := streamOptions["include_obfuscation"]; ok {
		t.Fatalf("include_obfuscation still present after opt-in strip: %s", nested)
	}
	if _, ok := streamOptions["include_usage"]; !ok {
		t.Fatalf("sibling stream_options.include_usage was stripped: %s", nested)
	}
	// 变异:不检查标志就默认剥除,会让 all-false 的保留断言失败。
}

func decodeObject(t *testing.T, body []byte) map[string]any {
	t.Helper()
	var obj map[string]any
	if err := json.Unmarshal(body, &obj); err != nil {
		t.Fatalf("decode object: %v; body=%s", err, body)
	}
	return obj
}
