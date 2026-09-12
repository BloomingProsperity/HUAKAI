package poolhealthhttp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	admindb "github.com/BloomingProsperity/HUAKAI/internal/db/admin"
)

type authStub struct {
	ident admin.AdminIdentity
	err   error
}

func (s authStub) Resolve(context.Context, *http.Request) (admin.AdminIdentity, error) {
	if s.err != nil {
		return admin.AdminIdentity{}, s.err
	}
	return s.ident, nil
}

// storeStub 按租户返回固定行;记录被查询的租户,让测试能断言越权/400 时根本没打数据库。
type storeStub struct {
	tenants    map[int64]bool
	rows       map[int64][]admindb.SummarizeProviderAccountHealthByPoolRow
	unpooled   map[int64]int64
	queried    []int64
	tenantErr  error
	summaryErr error
	unpoolErr  error
}

func (s *storeStub) GetPoolHealthTenant(_ context.Context, tenantID int64) (int64, error) {
	s.queried = append(s.queried, tenantID)
	if s.tenantErr != nil {
		return 0, s.tenantErr
	}
	if !s.tenants[tenantID] {
		return 0, pgx.ErrNoRows
	}
	return tenantID, nil
}

func (s *storeStub) SummarizeProviderAccountHealthByPool(_ context.Context, tenantID int64) ([]admindb.SummarizeProviderAccountHealthByPoolRow, error) {
	if s.summaryErr != nil {
		return nil, s.summaryErr
	}
	return s.rows[tenantID], nil
}

func (s *storeStub) CountUnpooledProviderAccounts(_ context.Context, tenantID int64) (int64, error) {
	if s.unpoolErr != nil {
		return 0, s.unpoolErr
	}
	return s.unpooled[tenantID], nil
}

var fixedNow = time.Date(2026, 9, 12, 6, 0, 0, 0, time.UTC)

func platformAdmin() admin.AdminIdentity {
	return admin.AdminIdentity{Role: admin.RolePlatformAdmin, UserID: 1}
}

func tenantOperator(tenantID int64) admin.AdminIdentity {
	return admin.AdminIdentity{Role: admin.RoleTenantOperator, ScopeTenantID: tenantID, UserID: 2}
}

func ts(t time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: t, Valid: true}
}

func samplePool(id int64, name string) admindb.SummarizeProviderAccountHealthByPoolRow {
	// 8 个账号:4 可调度、1 降级、2 冷却(最早已知恢复 +10m)、1 不可用;其中 1 个账号的栏位由
	// auth 降级车道决定(SQL 已折算进 cooling_down/unavailable,这里只回传计数)。
	return admindb.SummarizeProviderAccountHealthByPoolRow{
		PoolGroupID:          id,
		PoolName:             name,
		PoolEnabled:          true,
		TotalAccounts:        8,
		SchedulableAccounts:  4,
		DegradedAccounts:     1,
		CoolingDownAccounts:  2,
		UnavailableAccounts:  1,
		AuthCooldownAccounts: 1,
		EarliestRecoveryAt:   ts(fixedNow.Add(10 * time.Minute)),
	}
}

func newStore() *storeStub {
	return &storeStub{
		tenants: map[int64]bool{7: true, 9: true},
		rows: map[int64][]admindb.SummarizeProviderAccountHealthByPoolRow{
			7: {samplePool(1, "primary"), {PoolGroupID: 2, PoolName: "spare", PoolEnabled: false}},
			9: {samplePool(5, "other-tenant")},
		},
		unpooled: map[int64]int64{7: 3, 9: 0},
	}
}

func do(t *testing.T, d Deps, target string) (*httptest.ResponseRecorder, Response) {
	t.Helper()
	if d.Now == nil {
		d.Now = func() time.Time { return fixedNow }
	}
	rec := httptest.NewRecorder()
	NewHandler(d).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, target, nil))
	var body Response
	if rec.Code == http.StatusOK {
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("解析响应失败: %v; body=%s", err, rec.Body.String())
		}
	}
	return rec, body
}

func expectError(t *testing.T, rec *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	if rec.Code != status || !strings.Contains(rec.Body.String(), `"code":"`+code+`"`) {
		t.Fatalf("状态/错误码=%d/%s，期望=%d/%s", rec.Code, rec.Body.String(), status, code)
	}
}

