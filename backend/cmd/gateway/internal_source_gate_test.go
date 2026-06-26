package main

import (
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5/middleware"
)

func runGate(t *testing.T, gate func(http.Handler) http.Handler, remoteAddr, path string, headers map[string]string) (reached bool, status int) {
	t.Helper()
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = true
		w.WriteHeader(http.StatusOK)
	})
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.RemoteAddr = remoteAddr
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	gate(next).ServeHTTP(rec, req)
	return reached, rec.Code
}

// TestInternalSourceGate_RejectsPublicAllowsTrusted：/internal/* 可从
// loopback / RFC1918 / link-local 对端访问，但不可从公网对端访问；
// 非 /internal 路径永不受闸门管控。变异：去掉 IsLoopback/IsPrivate
// 的放行 -> 可信对端得到 404；去掉前缀检查 -> 公网访问 /v1 得到 404。
func TestInternalSourceGate_RejectsPublicAllowsTrusted(t *testing.T) {
	gate := internalSourceGate(nil, nil)
	cases := []struct {
		name, remote, path string
		wantReached        bool
	}{
		{"public peer to /internal -> rejected", "203.0.113.7:51000", "/internal/keys", false},
		{"loopback to /internal -> allowed", "127.0.0.1:51000", "/internal/keys", true},
		{"ipv6 loopback to /internal -> allowed", "[::1]:51000", "/internal/hermes/tool-execute", true},
		{"rfc1918 private to /internal -> allowed", "10.4.5.6:443", "/internal/hermes/tool-execute", true},
		{"private 172.16 to /internal -> allowed", "172.16.9.9:443", "/internal/runner/bootstrap", true},
		{"public peer to /v1 (non-internal) -> untouched", "203.0.113.7:51000", "/v1/chat/completions", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reached, status := runGate(t, gate, tc.remote, tc.path, nil)
			if reached != tc.wantReached {
				t.Fatalf("reached=%v want=%v (status=%d)", reached, tc.wantReached, status)
			}
			if !tc.wantReached && status != http.StatusNotFound {
				t.Fatalf("rejected request must be 404 (invisible), got %d", status)
			}
		})
	}
}

// TestInternalSourceGate_IgnoresXForwardedForSpoof 是安全绊线：一个
// 设置了 `X-Forwarded-For: 127.0.0.1` 的公网对端依然必须被拒绝——闸门
// 依据真实的 socket RemoteAddr 判断，绝不依据可伪造的头。变异：
// 让闸门读取 X-Forwarded-For 而非 RemoteAddr -> 本测试通过 -> 红。
func TestInternalSourceGate_IgnoresXForwardedForSpoof(t *testing.T) {
	gate := internalSourceGate(nil, nil)
	reached, status := runGate(t, gate, "203.0.113.7:51000", "/internal/keys",
		map[string]string{"X-Forwarded-For": "127.0.0.1", "X-Real-IP": "127.0.0.1"})
	if reached {
		t.Fatal("X-Forwarded-For: 127.0.0.1 must NOT bypass the gate from a public socket peer")
	}
	if status != http.StatusNotFound {
		t.Fatalf("spoof attempt must be 404, got %d", status)
	}
}

// TestInternalSourceGate_RunsBeforeRealIP 守护「顺序」不变式：闸门
// 必须位于 chi 的 middleware.RealIP 之前——后者会在不做可信代理检查的
// 情况下，依据 X-Forwarded-For 改写 RemoteAddr。在正确顺序
// gate(RealIP(next)) 下，伪造 XFF=127.0.0.1 的公网对端会在 RealIP
// 改写地址之前就被闸门拒绝。变异：反转为
// RealIP(gate(next)) -> RealIP 把 RemoteAddr 改写成 127.0.0.1，闸门随后
// 看到的是 loopback 对端并予以放行 -> 抵达 next -> 红。
func TestInternalSourceGate_RunsBeforeRealIP(t *testing.T) {
	reached := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { reached = true })

	// 正确顺序：先闸门，再 RealIP。
	handler := internalSourceGate(nil, nil)(middleware.RealIP(next))
	req := httptest.NewRequest(http.MethodGet, "/internal/keys", nil)
	req.RemoteAddr = "203.0.113.7:51000" // 公网 socket 对端
	req.Header.Set("X-Forwarded-For", "127.0.0.1")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if reached {
		t.Fatal("gate must reject the public peer before RealIP rewrites RemoteAddr from X-Forwarded-For")
	}
	if rec.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d", rec.Code)
	}
}

// TestInternalSourceAllowed_ExtraCIDR：运维配置的额外 CIDR 会扩大
// 放行集合；落在该集合之外的公网 IP 仍被拒绝。
func TestInternalSourceAllowed_ExtraCIDR(t *testing.T) {
	_, allowed, _ := net.ParseCIDR("198.51.100.0/24")
	extra := parseInternalAllowCIDRs("198.51.100.0/24")
	if len(extra) != 1 || !extra[0].IP.Equal(allowed.IP) {
		t.Fatalf("parseInternalAllowCIDRs did not parse the CIDR: %+v", extra)
	}
	if !internalSourceAllowed("198.51.100.42:9000", extra) {
		t.Fatal("IP inside the extra CIDR must be allowed")
	}
	if internalSourceAllowed("203.0.113.7:9000", extra) {
		t.Fatal("public IP outside loopback/private/extra must be rejected")
	}
	if internalSourceAllowed("garbage", nil) {
		t.Fatal("unparseable peer must fail closed")
	}
}

// TestInternalSourceAllowed_IPClassificationEdges 锁定网络闸门必须处理对的
// IP 分类边界用例：一个公网地址的「IPv4-mapped IPv6」绝不能被放行
//（Go 在分类前会先去映射），而真正的 IPv6 ULA（fc00::/7）以及
// v4-mapped 的私网/loopback 才是可信的。此处一旦回归，
// 就是一次静默的公网绕过。
func TestInternalSourceAllowed_IPClassificationEdges(t *testing.T) {
	rejected := []string{
		"[::ffff:203.0.113.7]:9000", // 公网 v4 的 IPv4-mapped IPv6 —— 必须保持被拒
		"[2001:db8::1]:9000",        // 公网 IPv6
		"[100.64.0.1]:0",            // CGNAT(非 RFC1918)—— 没有显式 CIDR 时被拒
	}
	for _, a := range rejected {
		if internalSourceAllowed(a, nil) {
			t.Fatalf("%s must be rejected (public/non-private)", a)
		}
	}
	allowed := []string{
		"[fd12:3456::1]:9000",     // IPv6 ULA(私网)
		"[::ffff:10.0.0.5]:9000",  // v4-mapped 私网
		"[::ffff:127.0.0.1]:9000", // v4-mapped loopback
		"[fe80::1]:9000",          // link-local
	}
	for _, a := range allowed {
		if !internalSourceAllowed(a, nil) {
			t.Fatalf("%s must be allowed (private/loopback/link-local)", a)
		}
	}
}
