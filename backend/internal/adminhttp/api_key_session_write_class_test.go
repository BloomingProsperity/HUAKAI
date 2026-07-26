package adminhttp

import (
	"net/http"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/adminsessionauthtest"
)

func TestAPIKeyAdminWritesAreSessionSafe(t *testing.T) {
	deps := AdminAPIKeysDeps{
		Auth: adminsessionauthtest.Resolver(), Issuer: &apiKeyIssuerStub{},
		Revoker: &apiKeyRevokerStub{}, Queries: &apiKeyQueriesStub{exists: true},
		PlatformTenantID: 1,
	}
	for _, tc := range []struct {
		method string
		path   string
	}{
		{http.MethodPost, "/admin/v1/api-keys"},
		{http.MethodPost, "/admin/v1/api-keys/7/revoke"},
	} {
		router := chi.NewRouter()
		router.Route("/admin/v1/api-keys", func(r chi.Router) { MountAPIKeyRoutes(r, deps) })
		if status := adminsessionauthtest.Status(
			router, tc.method, tc.path, adminsessionauthtest.SessionBearer,
		); status == http.StatusUnauthorized {
			t.Fatalf("管理员浏览器会话写 %s %s 不应被写分级门拒绝", tc.method, tc.path)
		}
	}
}
