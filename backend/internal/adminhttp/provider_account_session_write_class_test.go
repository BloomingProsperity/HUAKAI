package adminhttp

import (
	"net/http"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/adminsessionauthtest"
)

func TestProviderAccountProbeAndBulkWritesAreSessionSafe(t *testing.T) {
	probeRouter := chi.NewRouter()
	MountProviderAccountTestRoutes(probeRouter, ProviderAccountTestDeps{
		Auth:     adminsessionauthtest.Resolver(),
		Accounts: newProviderAccountTestAccountStoreStub(),
		Tester:   &providerAccountTesterStub{},
	})
	if status := adminsessionauthtest.Status(
		probeRouter, http.MethodPost, "/7/test?tenant_id=1", adminsessionauthtest.SessionBearer,
	); status == http.StatusUnauthorized {
		t.Fatal("管理员浏览器会话不应被账号探测写分级门拒绝")
	}

	bulkRouter := chi.NewRouter()
	MountProviderAccountBulkRoutes(bulkRouter, ProviderAccountBulkDeps{
		Auth:  adminsessionauthtest.Resolver(),
		Store: &stubBulkStore{},
	})
	if status := adminsessionauthtest.Status(
		bulkRouter, http.MethodPost, "/bulk-by-tag?tenant_id=1", adminsessionauthtest.SessionBearer,
	); status == http.StatusUnauthorized {
		t.Fatal("管理员浏览器会话不应被账号批量操作写分级门拒绝")
	}
}
