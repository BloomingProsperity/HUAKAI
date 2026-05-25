// R7.2 mutator tests.
// Lane: implementer-claude (CLAUDE.md #10 + 2026-05-04).
package gateway

import (
	"encoding/json"
	"strings"
	"testing"
)

// ---- helpers ----

// mustMarshal encodes v to JSON and fails the test on error.
func mustMarshal(t *testing.T, v interface{}) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("mustMarshal: %v", err)
	}
	return b
}

// bodyHasKey returns true if the raw JSON object at keyPath contains key.
// keyPath uses simple dot notation for nested objects (not used here, but
// the helper keeps things readable).
func bodyHasKey(body []byte, key string) bool {
	return strings.Contains(string(body), `"`+key+`"`)
}

// ---- Test 1: empty plan → byte semantics ----

// TestApplyBreakpoints_EmptyPlan verifies that an empty plan produces a body
// that round-trips to the same logical content (same InspectCacheControl
// result) even if the serialization differs in key order.
func TestApplyBreakpoints_EmptyPlan(t *testing.T) {
	body := []byte(`{
		"model": "claude-3-5-sonnet",
		"max_tokens": 64,
		"messages": [{"role": "user", "content": "hello"}]
	}`)

	before, err := InspectCacheControl(body)
	if err != nil {
		t.Fatal(err)
	}

	result, err := ApplyBreakpoints(body, BreakpointSuggestion{})
	if err != nil {
		t.Fatal(err)
	}

	after, err := InspectCacheControl(result.Body)
	if err != nil {
		t.Fatal(err)
	}

	if before.Count != after.Count {
		t.Fatalf("Count changed from %d to %d on empty plan", before.Count, after.Count)
	}
	if len(result.Applied) != 0 {
		t.Fatalf("Applied = %d; want 0", len(result.Applied))
	}
	if len(result.Skipped) != 0 {
		t.Fatalf("Skipped = %d; want 0", len(result.Skipped))
	}
}

// ---- Test 2: 1 system breakpoint → InspectCacheControl recognises it ----

func TestApplyBreakpoints_OneSystemBreakpoint(t *testing.T) {
	body := []byte(`{
		"messages": [{"role": "user", "content": "hi"}],
		"system": [{"type": "text", "text": "policy"}]
	}`)

	before, err := InspectCacheControl(body)
	if err != nil {
		t.Fatal(err)
	}
	if before.Count != 0 {
		t.Fatalf("precondition: expected 0 breakpoints, got %d", before.Count)
	}

	plan := BreakpointSuggestion{
		Add: []CacheControlLocation{
			{Path: "system", Index: 0, Type: "ephemeral", TTL: ""},
		},
	}
	result, err := ApplyBreakpoints(body, plan)
	if err != nil {
		t.Fatal(err)
	}

	after, err := InspectCacheControl(result.Body)
	if err != nil {
		t.Fatal(err)
	}

	if after.Count != before.Count+1 {
		t.Fatalf("Count = %d, want %d", after.Count, before.Count+1)
	}
	if len(result.Applied) != 1 {
		t.Fatalf("Applied = %d; want 1", len(result.Applied))
	}
	if result.Applied[0].Path != "system" || result.Applied[0].Index != 0 {
		t.Fatalf("Applied[0] = %+v; want system[0]", result.Applied[0])
	}
}

// ---- Test 3: max 4 cap → final count == 4 ----

