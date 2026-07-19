package executor

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestB14_UpstreamFailurePreservesStatusAndRetryAfter 断言:回退预算耗尽后把
// 上游非 2xx 终态失败写给客户端时,executor 必须保留上游派生的客户端状态
// (401 -> 401,429 -> 非 502 的可重试状态) 与 Retry-After 退避提示,和流式 /
// buffered chat 路径 (internal/gatewayhttp/chat_completions_error.go:115-116 用
// decision.ClientStatus + ceil(classification.RetryAfterMs/1000)) 保持一致。
//
// 有缺陷的 UpstreamFailure/WriteHTTP 把每个非 2xx 一律塌成 502 且丢弃
// Retry-After,故本测试在修复前应 RED。
func TestB14_UpstreamFailurePreservesStatusAndRetryAfter(t *testing.T) {
	t.Run("upstream_401_not_collapsed_to_502", func(t *testing.T) {
		f := UpstreamFailure(http.StatusUnauthorized, http.Header{}, []byte(`{"error":"unauthorized"}`), "openai")
		if f == nil {
			t.Fatal("UpstreamFailure returned nil")
		}
		if f.Status == http.StatusBadGateway {
			t.Fatalf("upstream 401 collapsed to 502; want upstream-derived client status (401)")
		}
		if f.Status != http.StatusUnauthorized {
			t.Fatalf("upstream 401 client status = %d; want 401", f.Status)
		}
	})

	t.Run("upstream_429_preserves_retry_after_and_status", func(t *testing.T) {
		hdr := http.Header{}
		hdr.Set("Retry-After", "30")
		f := UpstreamFailure(http.StatusTooManyRequests, hdr, []byte(`{"error":"rate_limited"}`), "openai")
		if f == nil {
			t.Fatal("UpstreamFailure returned nil")
		}
		if f.Status == http.StatusBadGateway {
			t.Fatalf("upstream 429 collapsed to 502; want non-502 retryable status")
		}
		if f.RetryAfterSeconds <= 0 {
			t.Fatalf("upstream 429 dropped Retry-After; RetryAfterSeconds=%d want >0 (upstream sent Retry-After: 30)", f.RetryAfterSeconds)
		}

		rec := httptest.NewRecorder()
		WriteHTTP(rec, f)
		if got := rec.Header().Get("Retry-After"); got == "" {
			t.Fatalf("WriteHTTP dropped Retry-After header for upstream 429")
		}
		if rec.Code == http.StatusBadGateway {
			t.Fatalf("WriteHTTP wrote 502 for upstream 429; want non-502 (client cannot back off)")
		}
	})
}
