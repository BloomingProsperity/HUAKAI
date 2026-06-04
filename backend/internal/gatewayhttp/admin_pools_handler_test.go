package gatewayhttp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/db"
	admindb "github.com/BloomingProsperity/HUAKAI/internal/db/admin"
	dbbilling "github.com/BloomingProsperity/HUAKAI/internal/db/billing"
)

type adminPoolsStoreStub struct {
	insert    *dbbilling.InsertPoolParams
	get       *dbbilling.GetPoolParams
	list      *dbbilling.ListPoolsParams
	update    *dbbilling.UpdatePoolParams
	audits    []admindb.InsertAdminAuditEventParams
	insertErr error
	getErr    error
	updateErr error
	pool      dbbilling.PoolGroup
	items     []dbbilling.PoolGroup
}

var adminPoolsAuditAllowedActions = map[string]struct{}{
	"issue_api_key":                    {},
	"revoke_api_key":                   {},
	"list_api_keys":                    {},
	"issue_admin_token":                {},
	"revoke_admin_token":               {},
	"admin_login":                      {},
	"create_provider_account":          {},
	"disable_provider_account":         {},
	"enable_provider_account":          {},
	"delete_provider_account":          {},
	"create_account_credential":        {},
	"rotate_account_credential":        {},
	"disable_account_credential":       {},
	"delete_account_credential":        {},
	"list_account_credentials":         {},
	"credential_acquisition_started":   {},
	"credential_acquisition_completed": {},
	"credential_acquisition_failed":    {},
	"credential_acquisition_cancelled": {},
	"update_billing_settings":          {},
	"create_pool_group":                {},
	"update_pool_group":                {},
}

var adminPoolsAuditAllowedTargetTypes = map[string]struct{}{
	"api_key":            {},
	"admin_token":        {},
	"tenant":             {},
	"user":               {},
	"provider_account":   {},
	"account_credential": {},
	"billing_setting":    {},
	"pool_group":         {},
}

func (s *adminPoolsStoreStub) InsertPool(_ context.Context, arg dbbilling.InsertPoolParams) (dbbilling.PoolGroup, error) {
	s.insert = &arg
	if s.insertErr != nil {
		return dbbilling.PoolGroup{}, s.insertErr
	}
	pool := poolOrDefault(s.pool, arg.TenantID, arg.Name)
	pool.TopKDefault = arg.TopKDefault
	pool.CapabilityDefault = arg.CapabilityDefault
	pool.AllowLastResort = arg.AllowLastResort
	s.pool = pool
	return pool, nil
}

func (s *adminPoolsStoreStub) GetPool(_ context.Context, arg dbbilling.GetPoolParams) (dbbilling.PoolGroup, error) {
	s.get = &arg
	if s.getErr != nil {
		return dbbilling.PoolGroup{}, s.getErr
	}
	return poolOrDefault(s.pool, arg.TenantID, "primary"), nil
}

func (s *adminPoolsStoreStub) ListPools(_ context.Context, arg dbbilling.ListPoolsParams) ([]dbbilling.PoolGroup, error) {
	s.list = &arg
	if s.items != nil {
		return s.items, nil
	}
	return []dbbilling.PoolGroup{{ID: 7, TenantID: arg.TenantID, Name: "primary", Enabled: true}}, nil
}

func (s *adminPoolsStoreStub) UpdatePool(_ context.Context, arg dbbilling.UpdatePoolParams) (dbbilling.PoolGroup, error) {
	s.update = &arg
	if s.updateErr != nil {
		return dbbilling.PoolGroup{}, s.updateErr
	}
	pool := poolOrDefault(s.pool, arg.TenantID, "primary")
	if arg.Name != nil {
		pool.Name = *arg.Name
	}
	if arg.TopKDefault != nil {
		pool.TopKDefault = *arg.TopKDefault
	}
	if arg.CapabilityDefault != nil {
		pool.CapabilityDefault = *arg.CapabilityDefault
	}
	if arg.AllowLastResort != nil {
		pool.AllowLastResort = *arg.AllowLastResort
	}
	if arg.Enabled != nil {
		pool.Enabled = *arg.Enabled
	}
	pool.ID = arg.ID
	pool.TenantID = arg.TenantID
	s.pool = pool
	return pool, nil
}