func TestApplyBreakpoints_MaxFourCap(t *testing.T) {
	// Body starts with 2 breakpoints; plan adds 4 more → only 2 can apply.
	body := []byte(`{
		"system": [
			{"type": "text", "text": "s0", "cache_control": {"type": "ephemeral"}},
			{"type": "text", "text": "s1", "cache_control": {"type": "ephemeral"}}
		],
		"messages": [{"role": "user", "content": [
			{"type": "text", "text": "m0"},
			{"type": "text", "text": "m1"}
		]}],
		"tools": [
			{"name": "t0", "description": "x", "input_schema": {"type": "object"}},
			{"name": "t1", "description": "y", "input_schema": {"type": "object"}}
		]
	}`)

	before, err := InspectCacheControl(body)
	if err != nil {
		t.Fatal(err)
	}
	if before.Count != 2 {
		t.Fatalf("precondition: expected 2 breakpoints, got %d", before.Count)
	}

	plan := BreakpointSuggestion{
		Add: []CacheControlLocation{
			{Path: "tools", Index: 0, Type: "ephemeral"},
			{Path: "tools", Index: 1, Type: "ephemeral"},
			{Path: "messages", Index: 0, Type: "ephemeral"},
			// This 4th would push to 6 total — only first 2 of these 4 can apply.
			{Path: "system", Index: 0, Type: "ephemeral"}, // already has CC → skipped differently
		},
	}
	result, err := ApplyBreakpoints(body, plan)
	if err != nil {
		t.Fatal(err)
	}

	after, err := InspectCacheControl(result.Body)
	if err != nil {
		t.Fatal(err)
	}

	if after.Count != 4 {
		t.Fatalf("Count = %d; want 4 (cap)", after.Count)
	}
}

// ---- Test 4: TTL="1h" → JSON contains "ttl":"1h" ----

func TestApplyBreakpoints_TTLOneHour(t *testing.T) {
	body := []byte(`{
		"messages": [{"role": "user", "content": [
			{"type": "text", "text": "long context"}
		]}]
	}`)

	plan := BreakpointSuggestion{
		Add: []CacheControlLocation{
			{Path: "messages", Index: 0, Type: "ephemeral", TTL: "1h"},
		},
	}
	result, err := ApplyBreakpoints(body, plan)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Applied) != 1 {
		t.Fatalf("Applied = %d; want 1", len(result.Applied))
	}

	// Raw JSON must contain the ttl key with value "1h".
	if !strings.Contains(string(result.Body), `"ttl":"1h"`) &&
		!strings.Contains(string(result.Body), `"ttl": "1h"`) {
		t.Fatalf("result body does not contain ttl:1h; got: %s", result.Body)
	}

	// Inspect must parse TTL correctly.
	after, err := InspectCacheControl(result.Body)
	if err != nil {
		t.Fatal(err)
	}
	if after.Count != 1 {
		t.Fatalf("Count = %d; want 1", after.Count)
	}
	if after.Locations[0].TTL != "1h" {
		t.Fatalf("TTL = %q; want \"1h\"", after.Locations[0].TTL)
	}
}

// ---- Test 5: already-occupied → Skipped + body unchanged count ----

func TestApplyBreakpoints_AlreadyOccupied(t *testing.T) {
	body := []byte(`{
		"messages": [{"role": "user", "content": [
			{"type": "text", "text": "x", "cache_control": {"type": "ephemeral"}}
		]}]
	}`)

	before, err := InspectCacheControl(body)
	if err != nil {
		t.Fatal(err)
	}

	plan := BreakpointSuggestion{
		Add: []CacheControlLocation{
			{Path: "messages", Index: 0, Type: "ephemeral"},
		},
	}
	result, err := ApplyBreakpoints(body, plan)
	if err != nil {
		t.Fatal(err)
	}

	if len(result.Applied) != 0 {
		t.Fatalf("Applied = %d; want 0 (already occupied)", len(result.Applied))
	}
	if len(result.Skipped) != 1 {
		t.Fatalf("Skipped = %d; want 1", len(result.Skipped))
	}
	if result.Skipped[0].Reason != skipReasonAlreadyHas {
		t.Fatalf("Skipped reason = %q; want %q", result.Skipped[0].Reason, skipReasonAlreadyHas)
	}

	after, err := InspectCacheControl(result.Body)
	if err != nil {
		t.Fatal(err)
	}
	if after.Count != before.Count {
		t.Fatalf("Count changed from %d to %d; should be unchanged", before.Count, after.Count)
	}
}

// ---- Test 6: path/index not found → Skipped "location not found" ----

