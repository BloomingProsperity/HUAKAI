package rate

import (
	"net/http"
	"strconv"
	"testing"
	"time"
)

func TestParseMultiWindowReset(t *testing.T) {
	// 变异:忽略 5h 的 reset 头必然让 until/reason 出错。
	now := time.Date(2026, 6, 8, 12, 0, 0, 0, time.UTC)
	reset5h := now.Add(30 * time.Minute)
	headers := http.Header{}
	headers.Set("anthropic-ratelimit-unified-5h-reset", strconv.FormatInt(reset5h.Unix(), 10))
	headers.Set("anthropic-ratelimit-unified-5h-surpassed-threshold", "true")

	until, reason, ok := ParseMultiWindowReset(headers, now)
	if !ok {
		t.Fatal("ParseMultiWindowReset ok=false, want true")
	}
	if !until.Equal(reset5h) {
		t.Fatalf("until=%s want %s", until, reset5h)
	}
	if reason != ReasonRateLimit5h {
		t.Fatalf("reason=%s want %s", reason, ReasonRateLimit5h)
	}

	// 守卫:没有「窗口已超限」标记时,parser 不得细化冷却。
	guardHeaders := http.Header{}
	guardHeaders.Set("anthropic-ratelimit-unified-5h-reset", strconv.FormatInt(reset5h.Unix(), 10))
	until, reason, ok = ParseMultiWindowReset(guardHeaders, now)
	if ok || !until.IsZero() || reason != "" {
		t.Fatalf("headers without exceeded marker parsed as (%s,%s,%v), want zero/false", until, reason, ok)
	}
}
