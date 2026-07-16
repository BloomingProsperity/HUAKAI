package adminquotahttp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/admintest"
	dbquota "github.com/BloomingProsperity/HUAKAI/internal/db/quotaadmin"
)

// quotaStoreStub 是供 handler 单元测试用的伪 quotaPolicyStore:它记录调用,
// 并在没有数据库的情况下返回可配置的行/错误。SQL 层面的租户围栏、唯一索引
// 以及 FK RESTRICT 由集成测试来演练。
type quotaStoreStub struct {
	listRows  []dbquota.QuotaPolicy
	getRow    dbquota.QuotaPolicy
	getErr    error
	createRow dbquota.QuotaPolicy
	createErr error
	updateRow dbquota.QuotaPolicy
	updateErr error
	deleteID  int64
	deleteErr error

	listArg    dbquota.ListQuotaPoliciesForAdminParams
	getArg     dbquota.GetQuotaPolicyByIDParams
	lastCreate quotaPolicyCreateParams
	lastUpdate quotaPolicyUpdateParams
	lastDelete quotaPolicyDeleteParams
	lastAudit  auditInput

	listCalls, getCalls, createCalls, updateCalls, deleteCalls int
}

func (s *quotaStoreStub) ListQuotaPoliciesForAdmin(_ context.Context, arg dbquota.ListQuotaPoliciesForAdminParams) ([]dbquota.QuotaPolicy, error) {
	s.listCalls++
	s.listArg = arg
	return s.listRows, nil
}

func (s *quotaStoreStub) GetQuotaPolicyByID(_ context.Context, arg dbquota.GetQuotaPolicyByIDParams) (dbquota.QuotaPolicy, error) {
	s.getCalls++
	s.getArg = arg
	if s.getErr != nil {
		return dbquota.QuotaPolicy{}, s.getErr
	}
	return s.getRow, nil
}

func (s *quotaStoreStub) CreateQuotaPolicyWithAudit(_ context.Context, arg quotaPolicyCreateParams, audit auditInput) (dbquota.QuotaPolicy, error) {
	s.createCalls++
	s.lastCreate = arg
	s.lastAudit = audit
	if s.createErr != nil {
		return dbquota.QuotaPolicy{}, s.createErr
	}
	return s.createRow, nil
}

func (s *quotaStoreStub) UpdateQuotaPolicyWithAudit(_ context.Context, arg quotaPolicyUpdateParams, audit auditInput) (dbquota.QuotaPolicy, error) {
	s.updateCalls++
	s.lastUpdate = arg
	s.lastAudit = audit
	if s.updateErr != nil {
		return dbquota.QuotaPolicy{}, s.updateErr
	}
	return s.updateRow, nil
}

func (s *quotaStoreStub) DeleteQuotaPolicyWithAudit(_ context.Context, arg quotaPolicyDeleteParams, audit auditInput) (int64, error) {
	s.deleteCalls++
	s.lastDelete = arg
	s.lastAudit = audit
	if s.deleteErr != nil {
		return 0, s.deleteErr
	}
	return s.deleteID, nil
}

func (s *quotaStoreStub) mutationCalls() int {
	return s.createCalls + s.updateCalls + s.deleteCalls
}

type quotaAuthStub struct {
	ident admin.AdminIdentity
	err   error
}

func (s quotaAuthStub) Resolve(context.Context, *http.Request) (admin.AdminIdentity, error) {
	if s.err != nil {
		return admin.AdminIdentity{}, s.err
	}
	return s.ident, nil
}

func tenantOperator(tenantID int64) admin.AdminIdentity {
	return admintest.TenantOperator(12, tenantID)
}

func platformAdmin() admin.AdminIdentity {
	return admintest.Platform(99)
}

func numericFromString(t *testing.T, raw string) pgtype.Numeric {
	t.Helper()
	var n pgtype.Numeric
	if err := n.Scan(raw); err != nil {
		t.Fatalf("scan numeric %q: %v", raw, err)
	}
	return n
}

func invoke(t *testing.T, deps Deps, method, target, body string) *httptest.ResponseRecorder {
	t.Helper()
	r := NewRouter(deps)
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func assertStatus(t *testing.T, rec *httptest.ResponseRecorder, want int) {
	t.Helper()
	if rec.Code != want {
		t.Fatalf("status=%d want=%d body=%s", rec.Code, want, strings.TrimSpace(rec.Body.String()))
	}
}

func errorCode(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode error body: %v body=%s", err, rec.Body.String())
	}
	return body.Error.Code
}

