package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// doTwoFactorLogin 从给定来源地址,经由被限流器包裹的桩处理器发送一次
// POST /v1/auth/login/2fa(独立的 TOTP 校验端点),并返回状态码。
func doTwoFactorLogin(rl *rateLimiter, remoteAddr string) int {
	req := httptest.NewRequest(http.MethodPost, "/v1/auth/login/2fa", nil)
	req.RemoteAddr = remoteAddr
	rec := httptest.NewRecorder()
	rl.middleware(stubOKHandler()).ServeHTTP(rec, req)
	return rec.Code
}

// TestRateLimit_B5_TwoFactorLoginInLoginClass 断言【正确行为】:独立的 TOTP
// 校验端点 /v1/auth/login/2fa(auth_handler.go:191 r.Post("/login/2fa", ...))
// 必须落在 login auth-strict class 内,并与 /v1/auth/login 共享同一个 tier
// (同一份 20/min 预算)。
//
// bug B5:rate_limit.go:149 的 login class paths 漏了 /v1/auth/login/2fa
// (注释 rate_limit.go:144「login + 2fa 共用...login 端点」写于 2fa 内联时,
// 后来 2fa 拆成独立路由,限流表未同步)。strictTierFor 按精确 r.URL.Path 匹配,
// /v1/auth/login/2fa 命不中 map → 无 auth-strict 层,仅剩全局 limiter
// (burst 180)。缺陷代码下:
//
//	(1) rl.authStrict["/v1/auth/login/2fa"] 为 nil → 本测试 RED;
//	(2) 该端点在 login burst(20)之外仍不返回 429,只受 global(180)约束。
func TestRateLimit_B5_TwoFactorLoginInLoginClass(t *testing.T) {
	rl := fixedClockLimiter(t)

	loginTier := rl.authStrict["/v1/auth/login"]
	if loginTier == nil {
		t.Fatal("login auth-strict tier not configured")
	}

	// (1) 2fa 端点必须被登记进 auth-strict,且与 login 共享同一 tier 指针
	//(共享 = 同一份预算,而非各自独立预算)。
	twoFATier := rl.authStrict["/v1/auth/login/2fa"]
	if twoFATier == nil {
		t.Fatal("/v1/auth/login/2fa not in any auth-strict class — TOTP verify endpoint only受宽松全局限流 (bug B5)")
	}
	if twoFATier != loginTier {
		t.Fatal("/v1/auth/login/2fa 未与 /v1/auth/login 共享同一 login tier — 2fa 校验的端点级配额与 login 分离")
	}

	// (2) 行为断言:同一 IP 在 login burst 之外打 2fa 端点必须 429。
	// login burst = 20,global burst = 180;若 2fa 漏进 login class,
	// 则 burst+1 处的请求只受 global 约束、不会是 429 → RED。
	burst := int(loginTier.registry.burst)
	if burst < 1 || burst >= int(rl.global.burst) {
		t.Fatalf("test precondition broken: login burst %d must be in (0, global burst %d)", burst, int(rl.global.burst))
	}

	const ip = "203.0.113.88:51000"
	for i := 0; i < burst; i++ {
		if code := doTwoFactorLogin(rl, ip); code == http.StatusTooManyRequests {
			t.Fatalf("2fa request %d/%d unexpectedly 429 (within login burst)", i+1, burst)
		}
	}
	// 第 burst+1 个 2fa 请求必须被 auth-strict 拒绝。
	if code := doTwoFactorLogin(rl, ip); code != http.StatusTooManyRequests {
		t.Fatalf("2fa request past login burst: want 429 got %d — TOTP verify endpoint 未受 login class 限流 (bug B5)", code)
	}
}
