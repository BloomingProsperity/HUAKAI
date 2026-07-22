package runtimeloghttp

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
	admindb "github.com/BloomingProsperity/HUAKAI/internal/db/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/logretention"
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
	rows      []logsink.RuntimeLogRow
	gotParams logsink.ListParams
	err       error
}

func (s *runtimeLogStoreStub) ListRuntimeLogs(_ context.Context, p logsink.ListParams) ([]logsink.RuntimeLogRow, error) {
	s.gotParams = p
	return s.rows, s.err
}

type runtimeLogRetentionStub struct {
	result logretention.Result
	health logretention.Health
	err    error
	calls  int
}

func (s *runtimeLogRetentionStub) RunOnce(context.Context) (logretention.Result, error) {
	s.calls++
	return s.result, s.err
}

func (s *runtimeLogRetentionStub) Health() logretention.Health {
	return s.health
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
		Auth:      runtimeLogsAuthStub{ident: admin.AdminIdentity{TokenID: 1, Role: admin.RoleTenantOperator, ScopeTenantID: 7}},
		Store:     &runtimeLogStoreStub{},
		Sink:      logsink.New(),
		Retention: &runtimeLogRetentionStub{},
		Audit:     &runtimeLogsAuditStub{},
	})
	for _, probe := range []struct{ method, path, body string }{
		{http.MethodGet, "/v1/admin/ops/runtime-logs", ""},
		{http.MethodPost, "/v1/admin/ops/runtime-logs/cleanup", `{"confirm":true}`},
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
		Auth:      runtimeLogsAuthStub{ident: admin.AdminIdentity{TokenID: 1, Role: admin.RolePlatformAdmin}},
		Store:     store,
		Sink:      logsink.New(),
		Retention: &runtimeLogRetentionStub{},
	})
	rec := serveRuntimeLogsJSON(t, handler, http.MethodGet,
		"/v1/admin/ops/runtime-logs?level=error&category=financial&event_type=billing.refund_failed"+
			"&result=server_failure&error_class=dependency&error_code=billing_store_down"+
			"&component=billing&actor_kind=system&tenant_id=7&request_id=req-1&trace_id=trace-1"+
			"&upstream_request_id=up-1&idempotency_key=idem-1&recovery_state=retrying&before_id=100&limit=50", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	want := logsink.ListParams{
		Level: "error", Category: "financial", EventType: "billing.refund_failed",
		Result: "server_failure", ErrorClass: "dependency", ErrorCode: "billing_store_down",
		Component: "billing", ActorKind: "system", TenantID: 7, RequestID: "req-1",
		TraceID: "trace-1", UpstreamRequestID: "up-1", IdempotencyKey: "idem-1",
		RecoveryState: "retrying", BeforeID: 100, Limit: 50,
	}
	if store.gotParams != want {
		t.Fatalf("过滤参数未透传: %+v", store.gotParams)
	}
	if !strings.Contains(rec.Body.String(), `"next_before_id":40`) {
		t.Fatalf("键集游标应为末行 id: %s", rec.Body.String())
	}
	// 非法参数拒绝(变异:砍校验 → 红)。
	for _, bad := range []string{
		"?level=debug", "?category=made_up", "?event_type=Bad%20Event", "?result=maybe",
		"?error_class=made_up", "?error_code=BAD", "?actor_kind=owner", "?tenant_id=0",
		"?recovery_state=done", "?before_id=0", "?limit=501", "?categry=error",
		"?category=error&category=access",
	} {
		rec = serveRuntimeLogsJSON(t, handler, http.MethodGet, "/v1/admin/ops/runtime-logs"+bad, "")
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%s 应 400, got %d", bad, rec.Code)
		}
	}
}

func TestRuntimeLogsListDoesNotExposeDatabaseError(t *testing.T) {
	store := &runtimeLogStoreStub{err: errors.New("pq: password=secret host=internal-db")}
	handler := newRuntimeLogsTestRouter(AdminRuntimeLogsDeps{
		Auth:      runtimeLogsAuthStub{ident: admin.AdminIdentity{TokenID: 1, Role: admin.RolePlatformAdmin}},
		Store:     store,
		Sink:      logsink.New(),
		Retention: &runtimeLogRetentionStub{},
	})
	rec := serveRuntimeLogsJSON(t, handler, http.MethodGet, "/v1/admin/ops/runtime-logs", "")
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("查询失败应 503, got %d", rec.Code)
	}
	for _, forbidden := range []string{"password", "secret", "internal-db"} {
		if strings.Contains(rec.Body.String(), forbidden) {
			t.Fatalf("响应泄露数据库细节 %q: %s", forbidden, rec.Body.String())
		}
	}
}