const validCreateBody = `{"scope_kind":"user","scope_id":"42","metric":"cost_usd","window_kind":"calendar_day","limit_value":"10.00000000","mode":"enforce"}`

// TestCreateHappyPathPersistsAndAudits 证明 create 返回 201 并带回持久化的行,
// 且审计输入携带 action=create_quota_policy / target_type=quota_policy。
// 变异:在 adapter 里去掉 InsertAdminAuditEvent 调用 —— 在 handler 层审计输入
// 仍然会流过,所以本测试钉住的契约是"一次 create 必须发出一条 quota_policy
// 审计";至于该行是否真的落库,由集成测试断言。
func TestCreateHappyPathPersistsAndAudits(t *testing.T) {
	store := &quotaStoreStub{createRow: dbquota.QuotaPolicy{
		ID: 901, TenantID: 7, ScopeKind: "user", ScopeID: "42", Metric: "cost_usd",
		WindowKind: "calendar_day", WindowSeconds: 0, LimitValue: numericFromString(t, "10.00000000"),
		BurstValue: numericFromString(t, "0"), Mode: "enforce", Priority: 100, Enabled: true,
		ValidFrom: pgtype.Timestamptz{Valid: true}, CreatedAt: pgtype.Timestamptz{Valid: true},
		UpdatedAt: pgtype.Timestamptz{Valid: true},
	}}
	rec := invoke(t, Deps{Auth: quotaAuthStub{ident: tenantOperator(7)}, Store: store},
		http.MethodPost, "/", validCreateBody)
	assertStatus(t, rec, http.StatusCreated)
	if store.createCalls != 1 {
		t.Fatalf("create calls=%d want 1", store.createCalls)
	}
	if store.lastCreate.insert.TenantID != 7 || store.lastCreate.insert.ScopeKind != "user" ||
		store.lastCreate.insert.Metric != "cost_usd" || store.lastCreate.insert.Mode != "enforce" {
		t.Fatalf("create insert params wrong: %+v", store.lastCreate.insert)
	}
	if store.lastAudit.Action != "create_quota_policy" || store.lastAudit.TenantID != 7 {
		t.Fatalf("audit input wrong: %+v", store.lastAudit)
	}
	var item quotaPolicyItem
	if err := json.Unmarshal(rec.Body.Bytes(), &item); err != nil {
		t.Fatalf("decode item: %v", err)
	}
	if item.ID != 901 || item.LimitValue != "10" || item.ScopeKind != "user" {
		t.Fatalf("response item wrong: %+v", item)
	}
}

// TestCreateMetricValidation 证明非法 metric 在写库之前就被 Go 侧以
// 400 invalid_metric 拒绝。变异:去掉 validMetrics 白名单会让 "bogus" 到达
// Postgres(CHECK 500/503),于是这个断言 400 + invalid_metric 的测试就会变红。
// 区分度:store 一定不能被调用。
func TestCreateMetricValidation(t *testing.T) {
	store := &quotaStoreStub{}
	rec := invoke(t, Deps{Auth: quotaAuthStub{ident: tenantOperator(7)}, Store: store},
		http.MethodPost, "/",
		`{"scope_kind":"user","scope_id":"42","metric":"bogus","window_kind":"none","limit_value":"1"}`)
	assertStatus(t, rec, http.StatusBadRequest)
	if code := errorCode(t, rec); code != "invalid_metric" {
		t.Fatalf("error code=%q want invalid_metric", code)
	}
	if store.createCalls != 0 {
		t.Fatalf("invalid metric reached store: calls=%d", store.createCalls)
	}
}

