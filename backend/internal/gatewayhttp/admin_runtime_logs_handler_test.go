package gatewayhttp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/admintest"
	admindb "github.com/BloomingProsperity/HUAKAI/internal/db/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/logsink"
)

type runtimeLogsAuditStub struct {
	events []admindb.InsertAdminAuditEventParams
	err    error
}

func (s *runtimeLogsAuditStub) InsertAdminAuditEvent(_ context.Context, p admindb.InsertAdminAuditEventParams) (admindb.InsertAdminAuditEventRow, error) {
	if s.err != nil {
		return admindb.InsertAdminAuditEventRow{}, s.err
	}
	s.events = append(s.events, p)
	return admindb.InsertAdminAuditEventRow{}, nil
}

type runtimeLogsAuthStub struct {
	ident admin.AdminIdentity
	err   error
}

func (s runtimeLogsAuthStub) Resolve(context.Context, *http.Request) (admin.AdminIdentity, error) {
	return s.ident, s.err
}

type runtimeLogStoreStub struct {
	rows       []logsink.RuntimeLogRow
	gotParams  logsink.ListParams
	gotCleanup time.Time
	deleted    int64
}

func (s *runtimeLogStoreStub) ListRuntimeLogs(_ context.Context, p logsink.ListParams) ([]logsink.RuntimeLogRow, error) {
	s.gotParams = p
	return s.rows, nil
}

func (s *runtimeLogStoreStub) CleanupRuntimeLogs(_ context.Context, before time.Time) (int64, error) {
	s.gotCleanup = before
	return s.deleted, nil
}

func newRuntimeLogsTestRouter(d AdminRuntimeLogsDeps) http.Handler {
	r := chi.NewRouter()
	r.Route("/v1/admin/ops", func(r chi.Router) {
		MountAdminRuntimeLogRoutes(r, d)
	})
	return r
}

// 角色闸门:运行日志是平台级数据,tenant_operator 必须 403(变异:放行 → 红)。
func TestRuntimeLogsPlatformAdminOnly(t *testing.T) {
	handler := newRuntimeLogsTestRouter(AdminRuntimeLogsDeps{
		Auth:  runtimeLogsAuthStub{ident: admintest.TenantOperator(1, 7)},
		Store: &runtimeLogStoreStub{},
		Sink:  logsink.New(),
		Audit: &runtimeLogsAuditStub{},
	})
	for _, probe := range []struct{ method, path, body string }{
		{http.MethodGet, "/v1/admin/ops/runtime-logs", ""},
		{http.MethodPost, "/v1/admin/ops/runtime-logs/cleanup", `{"before":"2026-01-01T00:00:00Z"}`},
		{http.MethodGet, "/v1/admin/ops/runtime-logs/health", ""},
	} {
		rec := serveRuntimeLogsJSON(t, handler, probe.method, probe.path, probe.body)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("%s %s: tenant_operator 应 403, got %d", probe.method, probe.path, rec.Code)
		}
	}
}

// 列表:过滤参数透传到 store;响应带键集游标 next_before_id=末行 id。
func TestRuntimeLogsListPassesFiltersAndCursor(t *testing.T) {
	rid := "req-1"
	store := &runtimeLogStoreStub{rows: []logsink.RuntimeLogRow{
		{ID: 42, Level: "error", Component: "billing", Message: "boom", RequestID: &rid, Attrs: json.RawMessage(`{}`)},
		{ID: 40, Level: "warn", Component: "billing", Message: "careful", Attrs: json.RawMessage(`{}`)},
	}}
	handler := newRuntimeLogsTestRouter(AdminRuntimeLogsDeps{
		Auth:  runtimeLogsAuthStub{ident: admintest.Platform(1)},
		Store: store,
		Sink:  logsink.New(),
	})
	rec := serveRuntimeLogsJSON(t, handler, http.MethodGet,
		"/v1/admin/ops/runtime-logs?level=error&component=billing&request_id=req-1&before_id=100&limit=50", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	want := logsink.ListParams{Level: "error", Component: "billing", RequestID: "req-1", BeforeID: 100, Limit: 50}
	if store.gotParams != want {
		t.Fatalf("过滤参数未透传: %+v", store.gotParams)
	}
	if !strings.Contains(rec.Body.String(), `"next_before_id":40`) {
		t.Fatalf("键集游标应为末行 id: %s", rec.Body.String())
	}
	// 非法参数拒绝(变异:砍校验 → 红)。
	for _, bad := range []string{"?level=info", "?before_id=0", "?limit=501"} {
		rec = serveRuntimeLogsJSON(t, handler, http.MethodGet, "/v1/admin/ops/runtime-logs"+bad, "")
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%s 应 400, got %d", bad, rec.Code)
		}
	}
}

