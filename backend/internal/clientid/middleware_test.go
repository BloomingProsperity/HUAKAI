// middleware_test.go — U6-B 测试: HTTP middleware 把 identity 写到 ctx；
// 下游 handler 通过 IdentityFromContext 能拿到正确身份。
package clientid

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestMiddleware_InjectsIdentity 验证: middleware 把 Detect 结果挂到 ctx
// → 下游 handler 通过 IdentityFromContext 拿到一致结果。
func TestMiddleware_InjectsIdentity(t *testing.T) {
	cases := []struct {
		name     string
		ua       string
		xClient  string
		wantID   Identity
		wantConf float64
	}{
		{"cursor UA", "Cursor/0.42", "", IdentityCursor, 0.9},
		{"claude-cli UA", "claude-cli/1.0", "", IdentityClaudeCode, 0.9},
		{"explicit X-Client-Name", "Mozilla/5.0", "Cursor IDE", IdentityCursor, 1.0},
		{"unknown", "Mozilla/5.0", "", IdentityUnknown, 0.5},
		{"curl script", "curl/7.81.0", "", IdentityCurlScript, 0.7},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var gotID Identity
			var gotConf float64
			next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotID, gotConf = IdentityFromContext(r.Context())
				w.WriteHeader(http.StatusOK)
			})
			handler := Middleware(nil)(next)

			req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
			req.Header.Set("User-Agent", tc.ua)
			if tc.xClient != "" {
				req.Header.Set("X-Client-Name", tc.xClient)
			}
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			if gotID != tc.wantID {
				t.Errorf("ctx Identity=%q want %q", gotID, tc.wantID)
			}
			if gotConf != tc.wantConf {
				t.Errorf("ctx confidence=%.2f want %.2f", gotConf, tc.wantConf)
			}
			if rec.Code != http.StatusOK {
				t.Errorf("downstream handler status=%d", rec.Code)
			}
		})
	}
}

// TestMiddleware_DoesNotConsumeBody 验证: middleware 不读 request body，
// 下游 handler 仍可正常读 body（防 body 被消费 bug）。
func TestMiddleware_DoesNotConsumeBody(t *testing.T) {
	var bodyAtHandler []byte
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf := make([]byte, 1024)
		n, _ := r.Body.Read(buf)
		bodyAtHandler = buf[:n]
		w.WriteHeader(http.StatusOK)
	})
	handler := Middleware(nil)(next)

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"gpt-4o","messages":[]}`))
	req.Header.Set("User-Agent", "curl/7.81")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	want := `{"model":"gpt-4o","messages":[]}`
	if string(bodyAtHandler) != want {
		t.Errorf("downstream body=%q want %q", string(bodyAtHandler), want)
	}
}

// TestMiddleware_IdempotentMultipleCalls 多次 Middleware(nil)() 链式不应错配。
func TestMiddleware_IdempotentMultipleCalls(t *testing.T) {
	var got Identity
	final := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, _ = IdentityFromContext(r.Context())
	})
	// 双 middleware 嵌套: 内层的 detection 应不被外层覆盖
	wrapped := Middleware(nil)(Middleware(nil)(final))

	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.Header.Set("User-Agent", "Cursor/0.42")
	wrapped.ServeHTTP(httptest.NewRecorder(), req)

	if got != IdentityCursor {
		t.Errorf("双层 middleware 后 identity=%q want cursor", got)
	}
}

// TestMiddleware_PreservesOriginalRequest 验证 middleware 不改 request 头/方法。
func TestMiddleware_PreservesOriginalRequest(t *testing.T) {
	var sawMethod, sawPath, sawAuth string
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawMethod = r.Method
		sawPath = r.URL.Path
		sawAuth = r.Header.Get("Authorization")
	})
	handler := Middleware(nil)(next)

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	req.Header.Set("Authorization", "Bearer hk_abc")
	req.Header.Set("User-Agent", "Cursor/0.42")
	handler.ServeHTTP(httptest.NewRecorder(), req)

	if sawMethod != "POST" || sawPath != "/v1/chat/completions" || sawAuth != "Bearer hk_abc" {
		t.Errorf("middleware 改了 request: method=%q path=%q auth=%q",
			sawMethod, sawPath, sawAuth)
	}
}

