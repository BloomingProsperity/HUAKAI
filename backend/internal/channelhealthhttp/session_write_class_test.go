package channelhealthhttp

import (
	"net/http"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/adminsessionauthtest"
)

func TestChannelHealthOverridesAreSessionSafe(t *testing.T) {
	router := chi.NewRouter()
	MountChannelHealthAdminRoutes(router, ChannelHealthAdminDeps{
		Auth:       adminsessionauthtest.Resolver(),
		Controller: &channelHealthControllerStub{},
	})
	for _, path := range []string{
		"/7/channel-health/pause",
		"/7/channel-health/resume",
		"/7/channel-health/force-active",
	} {
		if status := adminsessionauthtest.Status(
			router, http.MethodPost, path, adminsessionauthtest.SessionBearer,
		); status == http.StatusUnauthorized {
			t.Fatalf("管理员浏览器会话写 %s 不应被写分级门拒绝", path)
		}
	}
}
