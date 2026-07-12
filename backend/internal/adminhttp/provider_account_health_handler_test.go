package adminhttp

import (
	"context"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	admindb "github.com/BloomingProsperity/HUAKAI/internal/db/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/recentreq"
)

func TestProviderAccountHealthUnauthorized(t *testing.T) {
	store := newProviderAccountHealthStoreStub()

	rec := invokeProviderAccountHealth(t, ProviderAccountHealthDeps{
		Auth:  providerAccountHealthAuthStub{err: admin.ErrAdminUnauthorized},
		Store: store,
	}, "/admin/v1/provider-accounts/99/health")

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if len(store.getArgs) != 0 {
		t.Fatalf("unauthorized request touched store: %+v", store.getArgs)
	}
}

func TestProviderAccountHealthTenantScopeIgnoresQueryTenantID(t *testing.T) {
	// 判别防串租户:query tenant_id=8 必须被忽略,查询只能使用 admin identity 的 tenant 7。
	// 变异:从 query/body 收 tenant_id 或漏 tenant predicate 时会命中 tenant 8 row 并返回 200。
	store := newProviderAccountHealthStoreStub()
	store.put(providerAccountHealthRow(8, 200))

	rec := invokeProviderAccountHealth(t, ProviderAccountHealthDeps{
		Auth:  providerAccountHealthAuthStub{ident: tenantOperator(7)},
		Store: store,
	}, "/admin/v1/provider-accounts/200/health?tenant_id=8")

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if len(store.getArgs) != 1 || store.getArgs[0].TenantID != 7 || store.getArgs[0].ID != 200 {
		t.Fatalf("GetAdminProviderAccountHealth args=%+v, want tenant scoped lookup tenant=7 id=200", store.getArgs)
	}
	if strings.Contains(rec.Body.String(), "tenant-8") || strings.Contains(rec.Body.String(), "8") {
		t.Fatalf("cross-tenant response leaked target tenant detail: %s", rec.Body.String())
	}
}

// TestProviderAccountHealthPlatformAdminRequiresExplicitTenant 守护 #9(health handler 侧):
// 全局 platform_admin 不再被静默锁死到 tenant 1——无 ?tenant_id → 400 不触 store;?tenant_id=7 →
// 按 tenant 7 解析(够得到 tenant>1 的账号)。与 test handler 的同名用例对称,防三 handler 未来独立漂移。
// 判别:resolver 退回硬编码 tenant 1 时,(a) 无 query 落 tenant 1 → get(1,200) miss → 404 而非 400;
// (b) ?tenant_id=7 仍解析成 tenant 1 → 404 而非 200。
func TestProviderAccountHealthPlatformAdminRequiresExplicitTenant(t *testing.T) {
	store := newProviderAccountHealthStoreStub()
	store.put(providerAccountHealthRow(7, 200)) // 行位于 tenant 7(非 1)
	deps := ProviderAccountHealthDeps{
		Auth:  providerAccountHealthAuthStub{ident: admin.AdminIdentity{TokenID: 4, Role: admin.RolePlatformAdmin}},
		Store: store,
	}

	// (a) 不带 ?tenant_id → 400,且不触达 store。
	rec := invokeProviderAccountHealth(t, deps, "/admin/v1/provider-accounts/200/health")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("platform_admin 不带 ?tenant_id 应 400, status=%d body=%s", rec.Code, rec.Body.String())
	}
	if len(store.getArgs) != 0 {
		t.Fatalf("400 请求不应触达 store, getArgs=%+v", store.getArgs)
	}

	// (b) 带 ?tenant_id=7 → 200,且按 tenant 7 解析。
	rec2 := invokeProviderAccountHealth(t, deps, "/admin/v1/provider-accounts/200/health?tenant_id=7")
	if rec2.Code != http.StatusOK {
		t.Fatalf("platform_admin 带 ?tenant_id=7 应 200, status=%d body=%s", rec2.Code, rec2.Body.String())
	}
	if len(store.getArgs) != 1 || store.getArgs[0].TenantID != 7 || store.getArgs[0].ID != 200 {
		t.Fatalf("应按 ?tenant_id=7 解析 tenant, getArgs=%+v", store.getArgs)
	}
}

