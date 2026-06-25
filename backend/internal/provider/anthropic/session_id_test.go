package anthropic

import (
	"net/http"
	"strings"
	"testing"
)

func TestCachedSessionID(t *testing.T) {
	// MUTATION GUARD: ignoring the cache (fresh每次) makes the same account return
	// different ids -> red (real Claude Code keeps one session id per session).
	// 同账号两次取须命中缓存得同 id(有状态);分别捕获再判,避免 SA4000 误判为自反比较。
	stableID7A := cachedSessionID(7)
	stableID7B := cachedSessionID(7)
	if stableID7A != stableID7B {
		t.Fatal("session id must be stable per account")
	}
	if cachedSessionID(7) == cachedSessionID(8) {
		t.Fatal("distinct accounts must get distinct session ids")
	}
	// accountID<=0 -> fresh each call
	fresh0A := cachedSessionID(0)
	fresh0B := cachedSessionID(0)
	if fresh0A == fresh0B {
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
