package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"go.uber.org/zap"
)

// TestReqDecompressRunsAfterRateLimit 守护 codex #4:入站解码中间件位于限流器之后。
// 一个已被限流耗尽的 IP,再发一个 Content-Encoding: zstd + 垃圾 body 的请求:
//   - 解码在限流【之后】(正确):限流器先 429,垃圾 body 根本不被解码。
//   - 解码在限流【之前】(变异):reqdecompress 先解码垃圾 zstd 失败 → 400。
//
// 故断言 429(而非 400)直接区分中间件顺序。变异证伪:把 reqdecompress 的
// router.Use 移回限流器之前,本用例由 429 变 400,转红。
func TestReqDecompressRunsAfterRateLimit(t *testing.T) {
	clearRateLimitEnv(t)
	router := newRouter(minimalDeps(), zap.NewNop())
	const ip = "203.0.113.77:8000"

	// 先把该 IP 的 login 桶打空,直到出现 429。
	saw429 := false
	for i := 0; i < int(defaultAuthLoginPerMin)+10; i++ {
		req := httptest.NewRequest(http.MethodPost, "/v1/auth/login", nil)
		req.RemoteAddr = ip
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code == http.StatusTooManyRequests {
			saw429 = true
			break
		}
	}
	if !saw429 {
		t.Fatal("login flood 未触发 429,限流器未接入或 burst 异常")
	}

	// 桶已空:再发一个【垃圾 zstd】请求。解码若在限流后,限流先 429、垃圾不被解码;
	// 解码若在限流前,会先解码失败返 400。
	req := httptest.NewRequest(http.MethodPost, "/v1/auth/login", bytes.NewReader([]byte("not-a-valid-zstd-frame-at-all")))
	req.RemoteAddr = ip
	req.Header.Set("Content-Encoding", "zstd")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("限流耗尽的 IP 发垃圾 zstd:want 429(限流先于解码),got %d(若 400 说明解码跑在限流前)", rec.Code)
	}
}
