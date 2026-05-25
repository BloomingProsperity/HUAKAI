package gateway

import (
	"strings"
	"testing"
)

// ---- helpers (ported from original test file) ----

func ccAssertLocation(t *testing.T, location CacheControlLocation, path string, index int, cacheType string) {
	t.Helper()
	if location.Path != path {
		t.Fatalf("Path = %q, want %q", location.Path, path)
	}
	if location.Index != index {
		t.Fatalf("Index = %d, want %d", location.Index, index)
	}
	if location.Type != cacheType {
		t.Fatalf("Type = %q, want %q", location.Type, cacheType)
	}
}

func ccAssertLocationFull(t *testing.T, location CacheControlLocation, path string, index int, cacheType, ttl string) {
	t.Helper()
	ccAssertLocation(t, location, path, index, cacheType)
	if location.TTL != ttl {
		t.Fatalf("TTL = %q, want %q", location.TTL, ttl)
	}
}

// suggestNoTokens wraps SuggestBreakpoints with nil estimatedBlockTokens for
// backward-compatible calls in tests that predate D6.
func suggestNoTokens(body []byte, snapshot CacheControlSnapshot) (BreakpointSuggestion, error) {
	return SuggestBreakpoints(body, snapshot, nil)
}

// ---- original tests (preserved, updated for new SuggestBreakpoints signature) ----

func TestInspectCacheControl_EmptyBodyError(t *testing.T) {
	if _, err := InspectCacheControl(nil); err == nil {
		t.Fatal("expected error for empty body")
	}
}

