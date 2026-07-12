package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestSiteConfigRouteIsAnonymous 证明 GET /v1/site/config 已挂载,且
// 在*没有* session cookie 的情况下可达。buildTestRouter 接入的是 nil 的
// platformSettings,因此一个正确挂载的匿名处理器会返回 503
//(gateway_not_configured)——绝不会是 404(未挂载),也绝不会是 401/重定向
//(需要 session)。
//
// 变异守护:把该路由用 auth.SessionMiddleware 包起来,则匿名
// 请求返回 401,使本断言转红。移除该挂载会让它返回
// 404,同样转红。
func TestSiteConfigRouteIsAnonymous(t *testing.T) {
	r := buildTestRouter(t)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/site/config", nil) // 无 cookie,无 auth header
	r.ServeHTTP(rec, req)

	if rec.Code == http.StatusNotFound {
		t.Fatalf("GET /v1/site/config returned 404; anonymous site bootstrap route must be mounted")
	}
	if rec.Code == http.StatusUnauthorized {
		t.Fatalf("GET /v1/site/config returned 401; bootstrap endpoint must be anonymous (no session)")
	}
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("GET /v1/site/config status=%d body=%s; want 503 under nil-deps test router", rec.Code, rec.Body.String())
	}
}
