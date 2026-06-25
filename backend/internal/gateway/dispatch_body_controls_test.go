package gateway

import (
	"bytes"
	"strings"
	"testing"
)

// TestDispatchBodyControls_EmptyObfuscateWords verifies that an empty
// ObfuscateWords list leaves the body byte-identical — the default-off
// invariant for the cloaking feature.
func TestDispatchBodyControls_EmptyObfuscateWords(t *testing.T) {
	body := []byte(`{"model":"claude-3-5-sonnet-20241022","system":"banned word here","messages":[{"role":"user","content":"hello"}]}`)
	controls := DispatchBodyControls{
		ObfuscateWords: nil, // empty — must be strict no-op
	}
	out, err := ApplyDispatchBodyControls(body, controls)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !bytes.Equal(body, out) {
		t.Fatalf("empty ObfuscateWords must produce byte-identical output\ngot:  %s\nwant: %s", out, body)
	}
}

// TestDispatchBodyControls_WithObfuscateWords verifies that a non-empty
// ObfuscateWords list triggers cloaking through the full controls apply path.
func TestDispatchBodyControls_WithObfuscateWords(t *testing.T) {
	body := []byte(`{"model":"claude-3-5-sonnet-20241022","system":"banned word here","messages":[{"role":"user","content":"hello"}]}`)
	controls := DispatchBodyControls{
		ObfuscateWords: []string{"banned"},
	}
	out, err := ApplyDispatchBodyControls(body, controls)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if bytes.Equal(body, out) {
		t.Fatal("non-empty ObfuscateWords must transform the body")
	}
	if !strings.Contains(string(out), "b\u200banned") {
		t.Fatalf("expected ZWSP-cloaked word in output, got: %s", out)
	}
}
