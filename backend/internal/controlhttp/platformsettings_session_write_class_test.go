package controlhttp

import (
	"net/http"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/adminsessionauthtest"
)

func TestPlatformSettingsWriteIsSessionSafe(t *testing.T) {
	handler := newPlatformSettingsTestRouter(PlatformSettingsDeps{
		Auth:    adminsessionauthtest.Resolver(),
		Service: &platformSettingsServiceStub{},
	})
	if status := adminsessionauthtest.Status(
		handler, http.MethodPut, "/v1/admin/platform-settings/site_name",
		adminsessionauthtest.SessionBearer,
	); status == http.StatusUnauthorized {
		t.Fatal("平台管理员浏览器会话不应被平台设置写分级门拒绝")
	}
}