func TestProviderAccountHealthResponseContainsOnlySafeSnapshotFields(t *testing.T) {
	store := newProviderAccountHealthStoreStub()
	row := providerAccountHealthRow(7, 99)
	row.HealthStateUntil = pgTimestamp(time.Date(2026, 6, 2, 12, 10, 0, 0, time.UTC))
	row.LastProbeAt = pgTimestamp(time.Date(2026, 6, 2, 12, 9, 0, 0, time.UTC))
	latencyMS := int32(217)
	row.LastProbeLatencyMS = &latencyMS
	row.LastRefreshAt = pgTimestamp(time.Date(2026, 6, 2, 12, 0, 0, 0, time.UTC))
	outcome := "auth_expired"
	failureClass := "invalid_grant"
	row.LastRefreshOutcome = &outcome
	row.FailureClass = &failureClass
	row.FailureCount = 4
	store.put(row)

	rec := invokeProviderAccountHealth(t, ProviderAccountHealthDeps{
		Auth:  providerAccountHealthAuthStub{ident: tenantOperator(7)},
		Store: store,
	}, "/admin/v1/provider-accounts/99/health")

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var body map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v body=%s", err, rec.Body.String())
	}
	assertProviderAccountHealthKeys(t, body, []string{
		"enabled",
		"failure_class",
		"failure_count",
		"health_state",
		"health_state_until",
		"id",
		"last_probe_at",
		"last_probe_latency_ms",
		"last_refresh_at",
		"last_refresh_outcome",
		"model_sync_last_check_at",
		"requires_action",
		"session_window_5h_end",
		"session_window_5h_start",
		"session_window_5h_status",
		"session_window_5h_utilization",
		"session_window_7d_end",
		"session_window_7d_start",
		"session_window_7d_status",
		"session_window_7d_utilization",
		"updated_at",
	})
	forbiddenFragments := []string{"credential", "credentials", "encrypted", "payload", "secret", "token", "nonce", "key_id"}
	lowerBody := strings.ToLower(rec.Body.String())
	for _, fragment := range forbiddenFragments {
		if strings.Contains(lowerBody, fragment) {
			t.Fatalf("response leaked forbidden fragment %q: %s", fragment, rec.Body.String())
		}
	}
}

func TestProviderAccountHealthJoinsLatestRefreshMetadata(t *testing.T) {
	// 判别 refresh join:health 来自 provider_accounts,refresh outcome/failure 来自最新凭据。
	// 变异:漏掉 account_credentials join 或选旧 credential_version,这些断言会红。
	store := newProviderAccountHealthStoreStub()
	row := providerAccountHealthRow(7, 101)
	row.HealthState = "throttled"
	row.HealthStateUntil = pgTimestamp(time.Date(2026, 6, 2, 12, 30, 0, 0, time.UTC))
	row.Enabled = false
	row.LastRefreshAt = pgTimestamp(time.Date(2026, 6, 2, 12, 1, 0, 0, time.UTC))
	outcome := "auth_expired"
	failureClass := "invalid_grant"
	row.LastRefreshOutcome = &outcome
	row.FailureClass = &failureClass
	row.FailureCount = 4
	row.UpdatedAt = pgTimestamp(time.Date(2026, 6, 2, 12, 2, 0, 0, time.UTC))
	store.put(row)

	rec := invokeProviderAccountHealth(t, ProviderAccountHealthDeps{
		Auth:  providerAccountHealthAuthStub{ident: tenantOperator(7)},
		Store: store,
	}, "/admin/v1/provider-accounts/101/health")

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var body providerAccountHealthResponseBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v body=%s", err, rec.Body.String())
	}
	if body.ID != 101 || body.HealthState != "throttled" || body.HealthStateUntil == nil || *body.HealthStateUntil != "2026-06-02T12:30:00Z" {
		t.Fatalf("health fields=%+v, want throttled snapshot with deadline", body)
	}
	if body.Enabled {
		t.Fatalf("enabled=%v want false from provider account metadata", body.Enabled)
	}
	if body.LastRefreshAt == nil || *body.LastRefreshAt != "2026-06-02T12:01:00Z" {
		t.Fatalf("last_refresh_at=%v want latest credential refresh timestamp", body.LastRefreshAt)
	}
	if body.LastRefreshOutcome == nil || *body.LastRefreshOutcome != "auth_expired" {
		t.Fatalf("last_refresh_outcome=%v want auth_expired from latest credential", body.LastRefreshOutcome)
	}
	if body.FailureClass == nil || *body.FailureClass != "invalid_grant" || body.FailureCount != 4 {
		t.Fatalf("failure fields class=%v count=%d want invalid_grant/4", body.FailureClass, body.FailureCount)
	}
	if !body.RequiresAction {
		t.Fatalf("requires_action=false want true when failure_count > 3")
	}
}