// TestCreateValidationMatrix 覆盖 Go 侧并集校验的其余部分:每个非法输入都必须
// 返回 400,且不触及 store。
func TestCreateValidationMatrix(t *testing.T) {
	cases := map[string]struct {
		body string
		code string
	}{
		"bad scope_kind":         {`{"scope_kind":"nope","scope_id":"1","metric":"requests","window_kind":"none","limit_value":"1"}`, "invalid_scope_kind"},
		"empty scope_id":         {`{"scope_kind":"user","scope_id":"","metric":"requests","window_kind":"none","limit_value":"1"}`, "invalid_scope_id"},
		"bad window_kind":        {`{"scope_kind":"user","scope_id":"1","metric":"requests","window_kind":"weird","limit_value":"1"}`, "invalid_window_kind"},
		"fixed needs seconds":    {`{"scope_kind":"user","scope_id":"1","metric":"requests","window_kind":"fixed","window_seconds":0,"limit_value":"1"}`, "invalid_window_seconds"},
		"negative limit":         {`{"scope_kind":"user","scope_id":"1","metric":"cost_usd","window_kind":"none","limit_value":"-1"}`, "invalid_limit_value"},
		"negative burst":         {`{"scope_kind":"user","scope_id":"1","metric":"cost_usd","window_kind":"none","limit_value":"1","burst_value":"-2"}`, "invalid_burst_value"},
		"bad mode":               {`{"scope_kind":"user","scope_id":"1","metric":"cost_usd","window_kind":"none","limit_value":"1","mode":"halt"}`, "invalid_mode"},
		"validity inverted":      {`{"scope_kind":"user","scope_id":"1","metric":"cost_usd","window_kind":"none","limit_value":"1","valid_from":"2026-06-13T00:00:00Z","valid_until":"2026-06-12T00:00:00Z"}`, "invalid_validity_range"},
		"missing limit required": {`{"scope_kind":"user","scope_id":"1","metric":"cost_usd","window_kind":"none"}`, "invalid_limit_value"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			store := &quotaStoreStub{}
			rec := invoke(t, Deps{Auth: quotaAuthStub{ident: tenantOperator(7)}, Store: store},
				http.MethodPost, "/", tc.body)
			assertStatus(t, rec, http.StatusBadRequest)
			if code := errorCode(t, rec); code != tc.code {
				t.Fatalf("error code=%q want %q", code, tc.code)
			}
			if store.mutationCalls() != 0 {
				t.Fatalf("invalid input reached store: %+v", store)
			}
		})
	}
}

// TestCreateAcceptsFullUnion 证明每个超集枚举值都能通过校验,并被透传进
// insert params(没有任何值被悄悄丢弃)。
func TestCreateAcceptsFullUnion(t *testing.T) {
	scopeKinds := []string{"global", "user", "api_key", "channel", "pool_group", "provider_account"}
	metrics := []string{"requests", "tokens_estimated", "cost_usd", "concurrency"}
	windowKinds := []string{"none", "fixed", "calendar_day", "calendar_week", "calendar_month", "manual"}
	modes := []string{"enforce", "observe", "manual_first", "disabled"}
	for _, sk := range scopeKinds {
		for _, m := range metrics {
			for _, wk := range windowKinds {
				for _, mode := range modes {
					store := &quotaStoreStub{createRow: dbquota.QuotaPolicy{ID: 1}}
					ws := `,"window_seconds":0`
					if wk == "fixed" {
						ws = `,"window_seconds":60`
					}
					body := `{"scope_kind":"` + sk + `","scope_id":"x","metric":"` + m +
						`","window_kind":"` + wk + `","limit_value":"0","burst_value":"5","mode":"` + mode + `","priority":3` + ws + `}`
					rec := invoke(t, Deps{Auth: quotaAuthStub{ident: tenantOperator(7)}, Store: store},
						http.MethodPost, "/", body)
					if rec.Code != http.StatusCreated {
						t.Fatalf("union combo sk=%s m=%s wk=%s mode=%s rejected: %d %s",
							sk, m, wk, mode, rec.Code, strings.TrimSpace(rec.Body.String()))
					}
					if store.lastCreate.insert.ScopeKind != sk || store.lastCreate.insert.Metric != m ||
						store.lastCreate.insert.WindowKind != wk || store.lastCreate.insert.Mode != mode ||
						store.lastCreate.insert.Priority != 3 {
						t.Fatalf("union combo not passed through: %+v", store.lastCreate.insert)
					}
				}
			}
		}
	}
}