// 清理只接受显式确认并执行固定 30 天策略；任意 cutoff 字段必须被严格 JSON 拒绝。
func TestRuntimeLogsCleanup(t *testing.T) {
	store := &runtimeLogStoreStub{}
	cutoff := time.Date(2026, 6, 18, 0, 0, 0, 0, time.UTC)
	retention := &runtimeLogRetentionStub{result: logretention.Result{
		RetentionDays: 30, Cutoff: cutoff, Deleted: 7, ByTable: map[string]int64{"ops_runtime_logs": 7},
	}}
	audit := &runtimeLogsAuditStub{}
	handler := newRuntimeLogsTestRouter(AdminRuntimeLogsDeps{
		Auth:      runtimeLogsAuthStub{ident: admin.AdminIdentity{TokenID: 1, Role: admin.RolePlatformAdmin}},
		Store:     store,
		Sink:      logsink.New(),
		Retention: retention,
		Audit:     audit,
	})
	rec := serveRuntimeLogsJSON(t, handler, http.MethodPost, "/v1/admin/ops/runtime-logs/cleanup", `{"confirm":true}`)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"deleted":7`) {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if retention.calls != 1 || !strings.Contains(rec.Body.String(), `"retention_days":30`) {
		t.Fatalf("必须执行固定保留器: calls=%d body=%s", retention.calls, rec.Body.String())
	}
	if len(audit.events) != 1 || audit.events[0].Action != "cleanup_runtime_logs" || audit.events[0].TargetType != "runtime_logs" {
		t.Fatalf("清理必须落审计行: %+v", audit.events)
	}
	rec = serveRuntimeLogsJSON(t, handler, http.MethodPost, "/v1/admin/ops/runtime-logs/cleanup", `{"before":"2026-07-01T00:00:00Z","confirm":true}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("任意 before 必须被拒绝, got %d", rec.Code)
	}
	rec = serveRuntimeLogsJSON(t, handler, http.MethodPost, "/v1/admin/ops/runtime-logs/cleanup", `{"confirm":false}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("未确认应 400, got %d", rec.Code)
	}
}

// 审计先行:审计写失败/未接线 → 拒绝删除,store 不得被触达(变异:先删后审 → 红)。
func TestRuntimeLogsCleanupAuditFirst(t *testing.T) {
	store := &runtimeLogStoreStub{}
	retention := &runtimeLogRetentionStub{result: logretention.Result{Deleted: 7}}
	handler := newRuntimeLogsTestRouter(AdminRuntimeLogsDeps{
		Auth:      runtimeLogsAuthStub{ident: admin.AdminIdentity{TokenID: 1, Role: admin.RolePlatformAdmin}},
		Store:     store,
		Sink:      logsink.New(),
		Retention: retention,
		Audit:     &runtimeLogsAuditStub{err: errors.New("audit backend down")},
	})
	rec := serveRuntimeLogsJSON(t, handler, http.MethodPost, "/v1/admin/ops/runtime-logs/cleanup", `{"confirm":true}`)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("审计失败应 503, got %d", rec.Code)
	}
	if retention.calls != 0 {
		t.Fatal("审计失败时不得执行删除")
	}

	noAudit := newRuntimeLogsTestRouter(AdminRuntimeLogsDeps{
		Auth:      runtimeLogsAuthStub{ident: admin.AdminIdentity{TokenID: 1, Role: admin.RolePlatformAdmin}},
		Store:     store,
		Sink:      logsink.New(),
		Retention: retention,
	})
	rec = serveRuntimeLogsJSON(t, noAudit, http.MethodPost, "/v1/admin/ops/runtime-logs/cleanup", `{"confirm":true}`)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("审计依赖未接线应 503, got %d", rec.Code)
	}
}

// 健康:sink 计数回显(队列积压/入库/丢弃)。
func TestRuntimeLogsHealth(t *testing.T) {
	sink := logsink.New(logsink.WithQueueSize(4))
	sink.Enqueue(logsink.Entry{Level: "warn", Message: "queued"})
	handler := newRuntimeLogsTestRouter(AdminRuntimeLogsDeps{
		Auth:      runtimeLogsAuthStub{ident: admin.AdminIdentity{TokenID: 1, Role: admin.RolePlatformAdmin}},
		Store:     &runtimeLogStoreStub{},
		Sink:      sink,
		Retention: &runtimeLogRetentionStub{health: logretention.Health{RetentionDays: 30}},
	})
	rec := serveRuntimeLogsJSON(t, handler, http.MethodGet, "/v1/admin/ops/runtime-logs/health", "")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"queue_len":1`) ||
		!strings.Contains(rec.Body.String(), `"retention_days":30`) {
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