// 清理:RFC3339 校验 + before 透传 + 删除数回显 + 必留管理审计行。
func TestRuntimeLogsCleanup(t *testing.T) {
	store := &runtimeLogStoreStub{deleted: 7}
	audit := &runtimeLogsAuditStub{}
	handler := newRuntimeLogsTestRouter(AdminRuntimeLogsDeps{
		Auth:  runtimeLogsAuthStub{ident: admintest.Platform(1)},
		Store: store,
		Sink:  logsink.New(),
		Audit: audit,
	})
	rec := serveRuntimeLogsJSON(t, handler, http.MethodPost, "/v1/admin/ops/runtime-logs/cleanup", `{"before":"2026-07-01T00:00:00Z"}`)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"deleted":7`) {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if !store.gotCleanup.Equal(time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("before 未透传: %v", store.gotCleanup)
	}
	if len(audit.events) != 1 || audit.events[0].Action != "cleanup_runtime_logs" || audit.events[0].TargetType != "runtime_logs" {
		t.Fatalf("清理必须落审计行: %+v", audit.events)
	}
	rec = serveRuntimeLogsJSON(t, handler, http.MethodPost, "/v1/admin/ops/runtime-logs/cleanup", `{"before":"not-a-time"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("非法时间应 400, got %d", rec.Code)
	}
}

// 审计先行:审计写失败/未接线 → 拒绝删除,store 不得被触达(变异:先删后审 → 红)。
func TestRuntimeLogsCleanupAuditFirst(t *testing.T) {
	store := &runtimeLogStoreStub{deleted: 7}
	handler := newRuntimeLogsTestRouter(AdminRuntimeLogsDeps{
		Auth:  runtimeLogsAuthStub{ident: admintest.Platform(1)},
		Store: store,
		Sink:  logsink.New(),
		Audit: &runtimeLogsAuditStub{err: errors.New("audit backend down")},
	})
	rec := serveRuntimeLogsJSON(t, handler, http.MethodPost, "/v1/admin/ops/runtime-logs/cleanup", `{"before":"2026-07-01T00:00:00Z"}`)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("审计失败应 503, got %d", rec.Code)
	}
	if !store.gotCleanup.IsZero() {
		t.Fatal("审计失败时不得执行删除")
	}

	noAudit := newRuntimeLogsTestRouter(AdminRuntimeLogsDeps{
		Auth:  runtimeLogsAuthStub{ident: admintest.Platform(1)},
		Store: store,
		Sink:  logsink.New(),
	})
	rec = serveRuntimeLogsJSON(t, noAudit, http.MethodPost, "/v1/admin/ops/runtime-logs/cleanup", `{"before":"2026-07-01T00:00:00Z"}`)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("审计依赖未接线应 503, got %d", rec.Code)
	}
}

// 健康:sink 计数回显(队列积压/入库/丢弃)。
func TestRuntimeLogsHealth(t *testing.T) {
	sink := logsink.New(logsink.WithQueueSize(4))
	sink.Enqueue(logsink.Entry{Level: "warn", Message: "queued"})
	handler := newRuntimeLogsTestRouter(AdminRuntimeLogsDeps{
		Auth:  runtimeLogsAuthStub{ident: admintest.Platform(1)},
		Store: &runtimeLogStoreStub{},
		Sink:  sink,
	})
	rec := serveRuntimeLogsJSON(t, handler, http.MethodGet, "/v1/admin/ops/runtime-logs/health", "")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"queue_len":1`) {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func serveRuntimeLogsJSON(t *testing.T, h http.Handler, method, target, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}
