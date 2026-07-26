package proxyadminhttp

import (
	"net/http"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/adminsessionauthtest"
)

func TestTenantDefaultProxyWriteIsSessionSafe(t *testing.T) {
	router := chi.NewRouter()
	MountTenantRoutes(router, Deps{
		Auth: adminsessionauthtest.Resolver(),
		TenantDefaults: &tenantDefaultStoreStub{
			values: map[int64]*int64{1: nil},
		},
	})
	if status := adminsessionauthtest.Status(
		router, http.MethodPut, "/1/default-proxy", adminsessionauthtest.SessionBearer,
	); status == http.StatusUnauthorized {
		t.Fatal("管理员浏览器会话不应被租户默认代理写分级门拒绝")
	}
}
