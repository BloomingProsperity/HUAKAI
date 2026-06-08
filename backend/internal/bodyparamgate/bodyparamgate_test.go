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
	// MUTATION: making StripBodyParams a no-op leaves service_tier present.
	// GUARD: nil strip list must preserve the original body byte-for-byte.
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
	// MUTATION: making ApplyParamOverride a no-op leaves temperature at 0.9.
	// GUARD: nil override must preserve the original body byte-for-byte.
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
	// MUTATION: deleting the whole stream_options object removes include_usage.
}

func decodeTestObject(t *testing.T, body []byte) map[string]any {
	t.Helper()
	var obj map[string]any
	if err := json.Unmarshal(body, &obj); err != nil {
		t.Fatalf("decode object: %v body=%s", err, body)
	}
	return obj
}