func TestInspectCacheControl_InvalidJSONError(t *testing.T) {
	if _, err := InspectCacheControl([]byte(`{not json`)); err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestInspectCacheControl_SchemaError(t *testing.T) {
	if _, err := InspectCacheControl([]byte(`{"messages": "bad"}`)); err == nil {
		t.Fatal("expected schema error for non-array messages")
	}
}

func TestInspectCacheControl_NoCacheControl(t *testing.T) {
	snap, err := InspectCacheControl([]byte(`{
		"model": "claude-3-5-sonnet",
		"max_tokens": 64,
		"messages": [{"role": "user", "content": "hello"}]
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if snap.Count != 0 {
		t.Fatalf("Count = %d, want 0", snap.Count)
	}
	if snap.MaxAllowed != 4 {
		t.Fatalf("MaxAllowed = %d, want 4", snap.MaxAllowed)
	}
}

func TestInspectCacheControl_FindsSystemBlockBreakpoint(t *testing.T) {
	snap, err := InspectCacheControl([]byte(`{
		"messages": [{"role": "user", "content": "hi"}],
		"system": [{"type": "text", "text": "policy", "cache_control": {"type": "ephemeral"}}]
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if snap.Count != 1 {
		t.Fatalf("Count = %d, want 1", snap.Count)
	}
	ccAssertLocation(t, snap.Locations[0], "system", 0, "ephemeral")
}

func TestInspectCacheControl_NestedFindsAllPaths(t *testing.T) {
	snap, err := InspectCacheControl([]byte(`{
		"system": [{"type": "text", "text": "p", "cache_control": {"type": "persistent"}}],
		"messages": [
			{"role": "user", "content": [
				{"type": "text", "text": "a", "cache_control": {"type": "ephemeral"}},
				{"type": "text", "text": "b", "cache_control": {"type": "persistent"}}
			]},
			{"role": "assistant", "content": [{"type": "text", "text": "c"}]}
		],
		"tools": [{
			"name": "lookup", "description": "x", "input_schema": {"type": "object"},
			"cache_control": {"type": "ephemeral"}
		}]
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if snap.Count != 4 {
		t.Fatalf("Count = %d, want 4", snap.Count)
	}
	ccAssertLocation(t, snap.Locations[0], "system", 0, "persistent")
	ccAssertLocation(t, snap.Locations[1], "messages", 0, "ephemeral")
	ccAssertLocation(t, snap.Locations[2], "messages", 0, "persistent")
	ccAssertLocation(t, snap.Locations[3], "tools", 0, "ephemeral")
}

func TestInspectCacheControl_SystemAsString(t *testing.T) {
	snap, err := InspectCacheControl([]byte(`{
		"system": "you are a helpful assistant",
		"messages": [{"role": "user", "content": "hi"}]
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if snap.Count != 0 {
		t.Fatalf("string system has no cache_control; Count = %d", snap.Count)
	}
}

func TestInspectCacheControl_SystemAsObjectWithCC(t *testing.T) {
	snap, err := InspectCacheControl([]byte(`{
		"system": {"type": "text", "text": "p", "cache_control": {"type": "ephemeral"}},
		"messages": [{"role": "user", "content": "hi"}]
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if snap.Count != 1 {
		t.Fatalf("Count = %d, want 1", snap.Count)
	}
	if snap.Locations[0].Index != -1 {
		t.Fatalf("top-level system index want -1; got %d", snap.Locations[0].Index)
	}
}

func TestInspectCacheControl_MissingMessagesError(t *testing.T) {
	_, err := InspectCacheControl([]byte(`{"model": "claude"}`))
	if err == nil {
		t.Fatal("expected error for missing messages")
	}
}

func TestInspectCacheControl_BadCacheControlTypeError(t *testing.T) {
	_, err := InspectCacheControl([]byte(`{
		"messages": [{"role": "user", "content": [
			{"type": "text", "text": "x", "cache_control": "not-an-object"}
		]}]
	}`))
	if err == nil {
		t.Fatal("expected error for non-object cache_control")
	}
}

func TestSuggestBreakpoints_AtMaxSkipsAll(t *testing.T) {
	body := []byte(`{
		"system": [{"type": "text", "text": "a"}, {"type": "text", "text": "b"}],
		"messages": [{"role": "user", "content": "hi"}],
		"tools": [{"name": "t", "description": "x", "input_schema": {"type": "object"}}]
	}`)
	sugg, err := suggestNoTokens(body, CacheControlSnapshot{Count: 4, MaxAllowed: 4})
	if err != nil {
		t.Fatal(err)
	}
	if len(sugg.Add) != 0 {
		t.Fatalf("Add = %d; want 0", len(sugg.Add))
	}
	if len(sugg.Skipped) != 4 {
		t.Fatalf("Skipped = %d; want 4", len(sugg.Skipped))
	}
}

func TestSuggestBreakpoints_AddsUpToFourByPriority(t *testing.T) {
	body := []byte(`{
		"system": [{"type": "text", "text": "1"}, {"type": "text", "text": "last"}],
		"messages": [
			{"role": "user", "content": "older"},
			{"role": "assistant", "content": "a"},
			{"role": "user", "content": "newer"}
		],
		"tools": [
			{"name": "old", "description": "x", "input_schema": {"type": "object"}},
			{"name": "new", "description": "y", "input_schema": {"type": "object"}}
		]
	}`)
	sugg, err := suggestNoTokens(body, CacheControlSnapshot{Count: 0, MaxAllowed: 4})
	if err != nil {
		t.Fatal(err)
	}
	if len(sugg.Add) != 4 {
		t.Fatalf("Add = %d; want 4", len(sugg.Add))
	}
	ccAssertLocation(t, sugg.Add[0], "system", 1, "ephemeral")
	ccAssertLocation(t, sugg.Add[1], "tools", 1, "ephemeral")
	ccAssertLocation(t, sugg.Add[2], "messages", 2, "ephemeral")
	ccAssertLocation(t, sugg.Add[3], "system", 0, "ephemeral")
	if len(sugg.Skipped) != 2 {
		t.Fatalf("Skipped = %d; want 2", len(sugg.Skipped))
	}
}

func TestSuggestBreakpoints_NoSystem(t *testing.T) {
	body := []byte(`{
		"messages": [{"role": "user", "content": "hi"}],
		"tools": [{"name": "t", "description": "x", "input_schema": {"type": "object"}}]
	}`)
	sugg, err := suggestNoTokens(body, CacheControlSnapshot{Count: 0, MaxAllowed: 4})
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range sugg.Add {
		if l.Path == "system" {
			t.Fatalf("unexpected system suggestion: %+v", l)
		}
	}
}

func TestSuggestBreakpoints_NoTools(t *testing.T) {
	body := []byte(`{
		"system": [{"type": "text", "text": "p"}],
		"messages": [{"role": "user", "content": "hi"}]
	}`)
	sugg, err := suggestNoTokens(body, CacheControlSnapshot{Count: 0, MaxAllowed: 4})
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range sugg.Add {
		if l.Path == "tools" {
			t.Fatalf("unexpected tools suggestion: %+v", l)
		}
	}
}

func TestSuggestBreakpoints_OnlyUserMessagesEligible(t *testing.T) {
	body := []byte(`{
		"messages": [
			{"role": "user", "content": "u1"},
			{"role": "assistant", "content": "a1"}
		]
	}`)
	sugg, err := suggestNoTokens(body, CacheControlSnapshot{Count: 0, MaxAllowed: 4})
	if err != nil {
		t.Fatal(err)
	}
	if len(sugg.Add) != 1 || sugg.Add[0].Index != 0 {
		t.Fatalf("expected only user message at index 0; got %+v", sugg.Add)
	}
}

func TestSuggestBreakpoints_DefaultMaxAllowedWhenZero(t *testing.T) {
	body := []byte(`{"messages": [{"role": "user", "content": "hi"}]}`)
	sugg, err := suggestNoTokens(body, CacheControlSnapshot{Count: 0, MaxAllowed: 0})
	if err != nil {
		t.Fatal(err)
	}
	// MaxAllowed=0 should default to CacheControlMaxAllowed (4); 1 candidate added.
	if len(sugg.Add) != 1 {
		t.Fatalf("Add = %d; want 1", len(sugg.Add))
	}
}

// ---- D5 new tests: TTL field ----

// TestCacheControl_TTL_FieldParsed verifies that a cache_control with ttl:"1h"
// is captured in Location.TTL.
func TestCacheControl_TTL_FieldParsed(t *testing.T) {
	snap, err := InspectCacheControl([]byte(`{
		"messages": [{"role": "user", "content": [
			{"type": "text", "text": "long context",
			 "cache_control": {"type": "ephemeral", "ttl": "1h"}}
		]}]
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if snap.Count != 1 {
		t.Fatalf("Count = %d, want 1", snap.Count)
	}
	ccAssertLocationFull(t, snap.Locations[0], "messages", 0, "ephemeral", "1h")
}

// TestCacheControl_TTL_DefaultEmpty verifies that a cache_control without ttl
// leaves Location.TTL as the empty string (5-min default).
func TestCacheControl_TTL_DefaultEmpty(t *testing.T) {
	snap, err := InspectCacheControl([]byte(`{
		"messages": [{"role": "user", "content": [
			{"type": "text", "text": "short context",
			 "cache_control": {"type": "ephemeral"}}
		]}]
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if snap.Count != 1 {
		t.Fatalf("Count = %d, want 1", snap.Count)
	}
	ccAssertLocationFull(t, snap.Locations[0], "messages", 0, "ephemeral", "")
}

// TestCacheControl_ValidateTTLOrdering_OK verifies that long-TTL before
// short-TTL passes validation.
func TestCacheControl_ValidateTTLOrdering_OK(t *testing.T) {
	snap := CacheControlSnapshot{
		Count: 2,
		Locations: []CacheControlLocation{
			{Path: "system", Index: 0, Type: "ephemeral", TTL: "1h"},
			{Path: "messages", Index: 0, Type: "ephemeral", TTL: ""},
		},
		MaxAllowed: 4,
	}
	if err := ValidateTTLOrdering(snap); err != nil {
		t.Fatalf("expected no error for valid ordering; got: %v", err)
	}
}

// TestCacheControl_ValidateTTLOrdering_Violation verifies that short-TTL
// followed by long-TTL returns a descriptive error.
func TestCacheControl_ValidateTTLOrdering_Violation(t *testing.T) {
	snap := CacheControlSnapshot{
		Count: 2,
		Locations: []CacheControlLocation{
			{Path: "system", Index: 0, Type: "ephemeral", TTL: ""},   // short (default)
			{Path: "messages", Index: 0, Type: "ephemeral", TTL: "1h"}, // long — violates ordering
		},
		MaxAllowed: 4,
	}
	err := ValidateTTLOrdering(snap)
	if err == nil {
		t.Fatal("expected ordering violation error; got nil")
	}
	if !strings.Contains(err.Error(), "TTL ordering violation") {
		t.Fatalf("error message should mention TTL ordering violation; got: %v", err)
	}
}

// TestCacheControl_ValidateTTLOrdering_OnlyShort passes with all short TTLs.
func TestCacheControl_ValidateTTLOrdering_OnlyShort(t *testing.T) {
	snap := CacheControlSnapshot{
		Count: 2,
		Locations: []CacheControlLocation{
			{Path: "system", Index: 0, Type: "ephemeral", TTL: ""},
			{Path: "messages", Index: 0, Type: "ephemeral", TTL: ""},
		},
		MaxAllowed: 4,
	}
	if err := ValidateTTLOrdering(snap); err != nil {
		t.Fatalf("all-short TTL should pass; got: %v", err)
	}
}

// TestCacheControl_ValidateTTLOrdering_OnlyLong passes with all long TTLs.
func TestCacheControl_ValidateTTLOrdering_OnlyLong(t *testing.T) {
	snap := CacheControlSnapshot{
		Count: 2,
		Locations: []CacheControlLocation{
			{Path: "system", Index: 0, Type: "ephemeral", TTL: "1h"},
			{Path: "messages", Index: 0, Type: "ephemeral", TTL: "1h"},
		},
		MaxAllowed: 4,
	}
	if err := ValidateTTLOrdering(snap); err != nil {
		t.Fatalf("all-long TTL should pass; got: %v", err)
	}
}

// ---- D6 new tests: per-model min cacheable token thresholds ----

// TestCacheControl_ModelThresholds verifies documented thresholds for 5+ models.
func TestCacheControl_ModelThresholds(t *testing.T) {
	cases := []struct {
		model    string
		expected int
	}{
		{"claude-opus-4-5", 4096},
		{"claude-opus-4-6", 4096},
		{"claude-opus-4-7", 4096},
		{"claude-sonnet-4-6", 2048},
		{"claude-sonnet-4-5", 1024},
		{"claude-sonnet-3-7", 1024},
		{"claude-haiku-4-5", 4096},
		{"claude-haiku-3-5", 2048},
		{"claude-opus-4-1", 1024},
		{"claude-opus-4", 1024},
	}
	for _, tc := range cases {
		got := MinCacheableTokensForModel(tc.model)
		if got != tc.expected {
			t.Errorf("MinCacheableTokensForModel(%q) = %d, want %d", tc.model, got, tc.expected)
		}
	}
}

// TestCacheControl_ModelThresholds_UnknownFallback verifies conservative
// fallback of 4096 for unknown model identifiers.
func TestCacheControl_ModelThresholds_UnknownFallback(t *testing.T) {
	got := MinCacheableTokensForModel("claude-unknown-future-model")
	if got != 4096 {
		t.Fatalf("unknown model fallback = %d, want 4096", got)
	}
}

// TestSuggestBreakpoints_RespectsThreshold verifies that a block whose
// estimated token count is below the per-model threshold goes to Skipped
// rather than Add.
func TestSuggestBreakpoints_RespectsThreshold(t *testing.T) {
	// claude-sonnet-4-6 threshold = 2048; block with 500 tokens should be skipped.
	body := []byte(`{
		"model": "claude-sonnet-4-6",
		"messages": [{"role": "user", "content": "short context"}]
	}`)
	snap := CacheControlSnapshot{Count: 0, MaxAllowed: 4}
	// The candidate will be messages[0]; give it 500 tokens (below 2048).
	estimatedTokens := map[CacheControlLocation]int{
		{Path: "messages", Index: 0, Type: "ephemeral"}: 500,
	}
	sugg, err := SuggestBreakpoints(body, snap, estimatedTokens)
	if err != nil {
		t.Fatal(err)
	}
	if len(sugg.Add) != 0 {
		t.Fatalf("Add = %d; want 0 (block below threshold)", len(sugg.Add))
	}
	if len(sugg.Skipped) != 1 {
		t.Fatalf("Skipped = %d; want 1", len(sugg.Skipped))
	}
	if !strings.Contains(sugg.Skipped[0], "below threshold") {
		t.Fatalf("Skipped message should mention threshold; got: %q", sugg.Skipped[0])
	}
}

// TestSuggestBreakpoints_ThresholdNilBackwardCompat verifies that passing nil
// for estimatedBlockTokens preserves old behavior (no threshold filtering).
func TestSuggestBreakpoints_ThresholdNilBackwardCompat(t *testing.T) {
	body := []byte(`{
		"model": "claude-opus-4-7",
		"messages": [{"role": "user", "content": "hi"}]
	}`)
	snap := CacheControlSnapshot{Count: 0, MaxAllowed: 4}
	sugg, err := SuggestBreakpoints(body, snap, nil)
	if err != nil {
		t.Fatal(err)
	}
	// With nil tokens, the single user message candidate should be added
	// regardless of the model's threshold.
	if len(sugg.Add) != 1 {
		t.Fatalf("Add = %d; want 1 (nil tokens = no threshold filter)", len(sugg.Add))
	}
}

// TestSuggestBreakpoints_AboveThresholdAdded verifies that a block above the
// threshold is added normally.
func TestSuggestBreakpoints_AboveThresholdAdded(t *testing.T) {
	// claude-sonnet-4-6 threshold = 2048; block with 3000 tokens should be added.
	body := []byte(`{
		"model": "claude-sonnet-4-6",
		"messages": [{"role": "user", "content": "large context"}]
	}`)
	snap := CacheControlSnapshot{Count: 0, MaxAllowed: 4}
	estimatedTokens := map[CacheControlLocation]int{
		{Path: "messages", Index: 0, Type: "ephemeral"}: 3000,
	}
	sugg, err := SuggestBreakpoints(body, snap, estimatedTokens)
	if err != nil {
		t.Fatal(err)
	}
	if len(sugg.Add) != 1 {
		t.Fatalf("Add = %d; want 1 (block above threshold)", len(sugg.Add))
	}
	if len(sugg.Skipped) != 0 {
		t.Fatalf("Skipped = %d; want 0", len(sugg.Skipped))
	}
}