// 无车道叠加时,投影必须逐列等于数据库分栏;占比按分母四位小数;
// 停用池状态 disabled 且 0/0 占比不产生 NaN;totals 跨池合计,unpooled 单列。
func TestHandlerProjectsSQLColumns(t *testing.T) {
	store := newStore()
	rec, body := do(t, Deps{Auth: authStub{ident: platformAdmin()}, Store: store}, "/?tenant_id=7")
	if rec.Code != http.StatusOK {
		t.Fatalf("状态=%d body=%s", rec.Code, rec.Body.String())
	}
	if body.TenantID != 7 || body.GeneratedAt != "2026-09-12T06:00:00Z" || body.UnpooledAccounts != 3 {
		t.Fatalf("头部字段不符: %+v", body)
	}
	if len(body.Pools) != 2 {
		t.Fatalf("池数=%d，期望 2", len(body.Pools))
	}
	p := body.Pools[0]
	if p.PoolGroupID != 1 || p.Status != PoolStatusDegraded || p.TotalAccounts != 8 ||
		p.SchedulableAccounts != 4 || p.DegradedAccounts != 1 || p.CoolingDownAccounts != 2 || p.UnavailableAccounts != 1 ||
		p.AuthCooldownAccounts != 1 {
		t.Fatalf("池 1 计数不符: %+v", p)
	}
	if p.SchedulableRatio != "0.5000" || p.DegradedRatio != "0.1250" || p.CoolingDownRatio != "0.2500" || p.UnavailableRatio != "0.1250" {
		t.Fatalf("池 1 占比不符: %+v", p)
	}
	if p.EarliestRecoveryAt == nil || *p.EarliestRecoveryAt != "2026-09-12T06:10:00Z" {
		t.Fatalf("最早恢复时刻不符: %v", p.EarliestRecoveryAt)
	}
	spare := body.Pools[1]
	if spare.Status != PoolStatusDisabled || spare.TotalAccounts != 0 || spare.SchedulableRatio != "0.0000" || spare.EarliestRecoveryAt != nil {
		t.Fatalf("停用空池投影不符: %+v", spare)
	}
	if body.Totals.Pools != 2 || body.Totals.TotalAccounts != 8 || body.Totals.SchedulableAccounts != 4 ||
		body.Totals.CoolingDownAccounts != 2 || body.Totals.UnavailableAccounts != 1 || body.Totals.AuthCooldownAccounts != 1 {
		t.Fatalf("totals 不符: %+v", body.Totals)
	}
	// 响应只含池标识与计数:任何账号 id 数组都不得出现在响应体里。
	for _, leaked := range []string{"schedulable_ids", "degraded_ids", "cooling_ids", "cooling_recovery_at", "cooling_exempt_ids", "101", "105", "106"} {
		if strings.Contains(rec.Body.String(), leaked) {
			t.Fatalf("响应泄漏账号级字段 %q: %s", leaked, rec.Body.String())
		}
	}
}

// 恢复时刻只在仍有冷却账号且 SQL 给出已知时刻时输出:冷却账号恢复时刻全部未知(NULL)→ 省略;
// 没有冷却账号即使 SQL 带了时刻也不输出(不存在需要等待的账号)。
func TestRecoveryTimeOnlyWhenKnownAndCooling(t *testing.T) {
	store := newStore()
	unknown := samplePool(1, "primary")
	unknown.EarliestRecoveryAt = pgtype.Timestamptz{}
	store.rows[7] = []admindb.SummarizeProviderAccountHealthByPoolRow{unknown}
	_, body := do(t, Deps{Auth: authStub{ident: platformAdmin()}, Store: store}, "/?tenant_id=7")
	if body.Pools[0].CoolingDownAccounts != 2 || body.Pools[0].EarliestRecoveryAt != nil {
		t.Fatalf("恢复时刻未知时不得输出: %+v", body.Pools[0])
	}
	none := samplePool(1, "primary")
	none.CoolingDownAccounts = 0
	none.UnavailableAccounts = 3
	store.rows[7] = []admindb.SummarizeProviderAccountHealthByPoolRow{none}
	_, body = do(t, Deps{Auth: authStub{ident: platformAdmin()}, Store: store}, "/?tenant_id=7")
	if body.Pools[0].EarliestRecoveryAt != nil {
		t.Fatalf("没有冷却账号时不得输出恢复时刻: %+v", body.Pools[0])
	}
}

func TestPoolStatusEscalation(t *testing.T) {
	all := PoolHealth{PoolEnabled: true, TotalAccounts: 3, SchedulableAccounts: 3}
	if got := poolStatus(all); got != PoolStatusHealthy {
		t.Fatalf("全可调度状态=%s，期望 healthy", got)
	}
	none := PoolHealth{PoolEnabled: true, TotalAccounts: 3, UnavailableAccounts: 2, CoolingDownAccounts: 1}
	if got := poolStatus(none); got != PoolStatusUnavailable {
		t.Fatalf("无一可调度状态=%s，期望 unavailable", got)
	}
	partial := PoolHealth{PoolEnabled: true, TotalAccounts: 3, SchedulableAccounts: 0, DegradedAccounts: 1, UnavailableAccounts: 2}
	if got := poolStatus(partial); got != PoolStatusDegraded {
		t.Fatalf("仅剩降级账号状态=%s，期望 degraded", got)
	}
	disabled := PoolHealth{PoolEnabled: false, TotalAccounts: 3, SchedulableAccounts: 3}
	if got := poolStatus(disabled); got != PoolStatusDisabled {
		t.Fatalf("人工停用必须优先于子单元事实,状态=%s", got)
	}
	if got := poolStatus(PoolHealth{PoolEnabled: true}); got != PoolStatusEmpty {
		t.Fatalf("空池状态=%s，期望 empty", got)
	}
}

