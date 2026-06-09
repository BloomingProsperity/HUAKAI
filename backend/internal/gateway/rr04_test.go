// RR-04 tests: EnforceCacheControlLimit
package gateway

import (
	"bytes"
	"encoding/json"
	"testing"
)

// ---- helpers for RR-04 tests ----

// buildRR04Body constructs a Messages body with:
//
//	systemCC:  number of system blocks that carry cache_control (always first)
//	msgCC:     number of message content blocks that carry cache_control (appended in order)
//
// Total CC = systemCC + msgCC.
func buildRR04Body(t *testing.T, systemCC, systemTotal, msgCC, msgTotal int) []byte {
	t.Helper()

	// Build system array.
	systemBlocks := make([]interface{}, systemTotal)
	for i := 0; i < systemTotal; i++ {
		b := map[string]interface{}{"type": "text", "text": "sys"}
		if i < systemCC {
			b["cache_control"] = map[string]interface{}{"type": "ephemeral"}
		}
		systemBlocks[i] = b
	}

	// Build one message with msgTotal content blocks.
	contentBlocks := make([]interface{}, msgTotal)
	for i := 0; i < msgTotal; i++ {
		b := map[string]interface{}{"type": "text", "text": "msg"}
		if i < msgCC {
			b["cache_control"] = map[string]interface{}{"type": "ephemeral"}
		}
		contentBlocks[i] = b
	}

	root := map[string]interface{}{
		"model":      "claude-opus-4-6",
		"max_tokens": 64,
		"system":     systemBlocks,
		"messages": []interface{}{
			map[string]interface{}{
				"role":    "user",
				"content": contentBlocks,
			},
		},
	}

	out, err := json.Marshal(root)
	if err != nil {
		t.Fatalf("buildRR04Body: marshal: %v", err)
	}
	return out
}

// ---- Test: 5 breakpoints -> trimmed to 4, LAST one removed ----

func TestEnforceCacheControlLimit_5to4(t *testing.T) {
	// 1 system CC + 4 message CC blocks = 5 total.
	// After enforce: first 4 kept (1 system + 3 msg), last msg block loses CC.
	body := buildRR04Body(t, 1, 1, 4, 4)

	// Verify precondition.
	snap, err := InspectCacheControl(body)
	if err != nil {
		t.Fatalf("precondition inspect: %v", err)
	}
	if snap.Count != 5 {
		t.Fatalf("precondition: want 5 CC blocks, got %d", snap.Count)
	}

	out, err := EnforceCacheControlLimit(body, CacheControlMaxAllowed)
	if err != nil {
		t.Fatalf("EnforceCacheControlLimit error: %v", err)
	}

	snapAfter, err := InspectCacheControl(out)
	if err != nil {
		t.Fatalf("post-enforce inspect: %v", err)
	}
	if snapAfter.Count != 4 {
		t.Fatalf("after enforce: want 4 CC blocks, got %d", snapAfter.Count)
	}

	// The LAST (4th, 0-indexed index=3) message content block should have lost cache_control.
	// Parse and verify directly.
	var root map[string]interface{}
	if err := json.Unmarshal(out, &root); err != nil {
		t.Fatalf("unmarshal out: %v", err)
	}
	msgs := root["messages"].([]interface{})
	msg0 := msgs[0].(map[string]interface{})
	content := msg0["content"].([]interface{})
	if len(content) != 4 {
		t.Fatalf("expected 4 content blocks, got %d", len(content))
	}
	// Blocks 0,1,2 should keep CC (indices 1,2,3 of the 5 original CC sequence —
	// after the 1 system CC block consumed slot 0).
	for i := 0; i < 3; i++ {
		blk := content[i].(map[string]interface{})
		if _, has := blk["cache_control"]; !has {
			t.Errorf("content[%d] should still have cache_control (kept)", i)
		}
	}
	// Block index 3 is the removed one.
	blkLast := content[3].(map[string]interface{})
	if _, has := blkLast["cache_control"]; has {
		t.Errorf("content[3] should have cache_control REMOVED (excess), but still has it")
	}
}

// ---- Test: exactly 4 -> byte-identical ----

func TestEnforceCacheControlLimit_Exactly4_ByteIdentical(t *testing.T) {
	body := buildRR04Body(t, 1, 1, 3, 3) // 1+3=4

	snap, err := InspectCacheControl(body)
	if err != nil {
		t.Fatalf("precondition: %v", err)
	}
	if snap.Count != 4 {
		t.Fatalf("precondition: want 4, got %d", snap.Count)
	}

	out, err := EnforceCacheControlLimit(body, CacheControlMaxAllowed)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !bytes.Equal(body, out) {
		t.Fatalf("expected byte-identical output for exactly-4 input\nin:  %s\nout: %s", body, out)
	}
}

// ---- Test: 0 breakpoints -> byte-identical ----

func TestEnforceCacheControlLimit_Zero_ByteIdentical(t *testing.T) {
	body := buildRR04Body(t, 0, 2, 0, 2) // 0 CC

	out, err := EnforceCacheControlLimit(body, CacheControlMaxAllowed)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !bytes.Equal(body, out) {
		t.Fatalf("expected byte-identical output for 0-CC input\nin:  %s\nout: %s", body, out)
	}
}

// ---- Test: 2 breakpoints -> byte-identical ----

func TestEnforceCacheControlLimit_Two_ByteIdentical(t *testing.T) {
	body := buildRR04Body(t, 0, 1, 2, 2) // 0+2=2

	out, err := EnforceCacheControlLimit(body, CacheControlMaxAllowed)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !bytes.Equal(body, out) {
		t.Fatalf("expected byte-identical for 2-CC input\nin:  %s\nout: %s", body, out)
	}
}

// ---- Test: malformed JSON -> passthrough unchanged ----

func TestEnforceCacheControlLimit_MalformedJSON_Passthrough(t *testing.T) {
	bad := []byte(`{not valid json`)

	out, err := EnforceCacheControlLimit(bad, CacheControlMaxAllowed)
	// Must return original bytes unchanged.
	if !bytes.Equal(bad, out) {
		t.Fatalf("malformed JSON: expected original bytes back\ngot: %s", out)
	}
	// Error must be non-nil.
	if err == nil {
		t.Fatalf("malformed JSON: expected non-nil error, got nil")
	}
}
