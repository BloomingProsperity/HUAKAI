package runtimeloghttp

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/adminsessionauthtest"
	"github.com/BloomingProsperity/HUAKAI/internal/logsink"
)

func TestRuntimeLogCleanupIsSessionSafe(t *testing.T) {
	handler := newRuntimeLogsTestRouter(AdminRuntimeLogsDeps{
		Auth: adminsessionauthtest.Resolver(), Store: &runtimeLogStoreStub{},
		Sink: logsink.New(), Retention: &runtimeLogRetentionStub{}, Audit: &runtimeLogsAuditStub{},
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/admin/ops/runtime-logs/cleanup", strings.NewReader("{}"))
	req.Header.Set("Authorization", "Bearer "+adminsessionauthtest.SessionBearer)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code == http.StatusUnauthorized {
		t.Fatal("平台管理员浏览器会话不应被运行日志清理写分级门拒绝")
	}
}