// TestGuardRejectsNonAdminAndAnon 证明 role switch 的 default 分支会拒绝
// user 角色的凭证(403 admin_forbidden_scope)以及未认证的请求(401)——
// 二者都发生在触及 store 之前。变异:让 role switch 的 default 分支 fall through
// 到放行,会使 403 断言变红。
func TestGuardRejectsNonAdminAndAnon(t *testing.T) {
	t.Run("user role 403", func(t *testing.T) {
		store := &quotaStoreStub{}
		rec := invoke(t, Deps{
			Auth:  quotaAuthStub{ident: admin.AdminIdentity{TokenID: 5, Role: "user"}},
			Store: store,
		}, http.MethodGet, "/", "")
		assertStatus(t, rec, http.StatusForbidden)
		if code := errorCode(t, rec); code != "admin_forbidden_scope" {
			t.Fatalf("code=%q want admin_forbidden_scope", code)
		}
		if store.listCalls != 0 {
			t.Fatalf("forbidden touched store")
		}
	})
	t.Run("anon 401", func(t *testing.T) {
		store := &quotaStoreStub{}
		rec := invoke(t, Deps{Auth: quotaAuthStub{err: admin.ErrAdminUnauthorized}, Store: store},
			http.MethodGet, "/", "")
		assertStatus(t, rec, http.StatusUnauthorized)
		if store.listCalls != 0 {
			t.Fatalf("unauthorized touched store")
		}
	})
}

// TestPlatformAdminTenantScoping 与 adminuserhttp 保持一致:platform_admin
// 必须传 ?tenant_id(不传则 400),且解析出的 tenant_id 会流入读取操作。
func TestPlatformAdminTenantScoping(t *testing.T) {
	t.Run("requires tenant_id", func(t *testing.T) {
		store := &quotaStoreStub{}
		rec := invoke(t, Deps{Auth: quotaAuthStub{ident: platformAdmin()}, Store: store},
			http.MethodGet, "/", "")
		assertStatus(t, rec, http.StatusBadRequest)
		if store.listCalls != 0 {
			t.Fatalf("missing tenant_id reached store")
		}
	})
	t.Run("with tenant_id scopes read", func(t *testing.T) {
		store := &quotaStoreStub{}
		rec := invoke(t, Deps{Auth: quotaAuthStub{ident: platformAdmin()}, Store: store},
			http.MethodGet, "/?tenant_id=4", "")
		assertStatus(t, rec, http.StatusOK)
		if store.listArg.TenantID != 4 {
			t.Fatalf("tenant scope=%d want 4", store.listArg.TenantID)
		}
	})
}

// TestGetCrossTenantForbidden 证明 tenant_operator 无法取到另一租户的 policy:
// operator 作用域为 7 却带 ?tenant_id=8 -> 在触及 store 之前返回 403。
// 变异:去掉 CanActOnTenant 会让跨租户读取通过。
func TestGetCrossTenantForbidden(t *testing.T) {
	store := &quotaStoreStub{getRow: dbquota.QuotaPolicy{ID: 1, TenantID: 8}}
	rec := invoke(t, Deps{Auth: quotaAuthStub{ident: tenantOperator(7)}, Store: store},
		http.MethodGet, "/55?tenant_id=8", "")
	assertStatus(t, rec, http.StatusForbidden)
	if store.getCalls != 0 {
		t.Fatalf("cross-tenant get touched store: calls=%d", store.getCalls)
	}
}

// TestGetNotFound 证明缺失的行会映射为 404 quota_policy_not_found。
func TestGetNotFound(t *testing.T) {
	store := &quotaStoreStub{getErr: pgx.ErrNoRows}
	rec := invoke(t, Deps{Auth: quotaAuthStub{ident: tenantOperator(7)}, Store: store},
		http.MethodGet, "/55", "")
	assertStatus(t, rec, http.StatusNotFound)
	if code := errorCode(t, rec); code != "quota_policy_not_found" {
		t.Fatalf("code=%q want quota_policy_not_found", code)
	}
	if store.getArg.TenantID != 7 || store.getArg.ID != 55 {
		t.Fatalf("get arg wrong: %+v", store.getArg)
	}
}

// TestDeleteInUseMapsTo409 证明 FK RESTRICT 哨兵错误(errQuotaPolicyInUse,
// 由 SQLSTATE 23503 抛出)会被表现为 409 quota_policy_in_use,而非 503。
// 变异:把 errQuotaPolicyInUse 映射到默认的 503 分支,会使 409 +
// quota_policy_in_use 断言变红。
func TestDeleteInUseMapsTo409(t *testing.T) {
	store := &quotaStoreStub{deleteErr: errQuotaPolicyInUse}
	rec := invoke(t, Deps{Auth: quotaAuthStub{ident: tenantOperator(7)}, Store: store},
		http.MethodDelete, "/55", "")
	assertStatus(t, rec, http.StatusConflict)
	if code := errorCode(t, rec); code != "quota_policy_in_use" {
		t.Fatalf("code=%q want quota_policy_in_use", code)
	}
}

