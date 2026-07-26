package billingadminhttp

import (
	"net/http"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/adminsessionauthtest"
)

func TestBillingSettingsWriteIsSessionSafe(t *testing.T) {
	store := newAdminBillingSettingsStore()
	handler := newAdminBillingSettingsTestRouter(AdminBillingSettingsDeps{
		Auth: adminsessionauthtest.Resolver(), Store: store,
		TenantChecker: &adminBillingTenantCheckerStub{defaultExists: true},
		AuditUpdater:  newAdminBillingSettingsTestAuditUpdater(store, &adminBillingSettingsAuditStore{}),
	})
	if status := adminsessionauthtest.Status(
		handler, http.MethodPut, "/admin/v1/billing/settings", adminsessionauthtest.SessionBearer,
	); status == http.StatusUnauthorized {
		t.Fatal("管理员浏览器会话不应被计费设置写分级门拒绝")
	}
}
