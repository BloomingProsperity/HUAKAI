//go:build integration_pg

package adminquotahttp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BloomingProsperity/HUAKAI/internal/db"
	reservequota "github.com/BloomingProsperity/HUAKAI/internal/db/quota"
)

func openQuotaPool(t *testing.T, ctx context.Context) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("HUAKAI_DATABASE_URL")
	if dsn == "" {
		t.Skip("HUAKAI_DATABASE_URL not set; skipping integration_pg")
	}
	pool, err := db.Open(ctx, db.PoolConfig{DSN: dsn})
	if err != nil {
		t.Fatalf("open PG: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

type quotaFixture struct {
	t        *testing.T
	ctx      context.Context
	pool     *pgxpool.Pool
	suffix   string
	tenantA  int64
	tenantB  int64
	policyDs Deps
}

func newQuotaFixture(t *testing.T, ctx context.Context, pool *pgxpool.Pool) *quotaFixture {
	t.Helper()
	f := &quotaFixture{t: t, ctx: ctx, pool: pool, suffix: uuid.NewString()}
	f.tenantA = f.seedTenant("a")
	f.tenantB = f.seedTenant("b")
	t.Cleanup(func() {
		c := context.Background()
		for _, tid := range []int64{f.tenantA, f.tenantB} {
			_, _ = pool.Exec(c, `DELETE FROM quota_windows WHERE tenant_id=$1`, tid)
			_, _ = pool.Exec(c, `DELETE FROM quota_policies WHERE tenant_id=$1`, tid)
			_, _ = pool.Exec(c, `DELETE FROM admin_audit_events WHERE tenant_id=$1`, tid)
			_, _ = pool.Exec(c, `DELETE FROM tenants WHERE id=$1`, tid)
		}
	})
	f.policyDs = Deps{
		Auth:  quotaAuthStub{ident: platformAdmin()},
		Store: NewQuotaPolicyStoreAdapter(pool),
	}
	return f
}

func (f *quotaFixture) seedTenant(label string) int64 {
	f.t.Helper()
	var id int64
	if err := f.pool.QueryRow(f.ctx,
		`INSERT INTO tenants (name) VALUES ($1) RETURNING id`,
		"admin-quota-"+label+"-"+f.suffix,
	).Scan(&id); err != nil {
		f.t.Fatalf("seed tenant %s: %v", label, err)
	}
	return id
}

func (f *quotaFixture) invoke(method, target, body string) *httptest.ResponseRecorder {
	f.t.Helper()
	r := NewRouter(f.policyDs)
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func (f *quotaFixture) createPolicy(tenantID int64, body string) quotaPolicyItem {
	f.t.Helper()
	rec := f.invoke(http.MethodPost, "/?tenant_id="+strconv.FormatInt(tenantID, 10), body)
	if rec.Code != http.StatusCreated {
		f.t.Fatalf("create policy: status=%d body=%s", rec.Code, strings.TrimSpace(rec.Body.String()))
	}
	var item quotaPolicyItem
	if err := json.Unmarshal(rec.Body.Bytes(), &item); err != nil {
		f.t.Fatalf("decode created policy: %v", err)
	}
	return item
}

// TestCreatePersistsAndAuditsRealStore 守住 policy 与审计事件在同一个事务持久化。
// HTTP 201 之外还必须存在与新 policy 标识一致的 create_quota_policy 审计行，
// 否则管理员无法追溯创建动作。
func TestCreatePersistsAndAuditsRealStore(t *testing.T) {
	ctx := context.Background()
	pool := openQuotaPool(t, ctx)
	f := newQuotaFixture(t, ctx, pool)

	item := f.createPolicy(f.tenantA,
		`{"scope_kind":"user","scope_id":"42","metric":"cost_usd","window_kind":"calendar_day","limit_value":"10.00000000","mode":"enforce"}`)
	if item.ID == 0 || item.LimitValue != "10" || item.Metric != "cost_usd" {
		t.Fatalf("created item wrong: %+v", item)
	}

	var sk, sid, metric, wk, mode string
	var limit string
	if err := pool.QueryRow(ctx,
		`SELECT scope_kind, scope_id, metric, window_kind, mode, limit_value::text
		   FROM quota_policies WHERE tenant_id=$1 AND id=$2`,
		f.tenantA, item.ID).Scan(&sk, &sid, &metric, &wk, &mode, &limit); err != nil {
		t.Fatalf("read created policy: %v", err)
	}
	if sk != "user" || sid != "42" || metric != "cost_usd" || wk != "calendar_day" || mode != "enforce" {
		t.Fatalf("persisted policy state wrong: %s/%s/%s/%s/%s", sk, sid, metric, wk, mode)
	}

	var action, targetType string
	var targetID int64
	if err := pool.QueryRow(ctx,
		`SELECT action, target_type, target_id FROM admin_audit_events
		  WHERE tenant_id=$1 AND target_id=$2 AND action='create_quota_policy'
		  ORDER BY id DESC LIMIT 1`,
		f.tenantA, item.ID).Scan(&action, &targetType, &targetID); err != nil {
		t.Fatalf("read create audit row (must be written atomically in tx): %v", err)
	}
	if action != "create_quota_policy" || targetType != "quota_policy" || targetID != item.ID {
		t.Fatalf("audit row wrong: action=%s type=%s id=%d", action, targetType, targetID)
	}
}

// TestCrossTenantGet404 守住 GetQuotaPolicyByID 的租户围栏：租户 A 的 policy
// 对租户 B 必须返回 404，响应体也不得泄露租户 A 的 scope 内容。
func TestCrossTenantGet404(t *testing.T) {
	ctx := context.Background()
	pool := openQuotaPool(t, ctx)
	f := newQuotaFixture(t, ctx, pool)

	item := f.createPolicy(f.tenantA,
		`{"scope_kind":"user","scope_id":"7","metric":"requests","window_kind":"none","limit_value":"100"}`)

	rec := f.invoke(http.MethodGet,
		"/"+strconv.FormatInt(item.ID, 10)+"?tenant_id="+strconv.FormatInt(f.tenantB, 10), "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("cross-tenant get status=%d want 404 body=%s", rec.Code, strings.TrimSpace(rec.Body.String()))
	}
	if strings.Contains(rec.Body.String(), `"scope_id":"7"`) {
		t.Fatalf("cross-tenant get leaked the tenant-A row: %s", rec.Body.String())
	}
}

// TestDeleteInUseRealFK 守住 quota_windows 占用 policy 时的 FK 约束语义：
// 接口必须返回 409 quota_policy_in_use，不得把可操作的资源冲突误报为 503。
func TestDeleteInUseRealFK(t *testing.T) {
	ctx := context.Background()
	pool := openQuotaPool(t, ctx)
	f := newQuotaFixture(t, ctx, pool)

	item := f.createPolicy(f.tenantA,
		`{"scope_kind":"user","scope_id":"9","metric":"cost_usd","window_kind":"calendar_day","limit_value":"5"}`)

	now := time.Now().UTC()
	if _, err := pool.Exec(ctx,
		`INSERT INTO quota_windows (tenant_id, policy_id, window_start, window_end)
		 VALUES ($1, $2, $3, $4)`,
		f.tenantA, item.ID, now.Add(-time.Hour), now.Add(time.Hour)); err != nil {
		t.Fatalf("seed quota window: %v", err)
	}

	rec := f.invoke(http.MethodDelete,
		"/"+strconv.FormatInt(item.ID, 10)+"?tenant_id="+strconv.FormatInt(f.tenantA, 10), "")
	if rec.Code != http.StatusConflict {
		t.Fatalf("delete-in-use status=%d want 409 body=%s", rec.Code, strings.TrimSpace(rec.Body.String()))
	}
	if code := errorCode(t, rec); code != "quota_policy_in_use" {
		t.Fatalf("delete-in-use code=%q want quota_policy_in_use", code)
	}
}

// TestDeleteCleanRealStore 守住未被 window 使用的 policy 可从主表移除，
// 同时写入一条 delete_quota_policy 审计行，且随后的 GET 返回 404。
func TestDeleteCleanRealStore(t *testing.T) {
	ctx := context.Background()
	pool := openQuotaPool(t, ctx)
	f := newQuotaFixture(t, ctx, pool)

	item := f.createPolicy(f.tenantA,
		`{"scope_kind":"global","scope_id":"*","metric":"requests","window_kind":"none","limit_value":"0"}`)

	idPath := "/" + strconv.FormatInt(item.ID, 10) + "?tenant_id=" + strconv.FormatInt(f.tenantA, 10)
	rec := f.invoke(http.MethodDelete, idPath, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("clean delete status=%d want 200 body=%s", rec.Code, strings.TrimSpace(rec.Body.String()))
	}

	var n int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM admin_audit_events
		  WHERE tenant_id=$1 AND target_id=$2 AND action='delete_quota_policy'`,
		f.tenantA, item.ID).Scan(&n); err != nil {
		t.Fatalf("read delete audit count: %v", err)
	}
	if n != 1 {
		t.Fatalf("delete audit rows=%d want 1", n)
	}

	getRec := f.invoke(http.MethodGet, idPath, "")
	if getRec.Code != http.StatusNotFound {
		t.Fatalf("get after delete status=%d want 404", getRec.Code)
	}
}

// TestUpdateEnabledTogglesReserveVisibility 守住禁用 policy 的双重可见性：
// admin List 仍能用于运维查看，reserve 读取器必须忽略该行；两类读取结果
// 相同会让禁用状态失去运维可见性或继续影响准入。
func TestUpdateEnabledTogglesReserveVisibility(t *testing.T) {
	ctx := context.Background()
	pool := openQuotaPool(t, ctx)
	f := newQuotaFixture(t, ctx, pool)

	item := f.createPolicy(f.tenantA,
		`{"scope_kind":"user","scope_id":"123","metric":"cost_usd","window_kind":"calendar_day","limit_value":"3","mode":"enforce","enabled":true}`)

	scopes := []byte(`[{"kind":"user","id":"123"}]`)
	reserve := reservequota.New(pool)
	before, err := reserve.ListActiveQuotaPoliciesForScopes(ctx, reservequota.ListActiveQuotaPoliciesForScopesParams{
		TenantID: f.tenantA, Scopes: scopes, Metrics: []string{"cost_usd"},
		AtTime: pgTimestamptz(time.Now().UTC()),
	})
	if err != nil {
		t.Fatalf("reserve read before: %v", err)
	}
	if !containsPolicyID(before, item.ID) {
		t.Fatalf("reserve read should see enabled policy before toggle")
	}

	// PUT 设置 enabled=false。
	idPath := "/" + strconv.FormatInt(item.ID, 10) + "?tenant_id=" + strconv.FormatInt(f.tenantA, 10)
	rec := f.invoke(http.MethodPut, idPath,
		`{"scope_kind":"user","scope_id":"123","metric":"cost_usd","window_kind":"calendar_day","limit_value":"3","mode":"enforce","enabled":false}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("disable PUT status=%d body=%s", rec.Code, strings.TrimSpace(rec.Body.String()))
	}

	// reserve 路径不再返回它。
	after, err := reserve.ListActiveQuotaPoliciesForScopes(ctx, reservequota.ListActiveQuotaPoliciesForScopesParams{
		TenantID: f.tenantA, Scopes: scopes, Metrics: []string{"cost_usd"},
		AtTime: pgTimestamptz(time.Now().UTC()),
	})
	if err != nil {
		t.Fatalf("reserve read after: %v", err)
	}
	if containsPolicyID(after, item.ID) {
		t.Fatalf("reserve read must NOT see disabled policy")
	}

	// admin list(忽略 enabled)仍然返回它。
	adminRec := f.invoke(http.MethodGet, "/?tenant_id="+strconv.FormatInt(f.tenantA, 10)+"&scope_kind=user&scope_id=123", "")
	if adminRec.Code != http.StatusOK {
		t.Fatalf("admin list status=%d", adminRec.Code)
	}
	var listBody quotaPolicyListResponse
	if err := json.Unmarshal(adminRec.Body.Bytes(), &listBody); err != nil {
		t.Fatalf("decode admin list: %v", err)
	}
	found := false
	for _, it := range listBody.Items {
		if it.ID == item.ID {
			found = true
			if it.Enabled {
				t.Fatalf("admin list should reflect enabled=false, got true")
			}
		}
	}
	if !found {
		t.Fatalf("admin list must still see the disabled policy (reserve must not) — readers should DIFFER")
	}
}

// TestNoMoneySideEffect 守住 cost_usd quota policy 的 CRUD 只管理策略，
// create+update+delete 前后 user_balances 与 billing_ledger_claims 行数必须不变；
// 任一余额或账本写入都违反管理面与钱路隔离。
func TestNoMoneySideEffect(t *testing.T) {
	ctx := context.Background()
	pool := openQuotaPool(t, ctx)
	f := newQuotaFixture(t, ctx, pool)

	balBefore := countRows(t, ctx, pool, "user_balances")
	ledgerBefore := countRows(t, ctx, pool, "billing_ledger_claims")

	item := f.createPolicy(f.tenantA,
		`{"scope_kind":"user","scope_id":"500","metric":"cost_usd","window_kind":"calendar_day","limit_value":"25","mode":"enforce"}`)
	idPath := "/" + strconv.FormatInt(item.ID, 10) + "?tenant_id=" + strconv.FormatInt(f.tenantA, 10)
	updRec := f.invoke(http.MethodPut, idPath,
		`{"scope_kind":"user","scope_id":"500","metric":"cost_usd","window_kind":"calendar_day","limit_value":"50","mode":"observe"}`)
	if updRec.Code != http.StatusOK {
		t.Fatalf("update status=%d", updRec.Code)
	}
	delRec := f.invoke(http.MethodDelete, idPath, "")
	if delRec.Code != http.StatusOK {
		t.Fatalf("delete status=%d", delRec.Code)
	}

	if got := countRows(t, ctx, pool, "user_balances"); got != balBefore {
		t.Fatalf("user_balances changed: before=%d after=%d (quota policy CRUD must not touch balances)", balBefore, got)
	}
	if got := countRows(t, ctx, pool, "billing_ledger_claims"); got != ledgerBefore {
		t.Fatalf("billing_ledger_claims changed: before=%d after=%d", ledgerBefore, got)
	}
}

func countRows(t *testing.T, ctx context.Context, pool *pgxpool.Pool, table string) int64 {
	t.Helper()
	var n int64
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM "+table).Scan(&n); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return n
}

func containsPolicyID(rows []reservequota.ListActiveQuotaPoliciesForScopesRow, id int64) bool {
	for _, r := range rows {
		if r.ID == id {
			return true
		}
	}
	return false
}

func pgTimestamptz(tm time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: tm, Valid: true}
}
