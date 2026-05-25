package redact

import (
	"reflect"
	"testing"
)

func TestRedact_AllowedFieldsKept(t *testing.T) {
	in := map[string]any{
		"request_id":     "req_001",
		"tenant_id":      int64(42),
		"status_code":    200,
		"latency_ms_total": 150,
	}
	out := Redact(in)
	if !reflect.DeepEqual(out, in) {
		t.Errorf("all allowed fields should pass through; got %+v", out)
	}
}

func TestRedact_ForbiddenFieldsDropped(t *testing.T) {
	in := map[string]any{
		"request_id": "req_002",
		"prompt":     "secret user prompt content",
		"completion": "secret assistant output",
		"messages":   []string{"hello"},
		"api_key":    "sk-very-secret",
	}
	out := Redact(in)
	if _, ok := out["prompt"]; ok {
		t.Error("prompt must be redacted")
	}
	if _, ok := out["completion"]; ok {
		t.Error("completion must be redacted")
	}
	if _, ok := out["messages"]; ok {
		t.Error("messages must be redacted")
	}
	if _, ok := out["api_key"]; ok {
		t.Error("api_key must be redacted")
	}
	if out["request_id"] != "req_002" {
		t.Errorf("request_id should remain; got %v", out["request_id"])
	}
}

func TestRedact_NilEntry(t *testing.T) {
	if Redact(nil) != nil {
		t.Error("Redact(nil) must return nil")
	}
}

func TestRedact_EmptyEntry(t *testing.T) {
	out := Redact(map[string]any{})
	if len(out) != 0 {
		t.Errorf("empty entry should give empty output, got %+v", out)
	}
}

func TestDroppedFields_SortedListOfForbidden(t *testing.T) {
	in := map[string]any{
		"request_id": "x",
		"prompt":     "p",
		"api_key":    "k",
		"messages":   "m",
	}
	got := DroppedFields(in)
	want := []string{"api_key", "messages", "prompt"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("DroppedFields got=%v want=%v", got, want)
	}
}

func TestDroppedFields_AllSafeReturnsEmpty(t *testing.T) {
	in := map[string]any{"request_id": "x", "status_code": 200}
	got := DroppedFields(in)
	if len(got) != 0 {
		t.Errorf("expected no dropped, got %v", got)
	}
}

func TestDroppedFields_NilSafe(t *testing.T) {
	if DroppedFields(nil) != nil {
		t.Error("DroppedFields(nil) must return nil")
	}
}