func TestApplyBreakpoints_LocationNotFound(t *testing.T) {
	body := []byte(`{
		"messages": [{"role": "user", "content": "hi"}]
	}`)

	plan := BreakpointSuggestion{
		Add: []CacheControlLocation{
			{Path: "tools", Index: 99, Type: "ephemeral"},       // tools absent
			{Path: "system", Index: 5, Type: "ephemeral"},       // system absent
			{Path: "messages", Index: 99, Type: "ephemeral"},    // index OOB
		},
	}
	result, err := ApplyBreakpoints(body, plan)
	if err != nil {
		t.Fatal(err)
	}

	if len(result.Applied) != 0 {
		t.Fatalf("Applied = %d; want 0", len(result.Applied))
	}
	if len(result.Skipped) != 3 {
		t.Fatalf("Skipped = %d; want 3", len(result.Skipped))
	}
	for _, s := range result.Skipped {
		if s.Reason != skipReasonNotFound {
			t.Fatalf("Skipped reason = %q; want %q", s.Reason, skipReasonNotFound)
		}
	}
}

// ---- Test 7: plan.Add > 4 → first fill Applied, excess Skipped "would exceed cap" ----

func TestApplyBreakpoints_PlanExceedsCap(t *testing.T) {
	// 5 distinct locations; only 4 should be Applied (cap=4, start count=0).
	body := []byte(`{
		"system": [
			{"type": "text", "text": "s0"},
			{"type": "text", "text": "s1"}
		],
		"messages": [
			{"role": "user", "content": [{"type": "text", "text": "m0"}]},
			{"role": "user", "content": [{"type": "text", "text": "m1"}]}
		],
		"tools": [
			{"name": "t0", "description": "x", "input_schema": {"type": "object"}}
		]
	}`)

	plan := BreakpointSuggestion{
		Add: []CacheControlLocation{
			{Path: "system", Index: 0, Type: "ephemeral"},
			{Path: "system", Index: 1, Type: "ephemeral"},
			{Path: "messages", Index: 0, Type: "ephemeral"},
			{Path: "messages", Index: 1, Type: "ephemeral"},
			{Path: "tools", Index: 0, Type: "ephemeral"}, // 5th → would exceed cap
		},
	}
	result, err := ApplyBreakpoints(body, plan)
	if err != nil {
		t.Fatal(err)
	}

	if len(result.Applied) != 4 {
		t.Fatalf("Applied = %d; want 4", len(result.Applied))
	}
	if len(result.Skipped) != 1 {
		t.Fatalf("Skipped = %d; want 1", len(result.Skipped))
	}
	if result.Skipped[0].Reason != skipReasonExceedsCap {
		t.Fatalf("Skipped reason = %q; want %q", result.Skipped[0].Reason, skipReasonExceedsCap)
	}

	after, err := InspectCacheControl(result.Body)
	if err != nil {
		t.Fatal(err)
	}
	if after.Count != 4 {
		t.Fatalf("after.Count = %d; want 4", after.Count)
	}
}

// ---- Test 8: round-trip — Inspect → Suggest → Apply → Inspect count += len(Applied) ----

func TestApplyBreakpoints_RoundTrip(t *testing.T) {
	body := []byte(`{
		"model": "claude-sonnet-4-6",
		"max_tokens": 256,
		"system": [{"type": "text", "text": "You are helpful."}],
		"messages": [
			{"role": "user", "content": [{"type": "text", "text": "What is Go?"}]},
			{"role": "assistant", "content": "Go is a language."},
			{"role": "user", "content": [{"type": "text", "text": "Tell me more."}]}
		],
		"tools": [
			{"name": "search", "description": "web search", "input_schema": {"type": "object"}}
		]
	}`)

	before, err := InspectCacheControl(body)
	if err != nil {
		t.Fatal(err)
	}

	suggestion, err := SuggestBreakpoints(body, before, nil)
	if err != nil {
		t.Fatal(err)
	}

	if len(suggestion.Add) == 0 {
		t.Skip("no candidates suggested; round-trip test not applicable")
	}

	result, err := ApplyBreakpoints(body, suggestion)
	if err != nil {
		t.Fatal(err)
	}

	after, err := InspectCacheControl(result.Body)
	if err != nil {
		t.Fatal(err)
	}

	want := before.Count + len(result.Applied)
	if after.Count != want {
		t.Fatalf("after.Count = %d; want before.Count(%d) + Applied(%d) = %d",
			after.Count, before.Count, len(result.Applied), want)
	}
}

