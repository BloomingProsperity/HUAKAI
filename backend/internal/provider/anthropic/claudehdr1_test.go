package anthropic

import (
	"net/http"
	"testing"
)

// CLAUDEHDR-01: the static Claude Code / Stainless header set real clients always
// send. Their absence is a relay tell.
func TestStampClaudeCodeStaticHeaders(t *testing.T) {
	h := http.Header{}
	stampClaudeCodeStaticHeaders(h)
	want := map[string]string{
		"X-App":                   "cli",
		"X-Stainless-Retry-Count": "0",
		"X-Stainless-Runtime":     "node",
		"X-Stainless-Lang":        "js",
		"X-Stainless-Timeout":     "600",
		"Connection":              "keep-alive",
	}
	for k, v := range want {
		if got := h.Get(k); got != v {
			t.Fatalf("header %s=%q want %q", k, got, v)
		}
	}
}

// MUTATION GUARD: SetIfEmpty must NOT overwrite a caller-supplied value.
func TestStampClaudeCodeStaticHeaders_PreservesCaller(t *testing.T) {
	h := http.Header{}
	h.Set("X-Stainless-Runtime", "deno")
	h.Set("Connection", "close")
	stampClaudeCodeStaticHeaders(h)
	if h.Get("X-Stainless-Runtime") != "deno" {
		t.Fatalf("caller X-Stainless-Runtime overwritten: %q", h.Get("X-Stainless-Runtime"))
	}
	if h.Get("Connection") != "close" {
		t.Fatalf("caller Connection overwritten: %q", h.Get("Connection"))
	}
	// an unset one is still filled
	if h.Get("X-App") != "cli" {
		t.Fatalf("X-App not filled: %q", h.Get("X-App"))
	}
}