func TestProviderAccountHealthResponseIncludesLastProbeSnapshot(t *testing.T) {
	store := newProviderAccountHealthStoreStub()
	row := providerAccountHealthRow(7, 102)
	latencyMS := int32(321)
	row.LastProbeLatencyMS = &latencyMS
	row.LastProbeAt = pgTimestamp(time.Date(2026, 6, 2, 12, 3, 4, 0, time.UTC))
	store.put(row)

	rec := invokeProviderAccountHealth(t, ProviderAccountHealthDeps{
		Auth:  providerAccountHealthAuthStub{ident: tenantOperator(7)},
		Store: store,
	}, "/admin/v1/provider-accounts/102/health")

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var body providerAccountHealthResponseBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v body=%s", err, rec.Body.String())
	}
	if body.LastProbeLatencyMS == nil || *body.LastProbeLatencyMS != 321 {
		t.Fatalf("last_probe_latency_ms=%v want 321", body.LastProbeLatencyMS)
	}
	if body.LastProbeAt == nil || *body.LastProbeAt != "2026-06-02T12:03:04Z" {
		t.Fatalf("last_probe_at=%v want 2026-06-02T12:03:04Z", body.LastProbeAt)
	}
}

func TestProviderAccountHealthResponseIncludesSyncAndSessionWindowSnapshot(t *testing.T) {
	store := newProviderAccountHealthStoreStub()
	row := providerAccountHealthRow(7, 103)
	row.ModelSyncLastCheckAt = pgTimestamp(time.Date(2026, 6, 2, 12, 4, 0, 0, time.UTC))
	row.SessionWindow5hStart = pgTimestamp(time.Date(2099, 6, 2, 8, 0, 0, 0, time.UTC))
	row.SessionWindow5hEnd = pgTimestamp(time.Date(2099, 6, 2, 13, 0, 0, 0, time.UTC))
	row.SessionWindow5hUtilization = pgNumeric(37.5)
	row.SessionWindow7dStart = pgTimestamp(time.Date(2099, 5, 27, 13, 0, 0, 0, time.UTC))
	row.SessionWindow7dEnd = pgTimestamp(time.Date(2099, 6, 3, 13, 0, 0, 0, time.UTC))
	row.SessionWindow7dUtilization = pgNumeric(62.25)
	status := "allowed"
	row.SessionWindow5hStatus = &status
	row.SessionWindow7dStatus = &status
	store.put(row)

	rec := invokeProviderAccountHealth(t, ProviderAccountHealthDeps{
		Auth:  providerAccountHealthAuthStub{ident: tenantOperator(7)},
		Store: store,
	}, "/admin/v1/provider-accounts/103/health")

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var body providerAccountHealthResponseBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v body=%s", err, rec.Body.String())
	}
	if body.ModelSyncLastCheckAt == nil || *body.ModelSyncLastCheckAt != "2026-06-02T12:04:00Z" {
		t.Fatalf("model_sync_last_check_at=%v want 2026-06-02T12:04:00Z", body.ModelSyncLastCheckAt)
	}
	if body.SessionWindow5hStart == nil || *body.SessionWindow5hStart != "2099-06-02T08:00:00Z" {
		t.Fatalf("session_window_5h_start=%v want 2099-06-02T08:00:00Z", body.SessionWindow5hStart)
	}
	if body.SessionWindow5hEnd == nil || *body.SessionWindow5hEnd != "2099-06-02T13:00:00Z" {
		t.Fatalf("session_window_5h_end=%v want 2099-06-02T13:00:00Z", body.SessionWindow5hEnd)
	}
	if body.SessionWindow5hStatus == nil || *body.SessionWindow5hStatus != "allowed" {
		t.Fatalf("session_window_5h_status=%v want allowed", body.SessionWindow5hStatus)
	}
	if body.SessionWindow5hUtilization == nil || *body.SessionWindow5hUtilization != 37.5 {
		t.Fatalf("session_window_5h_utilization=%v want 37.5", body.SessionWindow5hUtilization)
	}
	if body.SessionWindow7dStart == nil || *body.SessionWindow7dStart != "2099-05-27T13:00:00Z" ||
		body.SessionWindow7dEnd == nil || *body.SessionWindow7dEnd != "2099-06-03T13:00:00Z" ||
		body.SessionWindow7dStatus == nil || *body.SessionWindow7dStatus != "allowed" ||
		body.SessionWindow7dUtilization == nil || *body.SessionWindow7dUtilization != 62.25 {
		t.Fatalf("7d 窗口响应不一致：%+v", body)
	}
}

