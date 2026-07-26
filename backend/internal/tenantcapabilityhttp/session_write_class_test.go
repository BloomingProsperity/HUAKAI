package tenantcapabilityhttp

import (
	"net/http"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/adminsessionauthtest"
)

func TestTenantCapabilityWriteIsSessionSafe(t *testing.T) {
	handler := testHandler(adminsessionauthtest.Resolver(), &storeStub{})
	req := adminsessionauthtest.Status(
		handler, http.MethodPut, "/admin/v1/tenant-capabilities/7/account_intake",
		adminsessionauthtest.SessionBearer,
	)
	if req == http.StatusUnauthorized {
		t.Fatal("平台管理员浏览器会话不应被租户能力写分级门拒绝")
	}
}