// CreatePoolWithAudit 模拟 adapter 的同事务方法。stub 用前/后快照模拟 rollback:
// audit insert 失败时,把 s.pool 还原到 InsertPool 前的状态,反映真实 tx 回滚。
//
// 真 PG tx 回滚验证走 integration_pg 测试 (TestAdminPoolsCreate_AuditFailureRollsBackPool)。
func (s *adminPoolsStoreStub) CreatePoolWithAudit(ctx context.Context, pp dbbilling.InsertPoolParams, ap admindb.InsertAdminAuditEventParams) (dbbilling.PoolGroup, error) {
	prev := s.pool
	pool, err := s.InsertPool(ctx, pp)
	if err != nil {
		return dbbilling.PoolGroup{}, err
	}
	ap.TargetID = &pool.ID
	if _, err := s.InsertAdminAuditEvent(ctx, ap); err != nil {
		// 模拟 tx rollback:把 s.pool 还原
		s.pool = prev
		return dbbilling.PoolGroup{}, err
	}
	return pool, nil
}

// UpdatePoolWithAudit 同事务模拟。audit 失败 → s.pool 还原 + return err。
func (s *adminPoolsStoreStub) UpdatePoolWithAudit(ctx context.Context, up dbbilling.UpdatePoolParams, ap admindb.InsertAdminAuditEventParams) (dbbilling.PoolGroup, error) {
	prev := s.pool
	pool, err := s.UpdatePool(ctx, up)
	if err != nil {
		return dbbilling.PoolGroup{}, err
	}
	ap.TargetID = &pool.ID
	if _, err := s.InsertAdminAuditEvent(ctx, ap); err != nil {
		s.pool = prev
		return dbbilling.PoolGroup{}, err
	}
	return pool, nil
}

func (s *adminPoolsStoreStub) InsertAdminAuditEvent(_ context.Context, arg admindb.InsertAdminAuditEventParams) (admindb.InsertAdminAuditEventRow, error) {
	if _, ok := adminPoolsAuditAllowedActions[arg.Action]; !ok {
		return admindb.InsertAdminAuditEventRow{}, &pgconn.PgError{
			Code:           "23514",
			ConstraintName: "admin_audit_events_action_check",
			Message:        "new row for relation \"admin_audit_events\" violates check constraint \"admin_audit_events_action_check\"",
		}
	}
	if _, ok := adminPoolsAuditAllowedTargetTypes[arg.TargetType]; !ok {
		return admindb.InsertAdminAuditEventRow{}, &pgconn.PgError{
			Code:           "23514",
			ConstraintName: "admin_audit_events_target_type_check",
			Message:        "new row for relation \"admin_audit_events\" violates check constraint \"admin_audit_events_target_type_check\"",
		}
	}
	s.audits = append(s.audits, arg)
	return admindb.InsertAdminAuditEventRow{ID: int64(len(s.audits))}, nil
}