func TestProviderAccountHealthExpiredWindowHidesUtilization(t *testing.T) {
	now := time.Date(2026, 7, 12, 10, 0, 0, 0, time.UTC)
	status := "active"
	row := providerAccountHealthRow(7, 104)
	row.SessionWindow5hEnd = pgTimestamp(now.Add(-time.Second))
	row.SessionWindow5hStatus = &status
	row.SessionWindow5hUtilization = pgNumeric(87.5)
	row.SessionWindow7dEnd = pgTimestamp(now.Add(time.Hour))
	row.SessionWindow7dStatus = &status
	row.SessionWindow7dUtilization = pgNumeric(42.25)

	body := providerAccountHealthResponseAt(row, nil, now)
	if body.SessionWindow5hStatus == nil || *body.SessionWindow5hStatus != "expired" {
		t.Fatalf("过期 5h status=%v，期望 expired", body.SessionWindow5hStatus)
	}
	if body.SessionWindow5hUtilization != nil {
		t.Fatalf("过期 5h 利用率不得作为活数据返回：%v", body.SessionWindow5hUtilization)
	}
	if body.SessionWindow7dStatus == nil || *body.SessionWindow7dStatus != "active" ||
		body.SessionWindow7dUtilization == nil || *body.SessionWindow7dUtilization != 42.25 {
		t.Fatalf("未过期 7d 窗口被误隐藏：%+v", body)
	}
}

