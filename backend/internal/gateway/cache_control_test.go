package gateway

import "testing"

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
	sugg, err := SuggestBreakpoints(body, CacheControlSnapshot{Count: 4, MaxAllowed: 4})
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
	sugg, err := SuggestBreakpoints(body, CacheControlSnapshot{Count: 0, MaxAllowed: 4})
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
	sugg, err := SuggestBreakpoints(body, CacheControlSnapshot{Count: 0, MaxAllowed: 4})
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
	sugg, err := SuggestBreakpoints(body, CacheControlSnapshot{Count: 0, MaxAllowed: 4})
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
	sugg, err := SuggestBreakpoints(body, CacheControlSnapshot{Count: 0, MaxAllowed: 4})
	if err != nil {
		t.Fatal(err)
	}
	if len(sugg.Add) != 1 || sugg.Add[0].Index != 0 {
		t.Fatalf("expected only user message at index 0; got %+v", sugg.Add)
	}
}

func TestSuggestBreakpoints_DefaultMaxAllowedWhenZero(t *testing.T) {
	body := []byte(`{"messages": [{"role": "user", "content": "hi"}]}`)
	sugg, err := SuggestBreakpoints(body, CacheControlSnapshot{Count: 0, MaxAllowed: 0})
	if err != nil {
		t.Fatal(err)
	}
	// MaxAllowed=0 should default to CacheControlMaxAllowed (4); 1 candidate added.
	if len(sugg.Add) != 1 {
		t.Fatalf("Add = %d; want 1", len(sugg.Add))
	}
}
