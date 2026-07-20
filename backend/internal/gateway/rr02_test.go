package gateway

import (
	"testing"
	"time"
)

// RR-02: 当没有 Retry-After header 时,从错误的 BODY 推导 cooldown(Codex
// 的 usage_limit_reached 只把 resets_at / resets_in_seconds 放在 body 里)。
func TestRetryAfterFromBody(t *testing.T) {
	now := time.Unix(1000, 0)
	cases := []struct {
		body string
		want int64
	}{
		{`{"error":{"resets_in_seconds":30}}`, 30000},
		{`{"error":{"resets_at":1060}}`, 60000}, // 未来 60s
		{`{"error":{"resets_at":900}}`, 0},      // 已过去 -> 0
		{`{"error":{"details":[{"@type":"type.googleapis.com/google.rpc.RetryInfo","retryDelay":"3.5s"}]}}`, 3500},
		{`{"error":{"details":[{"@type":"type.googleapis.com/google.rpc.RetryInfo","retryDelay":{"seconds":"4","nanos":250000000}}]}}`, 4250},
		{`{"error":{"details":[{"@type":"unrelated","retryDelay":"9s"}]}}`, 0},
		{`{"error":{"details":[{"@type":"type.googleapis.com/google.rpc.RetryInfo","retryDelay":"bad"}]}}`, 0},
		{`{"error":{"message":"nope"}}`, 0}, // 无 reset 字段
		{`not json`, 0},                     // 无法解析
		{``, 0},                             // 空
	}
	for _, c := range cases {
		// 变异守卫:让 retryAfterFromBody 忽略 body(返回 0)会让每个非零
		// 用例都坍塌 -> 变红。
		if got := retryAfterFromBody([]byte(c.body), now); got != c.want {
			t.Fatalf("retryAfterFromBody(%q)=%d want %d", c.body, got, c.want)
		}
	}
}