func TestATS1Tenant001TenantOperatorListCreateUpdateUsesOwnTenant(t *testing.T) {
	store := &adminPoolsStoreStub{}
	auth := adminPoolsTenantOperator(7)

	listRec := invokeAdminPools(t, store, auth, http.MethodGet, "/admin/v1/pools", "")
	if listRec.Code != http.StatusOK {
		t.Fatalf("list status=%d body=%s", listRec.Code, listRec.Body.String())
	}
	if store.list == nil || store.list.TenantID != 7 {
		t.Fatalf("list did not use operator tenant scope: %+v", store.list)
	}

	createRec := invokeAdminPools(t, store, auth, http.MethodPost, "/admin/v1/pools/",
		`{"name":"tenant pool"}`)
	if createRec.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", createRec.Code, createRec.Body.String())
	}
	if store.insert == nil || store.insert.TenantID != 7 || store.insert.Name != "tenant pool" {
		t.Fatalf("create did not use operator tenant scope: %+v", store.insert)
	}

	updateRec := invokeAdminPools(t, store, auth, http.MethodPatch, "/admin/v1/pools/77",
		`{"name":"tenant pool updated"}`)
	if updateRec.Code != http.StatusOK {
		t.Fatalf("update status=%d body=%s", updateRec.Code, updateRec.Body.String())
	}
	if store.update == nil || store.update.TenantID != 7 || store.update.ID != 77 {
		t.Fatalf("update did not use operator tenant scope: %+v", store.update)
	}
}

func TestATS1Tenant001TenantOperatorCrossTenantPoolDeniedOrHidden(t *testing.T) {
	store := &adminPoolsStoreStub{}
	rec := invokeAdminPools(t, store, adminPoolsTenantOperator(7), http.MethodPatch, "/admin/v1/pools/77?tenant_id=8",
		`{"name":"wrong tenant"}`)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if store.update != nil {
		t.Fatalf("cross-tenant update touched store: %+v", store.update)
	}

	store = &adminPoolsStoreStub{getErr: pgx.ErrNoRows}
	rec = invokeAdminPools(t, store, adminPoolsTenantOperator(7), http.MethodGet, "/admin/v1/pools/77", "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if store.get == nil || store.get.TenantID != 7 {
		t.Fatalf("hidden cross-tenant read did not use scoped tenant lookup: %+v", store.get)
	}
}

func TestATS1Tenant001PlatformAdminRequiresExplicitTenant(t *testing.T) {
	store := &adminPoolsStoreStub{}
	listRec := invokeAdminPools(t, store, adminPoolAdmin(), http.MethodGet, "/admin/v1/pools", "")
	if listRec.Code != http.StatusBadRequest {
		t.Fatalf("list status=%d body=%s", listRec.Code, listRec.Body.String())
	}
	if store.list != nil {
		t.Fatalf("platform list without tenant touched store: %+v", store.list)
	}

	createRec := invokeAdminPools(t, store, adminPoolAdmin(), http.MethodPost, "/admin/v1/pools/",
		`{"name":"missing tenant"}`)
	if createRec.Code != http.StatusBadRequest {
		t.Fatalf("create status=%d body=%s", createRec.Code, createRec.Body.String())
	}
	if store.insert != nil {
		t.Fatalf("platform create without tenant touched store: %+v", store.insert)
	}
}

