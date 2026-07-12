package controlhttp

import (
	"context"
	"net/http"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/adminsessionauthtest"
	"github.com/BloomingProsperity/HUAKAI/internal/routeadmin"
)

// fakeRouteService:非 nil 后端,返回良性零值——让 handler 越过鉴权前的 nil 后端 503 兜底,走到真鉴权。
type fakeRouteService struct{}

func (fakeRouteService) Create(context.Context, routeadmin.CreateInput) (routeadmin.Route, error) {
	return routeadmin.Route{}, nil
}
func (fakeRouteService) List(context.Context, int64) ([]routeadmin.Route, error) { return nil, nil }
func (fakeRouteService) Get(context.Context, int64, int64) (routeadmin.Route, error) {
	return routeadmin.Route{}, nil
}
func (fakeRouteService) Update(context.Context, routeadmin.UpdateInput) (routeadmin.Route, error) {
	return routeadmin.Route{}, nil
}
func (fakeRouteService) SetEnabled(context.Context, int64, int64, bool, int64) (routeadmin.Route, error) {
	return routeadmin.Route{}, nil
}
func (fakeRouteService) Delete(context.Context, int64, int64, int64) (routeadmin.Route, error) {
	return routeadmin.Route{}, nil
}

func mountRouteAdmin() http.Handler {
	r := chi.NewRouter()
	MountRouteAdminRoutes(r, RouteAdminDeps{Auth: adminsessionauthtest.Resolver(), Service: fakeRouteService{}})
	return r
}

// SessionSafe 写端点(分组路由增改停删):session-admin → 过鉴权(≠401)。
// 变异:摘掉任一路由的 .With(safe) → 该路由 session 写变 writeClassNone → 401 → RED。
func TestRouteAdminSessionSafeWrites(t *testing.T) {
	h := mountRouteAdmin()
	for _, tc := range []struct{ m, p string }{
		{http.MethodPost, "/"},
		{http.MethodPut, "/5"},
		{http.MethodPut, "/5/enabled"},
		{http.MethodDelete, "/5"},
	} {
		if code := adminsessionauthtest.Status(h, tc.m, tc.p, adminsessionauthtest.SessionBearer); code == http.StatusUnauthorized {
			t.Fatalf("SessionSafe 写 %s %s 应过鉴权(≠401),得 401", tc.m, tc.p)
		}
	}
}

// token 通道豁免:hk_admin 令牌写过鉴权(≠401)。
func TestRouteAdminTokenExempt(t *testing.T) {
	h := mountRouteAdmin()
	if code := adminsessionauthtest.Status(h, http.MethodDelete, "/5", adminsessionauthtest.TokenBearer); code == http.StatusUnauthorized {
		t.Fatalf("hk_admin 令牌写应过鉴权(≠401),得 401")
	}
}
