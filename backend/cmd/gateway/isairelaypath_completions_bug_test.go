package main

import "testing"

// TestIsAIRelayPath_IncludesCompletions_S2 证明审计 S2.3:
// /v1/completions 与 /v1/messages/count_tokens 曾漏出 isAIRelayPath 豁免名单,
// 于是被 aiAwareTimeout 的 60s 连接级总超时误砍(长流/慢上游在 60s 处 ctx 取消 →
// 长响应被截断、部分交付走欠费待对账)。修复:两条路径加入白名单。
//
// 判别:断言这两条路径在豁免名单内。修复前 false → RED;修复后 true → GREEN。
func TestIsAIRelayPath_IncludesCompletions_S2(t *testing.T) {
	for _, p := range []string{"/v1/completions", "/v1/messages/count_tokens"} {
		if !isAIRelayPath(p) {
			t.Fatalf("BUG(S2): %q 应在 AI 转发豁免名单内(否则被 60s 连接超时误砍长流),实得 isAIRelayPath=false", p)
		}
	}
	// 回归:原有豁免路径仍在;非转发路径仍不豁免。
	if !isAIRelayPath("/v1/chat/completions") {
		t.Fatal("回归:/v1/chat/completions 应仍豁免")
	}
	if isAIRelayPath("/v1/admin/usage/overview") {
		t.Fatal("回归:非转发路径不应被豁免")
	}
}