func TestATS1Tenant001PlatformAdminExplicitTenantSucceedsAndAudits(t *testing.T) {
	store := &adminPoolsStoreStub{}
	rec := invokeAdminPools(t, store, adminPoolAdmin(), http.MethodPost, "/admin/v1/pools/?tenant_id=42",
		`{"name":"platform tenant pool"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if store.insert == nil || store.insert.TenantID != 42 {
		t.Fatalf("platform create did not use explicit tenant: %+v", store.insert)
	}
	if len(store.audits) != 1 {
		t.Fatalf("audit count=%d audits=%+v", len(store.audits), store.audits)
	}
	audit := store.audits[0]
	if audit.TenantID == nil || *audit.TenantID != 42 || audit.ActorID != "11" || audit.ActorRole != admin.RolePlatformAdmin {
		t.Fatalf("audit lost tenant or actor: %+v", audit)
	}
	if audit.Action != "create_pool_group" || audit.TargetType != "pool_group" || audit.TargetID == nil || *audit.TargetID != 77 {
		t.Fatalf("audit action/target mismatch: %+v", audit)
	}
}

func TestAdminPoolsAuditStoreMirrorsAdminAuditChecks(t *testing.T) {
	store := &adminPoolsStoreStub{}
	if _, err := store.InsertAdminAuditEvent(context.Background(), admindb.InsertAdminAuditEventParams{
		Action:     "unknown_action",
		TargetType: "pool_group",
	}); err == nil {
		t.Fatalf("unknown action was accepted")
	}
	if _, err := store.InsertAdminAuditEvent(context.Background(), admindb.InsertAdminAuditEventParams{
		Action:     "create_pool_group",
		TargetType: "unknown_target",
	}); err == nil {
		t.Fatalf("unknown target_type was accepted")
	}
	if len(store.audits) != 0 {
		t.Fatalf("invalid audit records persisted: %d", len(store.audits))
	}
}

func TestATS1Tenant001BodyTenantIDCannotOverrideValidatedScope(t *testing.T) {
	store := &adminPoolsStoreStub{}
	rec := invokeAdminPools(t, store, adminPoolAdmin(), http.MethodPost, "/admin/v1/pools/?tenant_id=7",
		`{"tenant_id":8,"name":"body override"}`)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if store.insert != nil || len(store.audits) != 0 {
		t.Fatalf("body tenant override touched store: insert=%+v audits=%+v", store.insert, store.audits)
	}
}

func TestAdminPools_ListSuccessUsesDefaultLimit(t *testing.T) {
	store := &adminPoolsStoreStub{}
	rec := invokeAdminPools(t, store, adminPoolsTenantOperator(7), http.MethodGet, "/admin/v1/pools", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if store.list == nil || store.list.TenantID != 7 || store.list.LimitCount != defaultAdminPoolsLimit {
		t.Fatalf("list params mismatch: %+v", store.list)
	}
}

func TestAdminPools_CreateSuccessInsertsTrimmedName(t *testing.T) {
	store := &adminPoolsStoreStub{}
	rec := invokeAdminPools(t, store, adminPoolsTenantOperator(7), http.MethodPost, "/admin/v1/pools/",
		`{"name":" primary ","description":"owner visible label"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if store.insert == nil || store.insert.TenantID != 7 || store.insert.Name != "primary" {
		t.Fatalf("insert params mismatch: %+v", store.insert)
	}
	if store.insert.TopKDefault != defaultAdminPoolTopKDefault ||
		store.insert.CapabilityDefault != defaultAdminPoolCapabilityDefault ||
		store.insert.AllowLastResort {
		t.Fatalf("insert defaults mismatch: %+v", store.insert)
	}
}

func TestAT_POOL_001_001_CreateFieldsPersistAndReadBack(t *testing.T) {
	store := &adminPoolsStoreStub{}
	createResp := invokeAdminPools(t, store, adminPoolsTenantOperator(7), http.MethodPost, "/admin/v1/pools/",
		`{"name":"primary","top_k_default":4,"capability_default":"safe_equivalent_allowed","allow_last_resort":true}`)
	if createResp.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", createResp.Code, createResp.Body.String())
	}
	if store.insert == nil ||
		store.insert.TopKDefault != 4 ||
		store.insert.CapabilityDefault != "safe_equivalent_allowed" ||
		!store.insert.AllowLastResort {
		t.Fatalf("create did not pass persistence fields: %+v", store.insert)
	}

	getResp := invokeAdminPools(t, store, adminPoolsTenantOperator(7), http.MethodGet, "/admin/v1/pools/77", "")
	if getResp.Code != http.StatusOK {
		t.Fatalf("get status=%d body=%s", getResp.Code, getResp.Body.String())
	}
	var got dbbilling.PoolGroup
	if err := json.Unmarshal(getResp.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode get response: %v body=%s", err, getResp.Body.String())
	}
	if got.TopKDefault != 4 || got.CapabilityDefault != "safe_equivalent_allowed" || !got.AllowLastResort {
		t.Fatalf("get response lost fields: %+v", got)
	}
}

func TestAdminPools_GetSuccess(t *testing.T) {
	store := &adminPoolsStoreStub{}
	rec := invokeAdminPools(t, store, adminPoolsTenantOperator(7), http.MethodGet, "/admin/v1/pools/77", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if store.get == nil || store.get.ID != 77 || store.get.TenantID != 7 {
		t.Fatalf("get params mismatch: %+v", store.get)
	}
}

func TestAdminPools_UpdateSuccessPatchesNameAndEnabled(t *testing.T) {
	store := &adminPoolsStoreStub{}
	rec := invokeAdminPools(t, store, adminPoolsTenantOperator(7), http.MethodPatch, "/admin/v1/pools/77",
		`{"name":" updated ","enabled":false,"top_k_default":3,"capability_default":"safe_equivalent_allowed","allow_last_resort":true}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if store.update == nil || store.update.ID != 77 || store.update.TenantID != 7 {
		t.Fatalf("update params mismatch: %+v", store.update)
	}
	if store.update.Name == nil || *store.update.Name != "updated" || store.update.Enabled == nil || *store.update.Enabled {
		t.Fatalf("update fields mismatch: %+v", store.update)
	}
	if store.update.TopKDefault == nil || *store.update.TopKDefault != 3 ||
		store.update.CapabilityDefault == nil || *store.update.CapabilityDefault != "safe_equivalent_allowed" ||
		store.update.AllowLastResort == nil || !*store.update.AllowLastResort {
		t.Fatalf("update persistence fields mismatch: %+v", store.update)
	}
}

func TestAdminPools_CreateMissingNameReturns400(t *testing.T) {
	store := &adminPoolsStoreStub{}
	rec := invokeAdminPools(t, store, adminPoolsTenantOperator(7), http.MethodPost, "/admin/v1/pools/", `{"description":"missing"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if store.insert != nil {
		t.Fatalf("bad request touched store: %+v", store.insert)
	}
}

func TestAdminPools_CreateDuplicateNameReturns409(t *testing.T) {
	store := &adminPoolsStoreStub{insertErr: &pgconn.PgError{Code: "23505"}}
	rec := invokeAdminPools(t, store, adminPoolsTenantOperator(7), http.MethodPost, "/admin/v1/pools/", `{"name":"primary"}`)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestAdminPools_GetNotFoundReturns404(t *testing.T) {
	store := &adminPoolsStoreStub{getErr: pgx.ErrNoRows}
	rec := invokeAdminPools(t, store, adminPoolsTenantOperator(7), http.MethodGet, "/admin/v1/pools/404", "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestAdminPools_UnauthorizedReturns401(t *testing.T) {
	store := &adminPoolsStoreStub{}
	rec := invokeAdminPools(t, store, adminPoolAuthStub{err: admin.ErrAdminUnauthorized}, http.MethodGet, "/admin/v1/pools/", "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if store.list != nil {
		t.Fatalf("unauthorized request touched store: %+v", store.list)
	}
}

func TestAdminPools_CreateNameTooLongReturns400(t *testing.T) {
	store := &adminPoolsStoreStub{}
	rec := invokeAdminPools(t, store, adminPoolsTenantOperator(7), http.MethodPost, "/admin/v1/pools/",
		`{"name":"`+strings.Repeat("a", maxAdminPoolNameRunes+1)+`"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func invokeAdminPools(t *testing.T, store *adminPoolsStoreStub, auth AdminPoolsAuth, method, target, body string) *httptest.ResponseRecorder {
	t.Helper()
	r := chi.NewRouter()
	r.Route("/admin/v1/pools", func(r chi.Router) {
		r.Mount("/", NewAdminPoolsHandler(AdminPoolsDeps{Auth: auth, Store: store}))
	})
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func poolOrDefault(pool dbbilling.PoolGroup, tenantID int64, name string) dbbilling.PoolGroup {
	if pool.ID != 0 {
		return pool
	}
	return dbbilling.PoolGroup{
		ID:                77,
		TenantID:          tenantID,
		Name:              name,
		TopKDefault:       defaultAdminPoolTopKDefault,
		CapabilityDefault: defaultAdminPoolCapabilityDefault,
		Enabled:           true,
	}
}

func adminPoolsTenantOperator(tenantID int64) adminPoolAuthStub {
	return adminPoolAuthStub{ident: admin.AdminIdentity{TokenID: 22, Role: admin.RoleTenantOperator, ScopeTenantID: tenantID}}
}

// ------------------------------------------------------------------
// W5 synthesis §6 C3 T_G1 / T_G2 — admin pool 同事务真 PG 验证。
//
// 这些测试需要真 PostgreSQL (HUAKAI_DATABASE_URL 不设则 skip)。判别 fixture:
// 装一个 BEFORE INSERT trigger 拒收特定 actor_id 的 admin_audit_events 行,
// 然后调 CreatePoolWithAudit / UpdatePoolWithAudit;adapter 在 BeginFunc 内
// audit insert 失败应自动 rollback,断言 pool 行**不存在 / 字段未变**。
//
// Mutation 自检:把 adapter CreatePoolWithAudit 改成 InsertPool 后单独
// InsertAdminAuditEvent (非同事务) → audit 拒后 pool 行已落 → 本用例 red。
//
// 直接 append 到既有 _test.go,用 env-var 守卫而不是 build tag。
// ------------------------------------------------------------------

func openAdminPoolsTestPool(t *testing.T, ctx context.Context) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("HUAKAI_DATABASE_URL")
	if dsn == "" {
		t.Skip("HUAKAI_DATABASE_URL not set; skipping integration_pg")
	}
	p, err := db.Open(ctx, db.PoolConfig{DSN: dsn})
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(p.Close)
	return p
}

// seedAdminPoolsTenant 种一个 tenant 行,返回 ID + cleanup。
func seedAdminPoolsTenant(t *testing.T, ctx context.Context, pool *pgxpool.Pool, suffix string) int64 {
	t.Helper()
	var tenantID int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO tenants (name) VALUES ($1) RETURNING id`,
		"admin-pools-tx-tenant-"+suffix,
	).Scan(&tenantID); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM admin_audit_events WHERE tenant_id = $1`, tenantID)
		_, _ = pool.Exec(c, `DELETE FROM pool_groups WHERE tenant_id = $1`, tenantID)
		_, _ = pool.Exec(c, `DELETE FROM tenants WHERE id = $1`, tenantID)
	})
	return tenantID
}

// installAdminAuditRejectTrigger 装一个 BEFORE INSERT trigger 拒收
// actor_id = rejectActorID 的 admin_audit_events 行;返回 cleanup 闭包。
func installAdminAuditRejectTrigger(t *testing.T, ctx context.Context, pool *pgxpool.Pool, name, rejectActorID string) {
	t.Helper()
	fnName := "audit_reject_" + name
	trigName := "trg_" + name
	if _, err := pool.Exec(ctx, `
		CREATE OR REPLACE FUNCTION `+fnName+`() RETURNS trigger AS $$
		BEGIN
			IF NEW.actor_id = '`+rejectActorID+`' THEN
				RAISE EXCEPTION 'admin_pools_tx test reject actor_id %', NEW.actor_id;
			END IF;
			RETURN NEW;
		END;
		$$ LANGUAGE plpgsql`); err != nil {
		t.Fatalf("create reject fn: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		DROP TRIGGER IF EXISTS `+trigName+` ON admin_audit_events;
		CREATE TRIGGER `+trigName+` BEFORE INSERT ON admin_audit_events
		FOR EACH ROW EXECUTE FUNCTION `+fnName+`()`); err != nil {
		t.Fatalf("create trigger: %v", err)
	}
	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DROP TRIGGER IF EXISTS `+trigName+` ON admin_audit_events`)
		_, _ = pool.Exec(c, `DROP FUNCTION IF EXISTS `+fnName+`()`)
	})
}

// TestAdminPoolsCreate_AuditFailureRollsBackPool — T_G1
//
// 判别 fixture:trigger 拒收 actor_id='gw10-create-test' 的 audit row;
// CreatePoolWithAudit 应返 error 且 pool_groups 表内无 (tenant, name) 行。
//
// Mutation 自检:adapter 改成 InsertPool 提交后再调 InsertAdminAuditEvent (非同事务)
// → pool 行已落,审计后才拒 → pool 行留下 → 本用例 red。
func TestAdminPoolsCreate_AuditFailureRollsBackPool(t *testing.T) {
	ctx := context.Background()
	pool := openAdminPoolsTestPool(t, ctx)
	suffix := uuid.NewString()
	tenantID := seedAdminPoolsTenant(t, ctx, pool, suffix)
	rejectActor := "gw10-create-" + suffix
	installAdminAuditRejectTrigger(t, ctx, pool, "create_"+strings.ReplaceAll(suffix, "-", "_"), rejectActor)

	adapter := NewAdminPoolsStoreAdapter(dbbilling.New(pool), admindb.New(pool), pool)
	poolName := "tx-create-" + suffix
	requestID := "req-" + suffix

	_, err := adapter.CreatePoolWithAudit(ctx,
		dbbilling.InsertPoolParams{
			TenantID:          tenantID,
			Name:              poolName,
			TopKDefault:       1,
			CapabilityDefault: "exact_capability_only",
			AllowLastResort:   false,
		},
		admindb.InsertAdminAuditEventParams{
			TenantID:   &tenantID,
			ActorID:    rejectActor, // trigger 拒
			ActorRole:  admin.RolePlatformAdmin,
			Action:     "create_pool_group",
			TargetType: "pool_group",
			RequestID:  &requestID,
			Payload:    []byte(`{"name":"` + poolName + `"}`),
		},
	)
	if err == nil {
		t.Fatalf("CreatePoolWithAudit must fail when audit trigger rejects; got nil err")
	}

	// 验:pool_groups 没留下行 — tx 真回滚
	var count int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM pool_groups WHERE tenant_id=$1 AND name=$2`,
		tenantID, poolName,
	).Scan(&count); err != nil {
		t.Fatalf("count pool_groups: %v", err)
	}
	if count != 0 {
		t.Fatalf("pool row MUST NOT be committed when audit insert fails; got %d rows", count)
	}
}

// TestAdminPoolsUpdate_AuditFailureRollsBackPool — T_G2 (pool 版,不是 provider account)
//
// 判别 fixture:seed pool 后 audit trigger 拒 update;断言 pool 字段未变。
// Mutation 自检:UpdatePoolWithAudit 改成两段非同事务 → update 提交后 audit 拒
// → 字段已改 → 本用例 red。
func TestAdminPoolsUpdate_AuditFailureRollsBackPool(t *testing.T) {
	ctx := context.Background()
	pool := openAdminPoolsTestPool(t, ctx)
	suffix := uuid.NewString()
	tenantID := seedAdminPoolsTenant(t, ctx, pool, suffix)
	rejectActor := "gw10-update-" + suffix
	installAdminAuditRejectTrigger(t, ctx, pool, "update_"+strings.ReplaceAll(suffix, "-", "_"), rejectActor)

	adapter := NewAdminPoolsStoreAdapter(dbbilling.New(pool), admindb.New(pool), pool)
	poolName := "tx-update-baseline-" + suffix

	// seed a pool 行,直接走 InsertPool (非 tx 版本) — baseline
	seeded, err := dbbilling.New(pool).InsertPool(ctx, dbbilling.InsertPoolParams{
		TenantID:          tenantID,
		Name:              poolName,
		TopKDefault:       1,
		CapabilityDefault: "exact_capability_only",
		AllowLastResort:   false,
	})
	if err != nil {
		t.Fatalf("seed pool: %v", err)
	}

	// 跑同事务 UpdatePoolWithAudit — 应失败 + 字段未变
	newTopK := int32(5)
	requestID := "req-upd-" + suffix
	_, err = adapter.UpdatePoolWithAudit(ctx,
		dbbilling.UpdatePoolParams{
			TopKDefault: &newTopK,
			TenantID:    tenantID,
			ID:          seeded.ID,
		},
		admindb.InsertAdminAuditEventParams{
			TenantID:   &tenantID,
			ActorID:    rejectActor,
			ActorRole:  admin.RolePlatformAdmin,
			Action:     "update_pool_group",
			TargetType: "pool_group",
			RequestID:  &requestID,
			Payload:    []byte(`{"updated":true}`),
		},
	)
	if err == nil {
		t.Fatalf("UpdatePoolWithAudit must fail when audit trigger rejects")
	}

	// 验:pool.TopKDefault 仍是 1 (未变)
	var topK int32
	if err := pool.QueryRow(ctx,
		`SELECT top_k_default FROM pool_groups WHERE id=$1 AND tenant_id=$2`,
		seeded.ID, tenantID,
	).Scan(&topK); err != nil {
		t.Fatalf("read pool top_k: %v", err)
	}
	if topK != 1 {
		t.Fatalf("pool top_k MUST remain 1 when audit fails; got %d", topK)
	}
}

// TestAdminPoolsCreate_HappyPathCommitsBoth — 正向保持守:audit 成功时 pool + audit
// 都落库。Mutation: 把 BeginFunc 改成只 Rollback,正向用例必红。
func TestAdminPoolsCreate_HappyPathCommitsBoth(t *testing.T) {
	ctx := context.Background()
	pool := openAdminPoolsTestPool(t, ctx)
	suffix := uuid.NewString()
	tenantID := seedAdminPoolsTenant(t, ctx, pool, suffix)

	adapter := NewAdminPoolsStoreAdapter(dbbilling.New(pool), admindb.New(pool), pool)
	poolName := "tx-happy-" + suffix
	requestID := "req-happy-" + suffix

	pg, err := adapter.CreatePoolWithAudit(ctx,
		dbbilling.InsertPoolParams{
			TenantID:          tenantID,
			Name:              poolName,
			TopKDefault:       2,
			CapabilityDefault: "exact_capability_only",
			AllowLastResort:   false,
		},
		admindb.InsertAdminAuditEventParams{
			TenantID:   &tenantID,
			ActorID:    "happy-" + suffix,
			ActorRole:  admin.RolePlatformAdmin,
			Action:     "create_pool_group",
			TargetType: "pool_group",
			RequestID:  &requestID,
			Payload:    []byte(`{"name":"` + poolName + `"}`),
		},
	)
	if err != nil {
		t.Fatalf("happy path CreatePoolWithAudit: %v", err)
	}
	if pg.ID == 0 || pg.Name != poolName {
		t.Fatalf("returned pool drift: %+v", pg)
	}

	// 验 pool + audit 都落库
	var poolCount, auditCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM pool_groups WHERE id=$1`, pg.ID).Scan(&poolCount); err != nil {
		t.Fatalf("count pool: %v", err)
	}
	if poolCount != 1 {
		t.Fatalf("happy path pool row missing")
	}
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM admin_audit_events WHERE actor_id=$1 AND target_id=$2`,
		"happy-"+suffix, pg.ID,
	).Scan(&auditCount); err != nil {
		t.Fatalf("count audit: %v", err)
	}
	if auditCount != 1 {
		t.Fatalf("happy path audit row missing; got %d", auditCount)
	}
}