// TestDeleteCleanReturns200 证明一次干净的删除返回 200 {deleted:true},
// 且审计输入携带 action=delete_quota_policy。
func TestDeleteCleanReturns200(t *testing.T) {
	store := &quotaStoreStub{deleteID: 55}
	rec := invoke(t, Deps{Auth: quotaAuthStub{ident: tenantOperator(7)}, Store: store},
		http.MethodDelete, "/55", "")
	assertStatus(t, rec, http.StatusOK)
	var body quotaPolicyDeleteResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode delete body: %v", err)
	}
	if body.Object != "admin_quota_policy_deleted" || body.ID != 55 || !body.Deleted {
		t.Fatalf("delete response wrong: %+v", body)
	}
	if store.lastDelete.TenantID != 7 || store.lastDelete.ID != 55 {
		t.Fatalf("delete arg wrong: %+v", store.lastDelete)
	}
	if store.lastAudit.Action != "delete_quota_policy" || store.lastAudit.TenantID != 7 {
		t.Fatalf("delete audit wrong: %+v", store.lastAudit)
	}
}

// TestCreateConflictMapsTo409 证明 live-policy 唯一索引的哨兵错误会被表现为
// 409 quota_policy_conflict,而不是一个含糊的 503。
func TestCreateConflictMapsTo409(t *testing.T) {
	store := &quotaStoreStub{createErr: errQuotaPolicyConflict}
	rec := invoke(t, Deps{Auth: quotaAuthStub{ident: tenantOperator(7)}, Store: store},
		http.MethodPost, "/", validCreateBody)
	assertStatus(t, rec, http.StatusConflict)
	if code := errorCode(t, rec); code != "quota_policy_conflict" {
		t.Fatalf("code=%q want quota_policy_conflict", code)
	}
}

// TestUpdateEnabledToggle 证明 PUT 会把 enabled=false 流入 update params
// (reserve 路径的过滤依赖这一列)。
func TestUpdateEnabledToggle(t *testing.T) {
	store := &quotaStoreStub{updateRow: dbquota.QuotaPolicy{ID: 55, TenantID: 7, Enabled: false,
		LimitValue: numericFromString(t, "1"), BurstValue: numericFromString(t, "0"),
		ScopeKind: "user", ScopeID: "1", Metric: "cost_usd", WindowKind: "none", Mode: "enforce"}}
	body := `{"scope_kind":"user","scope_id":"1","metric":"cost_usd","window_kind":"none","limit_value":"1","enabled":false}`
	rec := invoke(t, Deps{Auth: quotaAuthStub{ident: tenantOperator(7)}, Store: store},
		http.MethodPut, "/55", body)
	assertStatus(t, rec, http.StatusOK)
	if store.lastUpdate.update.ID != 55 || store.lastUpdate.update.TenantID != 7 || store.lastUpdate.update.Enabled {
		t.Fatalf("update params wrong: %+v", store.lastUpdate.update)
	}
	if store.lastAudit.Action != "update_quota_policy" {
		t.Fatalf("update audit action=%q", store.lastAudit.Action)
	}
}

// TestListAdminIgnoresValidityAndModeFilters 证明 admin list 只传递显式的过滤
// (scope/metric/enabled),绝不注入 mode 或 valid_until 过滤 —— 运维必须能看到
// disabled+过期+shadow 的行。
func TestListAdminFiltersPassThrough(t *testing.T) {
	store := &quotaStoreStub{}
	rec := invoke(t, Deps{Auth: quotaAuthStub{ident: tenantOperator(7)}, Store: store},
		http.MethodGet, "/?scope_kind=user&metric=cost_usd&enabled=false&limit=999&offset=5", "")
	assertStatus(t, rec, http.StatusOK)
	if store.listArg.ScopeKind == nil || *store.listArg.ScopeKind != "user" {
		t.Fatalf("scope_kind filter not passed: %+v", store.listArg.ScopeKind)
	}
	if store.listArg.Metric == nil || *store.listArg.Metric != "cost_usd" {
		t.Fatalf("metric filter not passed: %+v", store.listArg.Metric)
	}
	if store.listArg.Enabled == nil || *store.listArg.Enabled {
		t.Fatalf("enabled=false filter not passed: %+v", store.listArg.Enabled)
	}
	if store.listArg.PageLimit != maxPageLimit || store.listArg.PageOffset != 5 {
		t.Fatalf("pagination caps wrong: limit=%d offset=%d", store.listArg.PageLimit, store.listArg.PageOffset)
	}
}
