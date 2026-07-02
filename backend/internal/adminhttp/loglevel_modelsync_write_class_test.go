package adminhttp

import (
	"context"
	"net/http"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/adminsessionauthtest"
	"github.com/BloomingProsperity/HUAKAI/internal/modelsync"
)

func mountLogLevel() http.Handler {
	r := chi.NewRouter()
	MountLogLevelRoutes(r, LogLevelDeps{Auth: adminsessionauthtest.Resolver()})
	return r
}

type fakeModelSync struct{}

func (fakeModelSync) SyncWithActor(context.Context, string, string) (modelsync.SyncResult, error) {
	return modelsync.SyncResult{}, nil
}

func mountModelSync() http.Handler {
	r := chi.NewRouter()
	MountModelSyncRoutes(r, AdminModelSyncDeps{Auth: adminsessionauthtest.Resolver(), Service: fakeModelSync{}})
	return r
}

// SessionSafe:PUT /loglevel(运行时日志级别)与 POST /(触发模型目录同步)knob 开 session-admin 过鉴权≠401。
// 变异:摘 loglevel PUT 或 model-sync POST 的 safe → 该路由 session 写 401 → RED。
func TestLogLevelAndModelSyncSessionSafe(t *testing.T) {
	sess := adminsessionauthtest.SessionBearer
	if code := adminsessionauthtest.Status(mountLogLevel(), http.MethodPut, "/loglevel", sess); code == http.StatusUnauthorized {
		t.Fatalf("SessionSafe PUT /loglevel 应过鉴权(≠401),得 401")
	}
	if code := adminsessionauthtest.Status(mountModelSync(), http.MethodPost, "/", sess); code == http.StatusUnauthorized {
		t.Fatalf("SessionSafe POST /(model-sync)应过鉴权(≠401),得 401")
	}
}

// 只读 GET /loglevel:session-admin 恒放行(读端点 P2a 已开),与写分级无关。
func TestLogLevelReadAllowed(t *testing.T) {
	if code := adminsessionauthtest.Status(mountLogLevel(), http.MethodGet, "/loglevel", adminsessionauthtest.SessionBearer); code == http.StatusUnauthorized {
		t.Fatalf("GET /loglevel(只读)应放行,得 401")
	}
}

// token 通道豁免:hk_admin 令牌写过鉴权≠401。
func TestLogLevelTokenExempt(t *testing.T) {
	if code := adminsessionauthtest.Status(mountLogLevel(), http.MethodPut, "/loglevel", adminsessionauthtest.TokenBearer); code == http.StatusUnauthorized {
		t.Fatalf("hk_admin 令牌写 /loglevel 应过鉴权(≠401),得 401")
	}
}
