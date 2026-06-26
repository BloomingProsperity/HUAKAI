package anthropic

import (
	"net/http"
	"testing"
)

// CLAUDEHDR-01:真实客户端总会发送的那组静态 Claude Code / Stainless 请求头。
// 缺失这些头就是中转站的破绽。
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

// MUTATION GUARD:SetIfEmpty 绝不能覆盖调用方已提供的值。
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
	// 未设置的字段仍会被填上
	if h.Get("X-App") != "cli" {
		t.Fatalf("X-App not filled: %q", h.Get("X-App"))
	}
}