// 租户管理员省略 tenant_id 锁定本租户;显式自报他人租户 → 403 且不查库;
// 显式自报本租户 → 200。
func TestTenantOperatorScope(t *testing.T) {
	store := newStore()
	rec, body := do(t, Deps{Auth: authStub{ident: tenantOperator(9)}, Store: store}, "/")
	if rec.Code != http.StatusOK || body.TenantID != 9 || len(body.Pools) != 1 || body.Pools[0].PoolGroupID != 5 {
		t.Fatalf("租户管理员默认应锁定本租户 9: 状态=%d body=%s", rec.Code, rec.Body.String())
	}
	before := len(store.queried)
	rec, _ = do(t, Deps{Auth: authStub{ident: tenantOperator(9)}, Store: store}, "/?tenant_id=7")
	expectError(t, rec, http.StatusForbidden, "admin_forbidden")
	if len(store.queried) != before {
		t.Fatalf("越权请求不得触达数据库,查询记录=%v", store.queried)
	}
	rec, body = do(t, Deps{Auth: authStub{ident: tenantOperator(9)}, Store: store}, "/?tenant_id=9")
	if rec.Code != http.StatusOK || body.TenantID != 9 {
		t.Fatalf("显式自报本租户应放行: 状态=%d", rec.Code)
	}
}

// 部署者省略/空串/空白/别名/非正整数 → 400 且不查库;不存在的租户 → 404;鉴权失败 → 401/503。
func TestPlatformAdminFailClosed(t *testing.T) {
	store := newStore()
	cases := map[string]string{
		"/":                      "tenant_id_required",
		"/?tenant_id=":           "tenant_id_required",
		"/?tenant_id=%20%20":     "tenant_id_required",
		"/?tenant_id=all":        "invalid_tenant_id",
		"/?tenant_id=*":          "invalid_tenant_id",
		"/?tenant_id=null":       "invalid_tenant_id",
		"/?tenant_id=0":          "invalid_tenant_id",
		"/?tenant_id=-1":         "invalid_tenant_id",
		"/?tenant_id=7,9":        "invalid_tenant_id",
		"/?tenant_id=7&x=%5B%5D": "",
	}
	for target, code := range cases {
		rec, _ := do(t, Deps{Auth: authStub{ident: platformAdmin()}, Store: store}, target)
		if code == "" {
			if rec.Code != http.StatusOK {
				t.Fatalf("%s: 合法请求状态=%d", target, rec.Code)
			}
			continue
		}
		expectError(t, rec, http.StatusBadRequest, code)
	}
	if len(store.queried) != 1 {
		t.Fatalf("只有合法请求应触达数据库,查询记录=%v", store.queried)
	}
	rec, _ := do(t, Deps{Auth: authStub{ident: platformAdmin()}, Store: store}, "/?tenant_id=424242")
	expectError(t, rec, http.StatusNotFound, "tenant_not_found")
	rec, _ = do(t, Deps{Auth: authStub{err: admin.ErrAdminUnauthorized}, Store: store}, "/?tenant_id=7")
	expectError(t, rec, http.StatusUnauthorized, "admin_unauthorized")
	rec, _ = do(t, Deps{Auth: authStub{err: admin.ErrAdminBackend}, Store: store}, "/?tenant_id=7")
	expectError(t, rec, http.StatusServiceUnavailable, "admin_backend_error")
}

// 真相不可用必须 503,不得把部分失败压成 200 空投影;Store 未接线同样 503。
func TestQueryFailuresAreFailClosed(t *testing.T) {
	rec, _ := do(t, Deps{Auth: authStub{ident: platformAdmin()}}, "/?tenant_id=7")
	expectError(t, rec, http.StatusServiceUnavailable, "gateway_not_configured")

	boom := errors.New("connection refused")
	for name, store := range map[string]*storeStub{
		"租户门失败":   {tenants: map[int64]bool{7: true}, tenantErr: boom},
		"聚合失败":    {tenants: map[int64]bool{7: true}, summaryErr: boom},
		"未挂池计数失败": {tenants: map[int64]bool{7: true}, unpoolErr: boom},
	} {
		rec, _ := do(t, Deps{Auth: authStub{ident: platformAdmin()}, Store: store}, "/?tenant_id=7")
		if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), "pool_health_query_failed") {
			t.Fatalf("%s: 状态=%d body=%s，期望 503 pool_health_query_failed", name, rec.Code, rec.Body.String())
		}
	}
}

// 租户存在但没有任何池 → 200 空列表,而不是 404/403。
func TestEmptyTenantReturnsEmptyProjection(t *testing.T) {
	store := &storeStub{tenants: map[int64]bool{11: true}}
	rec, body := do(t, Deps{Auth: authStub{ident: platformAdmin()}, Store: store}, "/?tenant_id=11")
	if rec.Code != http.StatusOK || body.Pools == nil || len(body.Pools) != 0 || body.Totals.Pools != 0 {
		t.Fatalf("空租户应返回空投影: 状态=%d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"pools":[]`) {
		t.Fatalf("空池列表必须序列化为 [] 而不是 null: %s", rec.Body.String())
	}
}
