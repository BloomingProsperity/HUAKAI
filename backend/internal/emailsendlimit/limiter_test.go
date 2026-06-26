package emailsendlimit

import (
	"testing"
	"time"
)

func TestEmailSendPerIPLimit(t *testing.T) {
	now := time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC)
	limiter := New(Config{
		Window:  time.Minute,
		Limit:   2,
		MaxKeys: 10,
		Now: func() time.Time {
			return now
		},
	})

	ipA := "198.51.100.10"
	for i := 0; i < 2; i++ {
		allowed, retryAfter := limiter.Allow(ipA)
		if !allowed {
			t.Fatalf("request %d/%d from ipA denied within limit; retryAfter=%s", i+1, 2, retryAfter)
		}
		if retryAfter != 0 {
			t.Fatalf("request %d/%d from ipA retryAfter=%s, want 0 while allowed", i+1, 2, retryAfter)
		}
	}

	allowed, retryAfter := limiter.Allow(ipA)
	if allowed {
		t.Fatal("same IP request past window limit was allowed; rotating target emails from one IP must be rejected")
	}
	if retryAfter <= 0 {
		t.Fatalf("same IP denial retryAfter=%s, want positive duration", retryAfter)
	}

	// 变异守卫:如果 handler 接线哪天改成按邮箱/全局状态作 key,而非按解析出的
	// 客户端 IP,那么在 ipA 用尽窗口后,这个来自不同 IP 的请求就会被拒绝。
	allowed, retryAfter = limiter.Allow("198.51.100.11")
	if !allowed {
		t.Fatalf("different IP denied in the same window; retryAfter=%s", retryAfter)
	}
	if retryAfter != 0 {
		t.Fatalf("different IP retryAfter=%s, want 0 while allowed", retryAfter)
	}

	now = now.Add(time.Minute)
	allowed, retryAfter = limiter.Allow(ipA)
	if !allowed {
		t.Fatalf("same IP after window elapsed denied; retryAfter=%s", retryAfter)
	}
}