func invokeProviderAccountHealth(t *testing.T, deps ProviderAccountHealthDeps, target string) *httptest.ResponseRecorder {
	t.Helper()
	r := chi.NewRouter()
	r.Route("/admin/v1/provider-accounts", func(r chi.Router) {
		MountProviderAccountHealthRoutes(r, deps)
	})
	req := httptest.NewRequest(http.MethodGet, target, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func assertProviderAccountHealthKeys(t *testing.T, body map[string]json.RawMessage, want []string) {
	t.Helper()
	got := make([]string, 0, len(body))
	for key := range body {
		got = append(got, key)
	}
	sort.Strings(got)
	sort.Strings(want)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("response keys=%v want exactly %v", got, want)
	}
}

type providerAccountHealthAuthStub struct {
	ident admin.AdminIdentity
	err   error
}

func (s providerAccountHealthAuthStub) Resolve(context.Context, *http.Request) (admin.AdminIdentity, error) {
	if s.err != nil {
		return admin.AdminIdentity{}, s.err
	}
	return s.ident, nil
}

type providerAccountHealthStoreStub struct {
	rows    map[string]admindb.GetAdminProviderAccountHealthRow
	getArgs []admindb.GetAdminProviderAccountHealthParams
	err     error
}

func newProviderAccountHealthStoreStub() *providerAccountHealthStoreStub {
	return &providerAccountHealthStoreStub{rows: map[string]admindb.GetAdminProviderAccountHealthRow{}}
}

func (s *providerAccountHealthStoreStub) put(row admindb.GetAdminProviderAccountHealthRow) {
	s.rows[providerAccountHealthKey(row.TenantID, row.ID)] = row
}

func (s *providerAccountHealthStoreStub) GetAdminProviderAccountHealth(_ context.Context, arg admindb.GetAdminProviderAccountHealthParams) (admindb.GetAdminProviderAccountHealthRow, error) {
	s.getArgs = append(s.getArgs, arg)
	if s.err != nil {
		return admindb.GetAdminProviderAccountHealthRow{}, s.err
	}
	row, ok := s.rows[providerAccountHealthKey(arg.TenantID, arg.ID)]
	if !ok {
		return admindb.GetAdminProviderAccountHealthRow{}, pgx.ErrNoRows
	}
	return row, nil
}

func providerAccountHealthKey(tenantID, accountID int64) string {
	return strconv.FormatInt(tenantID, 10) + ":" + strconv.FormatInt(accountID, 10)
}

func TestProviderAccountHealthRecentRequestsPopulated(t *testing.T) {
	// 预先为账号 99 在 ring 中填入 3 次成功 + 1 次失败。
	ring := recentreq.NewRing()
	ring.Record(99, true)
	ring.Record(99, true)
	ring.Record(99, true)
	ring.Record(99, false)

	store := newProviderAccountHealthStoreStub()
	store.put(providerAccountHealthRow(7, 99))

	rec := invokeProviderAccountHealthWithRing(t, ProviderAccountHealthDeps{
		Auth:          providerAccountHealthAuthStub{ident: tenantOperator(7)},
		Store:         store,
		RecentReqRing: ring,
	}, "/admin/v1/provider-accounts/99/health")

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var body struct {
		RecentRequests *struct {
			Total   int    `json:"total"`
			Success int    `json:"success"`
			Failure int    `json:"failure"`
			LastAt  string `json:"last_at"`
		} `json:"recent_requests"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v body=%s", err, rec.Body.String())
	}
	if body.RecentRequests == nil {
		t.Fatalf("recent_requests absent, want populated")
	}
	if body.RecentRequests.Total != 4 {
		t.Fatalf("total=%d want 4", body.RecentRequests.Total)
	}
	if body.RecentRequests.Success != 3 {
		t.Fatalf("success=%d want 3", body.RecentRequests.Success)
	}
	if body.RecentRequests.Failure != 1 {
		t.Fatalf("failure=%d want 1", body.RecentRequests.Failure)
	}
	if body.RecentRequests.LastAt == "" {
		t.Fatalf("last_at empty, want RFC3339 timestamp")
	}
}

func TestProviderAccountHealthRecentRequestsNilRingOmitted(t *testing.T) {
	// ring 为 nil -> recent_requests 在 JSON 中缺省(omitempty)。
	store := newProviderAccountHealthStoreStub()
	store.put(providerAccountHealthRow(7, 99))

	rec := invokeProviderAccountHealth(t, ProviderAccountHealthDeps{
		Auth:  providerAccountHealthAuthStub{ident: tenantOperator(7)},
		Store: store,
		// RecentReqRing 故意置为 nil
	}, "/admin/v1/provider-accounts/99/health")

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "recent_requests") {
		t.Fatalf("recent_requests present in response with nil ring: %s", rec.Body.String())
	}
}

func TestProviderAccountHealthRecentRequestsEmptyRingOmitted(t *testing.T) {
	// ring 中没有该账号的数据 -> recent_requests 缺省。
	ring := recentreq.NewRing()
	// 为另一个不同的账号记录
	ring.Record(9999, true)

	store := newProviderAccountHealthStoreStub()
	store.put(providerAccountHealthRow(7, 99))

	rec := invokeProviderAccountHealthWithRing(t, ProviderAccountHealthDeps{
		Auth:          providerAccountHealthAuthStub{ident: tenantOperator(7)},
		Store:         store,
		RecentReqRing: ring,
	}, "/admin/v1/provider-accounts/99/health")

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "recent_requests") {
		t.Fatalf("recent_requests present when no data for account: %s", rec.Body.String())
	}
}

// invokeProviderAccountHealthWithRing 与 invokeProviderAccountHealth 类似,但
// 挂载在真实的路径模式上,使 handler 能够接收到 {id} URL 参数。
func invokeProviderAccountHealthWithRing(t *testing.T, deps ProviderAccountHealthDeps, target string) *httptest.ResponseRecorder {
	t.Helper()
	r := chi.NewRouter()
	r.Route("/admin/v1/provider-accounts", func(r chi.Router) {
		MountProviderAccountHealthRoutes(r, deps)
	})
	req := httptest.NewRequest(http.MethodGet, target, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func providerAccountHealthRow(tenantID, id int64) admindb.GetAdminProviderAccountHealthRow {
	return admindb.GetAdminProviderAccountHealthRow{
		ID:          id,
		TenantID:    tenantID,
		HealthState: "healthy",
		Enabled:     true,
		UpdatedAt:   pgTimestamp(time.Date(2026, 6, 2, 12, 0, 0, 0, time.UTC)),
	}
}

func pgNumeric(value float64) pgtype.Numeric {
	text := strconv.FormatFloat(value, 'f', -1, 64)
	point := strings.IndexByte(text, '.')
	exponent := int32(0)
	if point >= 0 {
		exponent = int32(-(len(text) - point - 1))
		text = strings.ReplaceAll(text, ".", "")
	}
	integer, ok := new(big.Int).SetString(text, 10)
	if !ok {
		panic("测试 numeric 构造失败")
	}
	return pgtype.Numeric{Int: integer, Exp: exponent, Valid: true}
}