// ---- Test 9: invalid JSON → error ----

func TestApplyBreakpoints_InvalidJSON(t *testing.T) {
	_, err := ApplyBreakpoints([]byte(`{not valid json`), BreakpointSuggestion{})
	if err == nil {
		t.Fatal("expected error for invalid JSON; got nil")
	}
}

// ---- Test 10: TTL ordering — WithTTLOrdering reorders correctly ----

func TestApplyBreakpointsWithTTLOrdering_Reorders(t *testing.T) {
	// Plan has short-TTL first, long-TTL second (wrong order).
	// WithTTLOrdering should reorder to long-TTL first.
	body := []byte(`{
		"system": [
			{"type": "text", "text": "s0"},
			{"type": "text", "text": "s1"}
		],
		"messages": [{"role": "user", "content": [{"type": "text", "text": "hi"}]}]
	}`)

	// Deliberately out-of-order: short TTL first, then long TTL.
	plan := BreakpointSuggestion{
		Add: []CacheControlLocation{
			{Path: "messages", Index: 0, Type: "ephemeral", TTL: ""},   // short
			{Path: "system", Index: 1, Type: "ephemeral", TTL: "1h"},   // long
		},
	}

	result, err := ApplyBreakpointsWithTTLOrdering(body, plan)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result.Applied) != 2 {
		t.Fatalf("Applied = %d; want 2", len(result.Applied))
	}

	// The first applied location should be the long-TTL ("1h") one.
	if result.Applied[0].TTL != "1h" {
		t.Fatalf("Applied[0].TTL = %q; want \"1h\" (should be applied first after reorder)",
			result.Applied[0].TTL)
	}
	if result.Applied[1].TTL != "" {
		t.Fatalf("Applied[1].TTL = %q; want \"\" (short TTL applied second)",
			result.Applied[1].TTL)
	}

	// Final body must pass TTL ordering validation.
	after, err := InspectCacheControl(result.Body)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateTTLOrdering(after); err != nil {
		t.Fatalf("TTL ordering violated in result: %v", err)
	}
}

// ---- Bonus: TTL="" → no ttl key in JSON ----

func TestApplyBreakpoints_TTLDefaultNoKey(t *testing.T) {
	body := []byte(`{
		"messages": [{"role": "user", "content": [
			{"type": "text", "text": "content"}
		]}]
	}`)

	plan := BreakpointSuggestion{
		Add: []CacheControlLocation{
			{Path: "messages", Index: 0, Type: "ephemeral", TTL: ""},
		},
	}
	result, err := ApplyBreakpoints(body, plan)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Applied) != 1 {
		t.Fatalf("Applied = %d; want 1", len(result.Applied))
	}

	// Must NOT contain "ttl" key.
	if strings.Contains(string(result.Body), `"ttl"`) {
		t.Fatalf("body should not contain ttl key for default TTL; got: %s", result.Body)
	}
}

// ---- Bonus: ApplyBreakpoints does not mutate input body ----

func TestApplyBreakpoints_DoesNotMutateInput(t *testing.T) {
	body := []byte(`{"messages":[{"role":"user","content":[{"type":"text","text":"hi"}]}]}`)
	original := make([]byte, len(body))
	copy(original, body)

	plan := BreakpointSuggestion{
		Add: []CacheControlLocation{
			{Path: "messages", Index: 0, Type: "ephemeral"},
		},
	}
	_, err := ApplyBreakpoints(body, plan)
	if err != nil {
		t.Fatal(err)
	}

	// body slice must be unchanged.
	if string(body) != string(original) {
		t.Fatalf("input body was mutated:\nbefore: %s\nafter:  %s", original, body)
	}
}
