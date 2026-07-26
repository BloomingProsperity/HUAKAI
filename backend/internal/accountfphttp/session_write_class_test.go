package accountfphttp

import (
	"net/http"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/adminsessionauthtest"
)

func TestFingerprintBindingWriteIsSessionSafe(t *testing.T) {
	router := chi.NewRouter()
	MountRoutes(router, Deps{Auth: adminsessionauthtest.Resolver(), Store: &storeStub{}})
	if status := adminsessionauthtest.Status(
		router, http.MethodPatch, "/7/fingerprint-profile?tenant_id=1",
		adminsessionauthtest.SessionBearer,
	); status == http.StatusUnauthorized {
		t.Fatal("管理员浏览器会话不应被指纹绑定写分级门拒绝")
	}
}
