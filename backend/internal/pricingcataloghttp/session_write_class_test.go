package pricingcataloghttp

import (
	"net/http"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/adminsessionauthtest"
)

func TestPricingRatioWritesAreSessionSafe(t *testing.T) {
	deps := validPricingRatioDeps()
	deps.Auth = adminsessionauthtest.Resolver()
	router := chi.NewRouter()
	MountPricingRatioRoutes(router, deps)
	for _, method := range []string{http.MethodPut, http.MethodDelete} {
		if status := adminsessionauthtest.Status(
			router, method, "/7?tenant_id=1", adminsessionauthtest.SessionBearer,
		); status == http.StatusUnauthorized {
			t.Fatalf("平台管理员浏览器会话执行 %s 不应被倍率写分级门拒绝", method)
		}
	}
}
