package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/clientip"
	"github.com/BloomingProsperity/HUAKAI/internal/usersession"
)

type ipCapturingValidator struct{ gotIP string }

func (v *ipCapturingValidator) Validate(_ context.Context, _ string, ip string, _ string) (usersession.ValidatedSession, error) {
	v.gotIP = ip
	return usersession.ValidatedSession{TenantID: 1, UserID: 42, FamilyID: "fam", TokenID: "tok", Generation: 1}, nil
}

// TestSessionMiddlewareUsesTrustedProxyClientIP 证明 session 校验路径
// 使用与 login/refresh 相同的、感知可信代理的 resolver 来推导客户端 IP。
// 否则 login 存的是真实的 forwarded 客户端 IP, 而 middleware 用代理的
// socket IP 来校验, DetectDrift 就可能在反向代理后误吊销一个有效 session。
//
// 变异检查: 把 SessionMiddleware 退回到旧的 requestIP(r)/RemoteAddr 取值 → 捕获到的
// Validate IP 会变成 "10.1.2.3" 而非 forwarded 的 "198.51.100.9" → 红。
func TestSessionMiddlewareUsesTrustedProxyClientIP(t *testing.T) {
	resolver, err := clientip.NewResolver([]string{"10.0.0.0/8"})
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	v := &ipCapturingValidator{}
	nextCalled := false
	h := SessionMiddleware(v, resolver)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		nextCalled = true
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.RemoteAddr = "10.1.2.3:5000" // 可信的反向代理 peer
	req.Header.Set("Authorization", "Bearer sometoken")
	req.Header.Set("X-Forwarded-For", "198.51.100.9") // 代理后面的真实客户端
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if !nextCalled {
		t.Fatalf("middleware rejected a valid session: status=%d body=%s", rec.Code, rec.Body.String())
	}
	if v.gotIP != "198.51.100.9" {
		t.Fatalf("Validate IP=%q want forwarded client 198.51.100.9 (middleware must use the resolver, not RemoteAddr)", v.gotIP)
	}
}

// TestSessionMiddlewareNilResolverFallsBackToRemoteAddr 证明 nil-resolver 路径
// (直接暴露/旧行为) 仍然用 socket peer 来校验。
func TestSessionMiddlewareNilResolverFallsBackToRemoteAddr(t *testing.T) {
	v := &ipCapturingValidator{}
	h := SessionMiddleware(v, nil)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.RemoteAddr = "203.0.113.7:443"
	req.Header.Set("Authorization", "Bearer sometoken")
	req.Header.Set("X-Forwarded-For", "198.51.100.9") // untrusted (no resolver) → ignored
	h.ServeHTTP(httptest.NewRecorder(), req)
	if v.gotIP != "203.0.113.7" {
		t.Fatalf("nil resolver Validate IP=%q want socket peer 203.0.113.7", v.gotIP)
	}
}
