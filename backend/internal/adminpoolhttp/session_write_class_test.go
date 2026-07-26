package adminpoolhttp

import (
	"net/http"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/adminsessionauthtest"
)

func TestProviderAccountWritesAreSessionSafe(t *testing.T) {
	store := &adminPoolStoreStub{}
	deps := AdminPoolAccountDeps{
		Auth: adminsessionauthtest.Resolver(), Store: store,
		Credentials:       &adminPoolCredentialWriterStub{},
		RateLimitRecovery: &adminPoolRateLimitRecoveryStub{},
		Capabilities:      allowAdminPoolCapability{},
		PlatformTenantID:  1,
	}
	router := chi.NewRouter()
	MountAdminPoolAccountRoutes(router, deps)

	for _, tc := range []struct {
		method string
		path   string
	}{
		{http.MethodPost, "/?tenant_id=1"},
		{http.MethodPatch, "/7?tenant_id=1"},
		{http.MethodPatch, "/7/enabled?tenant_id=1"},
		{http.MethodPost, "/7/clear-rate-limit?tenant_id=1"},
		{http.MethodPost, "/7/recover?tenant_id=1"},
		{http.MethodDelete, "/7?tenant_id=1"},
	} {
		if status := adminsessionauthtest.Status(
			router, tc.method, tc.path, adminsessionauthtest.SessionBearer,
		); status == http.StatusUnauthorized {
			t.Fatalf("管理员浏览器会话写 %s %s 不应被写分级门拒绝", tc.method, tc.path)
		}
	}
}

func TestPoolWritesAreSessionSafe(t *testing.T) {
	router := NewAdminPoolsHandler(AdminPoolsDeps{
		Auth:  adminsessionauthtest.Resolver(),
		Store: &adminPoolsStoreStub{},
	})
	for _, tc := range []struct {
		method string
		path   string
	}{
		{http.MethodPost, "/?tenant_id=1"},
		{http.MethodPatch, "/7?tenant_id=1"},
		{http.MethodDelete, "/7?tenant_id=1"},
	} {
		if status := adminsessionauthtest.Status(
			router, tc.method, tc.path, adminsessionauthtest.SessionBearer,
		); status == http.StatusUnauthorized {
			t.Fatalf("管理员浏览器会话写 %s %s 不应被写分级门拒绝", tc.method, tc.path)
		}
	}
}
