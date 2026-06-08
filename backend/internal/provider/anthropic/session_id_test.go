package anthropic

import (
	"net/http"
	"strings"
	"testing"
)

func TestCachedSessionID(t *testing.T) {
	// MUTATION GUARD: ignoring the cache (fresh每次) makes the same account return
	// different ids -> red (real Claude Code keeps one session id per session).
	if cachedSessionID(7) != cachedSessionID(7) {
		t.Fatal("session id must be stable per account")
	}
	if cachedSessionID(7) == cachedSessionID(8) {
		t.Fatal("distinct accounts must get distinct session ids")
	}
	// accountID<=0 -> fresh each call
	if cachedSessionID(0) == cachedSessionID(0) {
		t.Fatal("accountID<=0 must yield a fresh id each call")
	}
	id := cachedSessionID(7)
	if len(id) != 36 || strings.Count(id, "-") != 4 {
		t.Fatalf("session id not uuid-shaped: %q", id)
	}
}

func TestApplyClaudeSessionHeaders(t *testing.T) {
	h1 := http.Header{}
	applyClaudeSessionHeaders(h1, 7)
	if h1.Get("X-Claude-Code-Session-Id") == "" {
		t.Fatal("missing X-Claude-Code-Session-Id")
	}
	rid1 := h1.Get("X-Client-Request-Id")
	if rid1 == "" {
		t.Fatal("missing X-Client-Request-Id")
	}
	h2 := http.Header{}
	applyClaudeSessionHeaders(h2, 7)
	// session id stable per account, request id fresh per call
	if h1.Get("X-Claude-Code-Session-Id") != h2.Get("X-Claude-Code-Session-Id") {
		t.Fatal("session id must be stable per account across requests")
	}
	if rid1 == h2.Get("X-Client-Request-Id") {
		t.Fatal("client request id must be fresh per request")
	}
}
