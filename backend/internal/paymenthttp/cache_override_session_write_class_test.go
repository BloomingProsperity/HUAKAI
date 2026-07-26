package paymenthttp

import (
	"net/http"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/adminsessionauthtest"
	"github.com/BloomingProsperity/HUAKAI/internal/billing"
)

func TestCachePriceOverrideWritesAreSessionSafe(t *testing.T) {
	router := chi.NewRouter()
	MountCacheOverrideAdminRoutes(router, CacheOverrideAdminDeps{
		Auth:  adminsessionauthtest.Resolver(),
		Store: billing.NewCacheOverrideStore(nil, nil),
	})
	for _, method := range []string{http.MethodPut, http.MethodDelete} {
		if status := adminsessionauthtest.Status(
			router, method, "/global", adminsessionauthtest.SessionBearer,
		); status == http.StatusUnauthorized {
			t.Fatalf("平台管理员浏览器会话执行 %s 不应被缓存价格写分级门拒绝", method)
		}
	}
}
