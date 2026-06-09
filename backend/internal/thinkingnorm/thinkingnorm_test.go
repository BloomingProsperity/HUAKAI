package thinkingnorm

import (
	"bytes"
	"encoding/json"
	"testing"
)

func field(t *testing.T, body []byte, key string) interface{} {
	t.Helper()
	var m map[string]interface{}
	if err := json.Unmarshal(body, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return m[key]
}

func TestThinkingForcesTemperatureOne(t *testing.T) {
	in := []byte(`{"model":"claude","thinking":{"type":"enabled"},"temperature":0.5,"messages":[]}`)
	out := NormalizeThinkingValidity(in)
	// MUTATION GUARD: if the temperature rewrite is skipped, temperature stays
	// 0.5 and this assertion goes red.
	if got := field(t, out, "temperature"); got != float64(1) {
		t.Fatalf("temperature=%v want 1", got)
	}
	// other fields preserved
	if got := field(t, out, "model"); got != "claude" {
		t.Fatalf("model dropped: %v", got)
	}
}

func TestThinkingForcesToolChoiceAuto(t *testing.T) {
	in := []byte(`{"thinking":{"type":"enabled"},"temperature":1,"tool_choice":{"type":"any"}}`)
	out := NormalizeThinkingValidity(in)
	tc, _ := field(t, out, "tool_choice").(map[string]interface{})
	if tc["type"] != "auto" {
		t.Fatalf("tool_choice.type=%v want auto", tc["type"])
	}
}

func TestValidThinkingUnchanged(t *testing.T) {
	in := []byte(`{"thinking":{"type":"enabled"},"temperature":1,"tool_choice":{"type":"auto"}}`)
	out := NormalizeThinkingValidity(in)
	// already valid -> must be byte-identical (no needless re-encode / fingerprint churn)
	if !bytes.Equal(in, out) {
		t.Fatalf("valid thinking request must be unchanged; got %s", out)
	}
}

func TestNoThinkingUnchanged(t *testing.T) {
	in := []byte(`{"model":"claude","temperature":0.3,"tool_choice":{"type":"any"}}`)
	out := NormalizeThinkingValidity(in)
	// no thinking field -> constraints do not apply -> byte-identical
	if !bytes.Equal(in, out) {
		t.Fatalf("non-thinking request must be unchanged; got %s", out)
	}
}

func TestDisabledThinkingUnchanged(t *testing.T) {
	in := []byte(`{"thinking":{"type":"disabled"},"temperature":0.5}`)
	out := NormalizeThinkingValidity(in)
	if !bytes.Equal(in, out) {
		t.Fatalf("disabled thinking must be unchanged; got %s", out)
	}
}

func TestMalformedPassthrough(t *testing.T) {
	in := []byte(`{not json`)
	out := NormalizeThinkingValidity(in)
	if !bytes.Equal(in, out) {
		t.Fatalf("malformed body must pass through unchanged")
	}
}
