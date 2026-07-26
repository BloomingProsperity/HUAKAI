package controlhttp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/audit"
	sessionauth "github.com/BloomingProsperity/HUAKAI/internal/auth"
)

// 变异:跳过 GetReceiptForUser 或用错误的 user_id 调用它。
// 用户 A 将能为用户 B 的 receipt 创建 dispute;此处必须变红。
func TestCreateDisputeRejectsReceiptOwnedByAnotherUser(t *testing.T) {
	receipts := &disputeFakeReceiptReader{err: audit.ErrReceiptNotFound}
	store := &disputeFakeStore{}
	router := disputeUserRouter(DisputeUserDeps{Receipts: receipts, Store: store}, sessionauth.SessionIdentity{TenantID: 7, UserID: 42})

	rec := doDisputeJSON(router, http.MethodPost, "/v1/receipts/req-user-b/disputes", `{"reason":"not my charge"}`)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status=%d want 404 body=%s", rec.Code, rec.Body.String())
	}
	if store.createCalled {
		t.Fatal("CreateDispute must not run when receipt owner check fails")
	}
	if receipts.gotTenantID != 7 || receipts.gotUserID != 42 || receipts.gotRequestID != "req-user-b" {
		t.Fatalf("receipt lookup scope=(tenant=%d,user=%d,request=%q), want (7,42,req-user-b)",
			receipts.gotTenantID, receipts.gotUserID, receipts.gotRequestID)
	}
}

// 变异:从 JSON 或 query 取 tenant_id/user_id 而非从 session 取。
// 合法请求只能使用 session 身份，重复争议保持明确冲突。
func TestCreateDisputeUsesSessionScopeAndRejectsDuplicate(t *testing.T) {
	receipts := &disputeFakeReceiptReader{receipt: &audit.CostReceipt{TenantID: 7, UserID: 42, RequestID: "req-own"}}
	store := &disputeFakeStore{createErr: audit.ErrDisputeDuplicate}
	router := disputeUserRouter(DisputeUserDeps{Receipts: receipts, Store: store}, sessionauth.SessionIdentity{TenantID: 7, UserID: 42})

	rec := doDisputeJSON(router, http.MethodPost, "/v1/receipts/req-own/disputes", `{"reason":"charged twice"}`)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status=%d want 409 duplicate body=%s", rec.Code, rec.Body.String())
	}
	if !store.createCalled {
		t.Fatal("CreateDispute should run after own receipt is verified")
	}
	if store.createArg.TenantID != 7 || store.createArg.UserID != 42 || store.createArg.RequestID != "req-own" {
		t.Fatalf("create arg=%+v, want auth-derived tenant/user/request", store.createArg)
	}
}

func TestCreateDisputeRejectsUnknownIdentityFields(t *testing.T) {
	receipts := &disputeFakeReceiptReader{receipt: &audit.CostReceipt{TenantID: 7, UserID: 42, RequestID: "req-own"}}
	store := &disputeFakeStore{}
	router := disputeUserRouter(DisputeUserDeps{Receipts: receipts, Store: store}, sessionauth.SessionIdentity{TenantID: 7, UserID: 42})

	rec := doDisputeJSON(router, http.MethodPost, "/v1/receipts/req-own/disputes", `{"reason":"charged twice","tenant_id":99,"user_id":99}`)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d want 400 body=%s", rec.Code, rec.Body.String())
	}
	if store.createCalled {
		t.Fatal("unknown identity fields must be rejected before CreateDispute")
	}
}

// 变异:handler 把零值/错误的 user_id 传给 DisputeStore.ListUserDisputes。
// fake store 按传入参数过滤;错误的 scope 会泄露或丢掉那条区分行。
func TestListMyDisputesIsScopedToSessionUser(t *testing.T) {
	store := &disputeFakeStore{rows: []audit.CostDispute{
		dispute(1, 7, 42, "req-a", audit.DisputeStatusOpen),
		dispute(2, 7, 99, "req-b", audit.DisputeStatusResolved),
		dispute(3, 8, 42, "req-c", audit.DisputeStatusOpen),
	}}
	router := disputeUserRouter(DisputeUserDeps{Store: store}, sessionauth.SessionIdentity{TenantID: 7, UserID: 42})

	rec := doDisputeJSON(router, http.MethodGet, "/v1/me/disputes?tenant_id=8&user_id=99", "")

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d want 200 body=%s", rec.Code, rec.Body.String())
	}
	if store.listTenantID != 7 || store.listUserID != 42 {
		t.Fatalf("list scope=(tenant=%d,user=%d), want (7,42)", store.listTenantID, store.listUserID)
	}
	var body struct {
		Disputes []struct {
			RequestID string `json:"request_id"`
			UserID    int64  `json:"user_id"`
		} `json:"disputes"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v body=%s", err, rec.Body.String())
	}
	if len(body.Disputes) != 1 || body.Disputes[0].RequestID != "req-a" || body.Disputes[0].UserID != 42 {
		t.Fatalf("body=%+v, want only user 42 req-a", body.Disputes)
	}
}

// 变异:resolve handler 忽略 status/operator_note 或保留旧 status。
// 运营者恢复操作必须可见地持久化这次状态迁移。
func TestAdminResolveDisputeChangesStatusAndNote(t *testing.T) {
	resolver := &disputeFakeResolver{
		resolveReturn: audit.ResolveCostDisputeResult{
			Dispute:             disputeResolved(55, 7, 42, "req-r", audit.DisputeStatusResolved, "receipt checked"),
			RefundMicroUSD:      12345,
			RefundAdjustmentRef: "billing_event:91",
		},
	}
	router := disputeAdminRouter(DisputeAdminDeps{
		Auth:     disputeFakeAdminAuth{ident: admin.AdminIdentity{TokenID: 77, Role: admin.RolePlatformAdmin}},
		Resolver: resolver,
	})

	rec := doDisputeJSON(router, http.MethodPost, "/v1/admin/disputes/55/resolve",
		`{"tenant_id":7,"status":"resolved","operator_note":"receipt checked"}`)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d want 200 body=%s", rec.Code, rec.Body.String())
	}
	if !resolver.resolveCalled {
		t.Fatal("ResolveDispute not called")
	}
	if resolver.resolveArg.TenantID != 7 || resolver.resolveArg.ID != 55 ||
		resolver.resolveArg.Status != audit.DisputeStatusResolved ||
		resolver.resolveArg.OperatorNote != "receipt checked" {
		t.Fatalf("resolve arg=%+v, want tenant=7 id=55 status=resolved note", resolver.resolveArg)
	}
	if !bytes.Contains(rec.Body.Bytes(), []byte(`"status":"resolved"`)) ||
		!bytes.Contains(rec.Body.Bytes(), []byte(`"operator_note":"receipt checked"`)) ||
		!bytes.Contains(rec.Body.Bytes(), []byte(`"refund_micro_usd":12345`)) ||
		!bytes.Contains(rec.Body.Bytes(), []byte(`"refund_adjustment_ref":"billing_event:91"`)) {
		t.Fatalf("response did not expose updated status/refund: %s", rec.Body.String())
	}
}

func TestAdminResolveDisputeRejectsUnknownOrTrailingJSON(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{name: "未知字段", body: `{"tenant_id":7,"status":"resolved","operator_note":"checked","actor_id":99}`},
		{name: "尾随对象", body: `{"tenant_id":7,"status":"resolved","operator_note":"checked"}{"tenant_id":8}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resolver := &disputeFakeResolver{}
			router := disputeAdminRouter(DisputeAdminDeps{
				Auth:     disputeFakeAdminAuth{ident: admin.AdminIdentity{TokenID: 77, Role: admin.RolePlatformAdmin}},
				Resolver: resolver,
			})

			rec := doDisputeJSON(router, http.MethodPost, "/v1/admin/disputes/55/resolve", tt.body)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status=%d want 400 body=%s", rec.Code, rec.Body.String())
			}
			if resolver.resolveCalled {
				t.Fatal("invalid JSON contract must not reach ResolveDispute")
			}
		})
	}
}

// 变异:在 resolve 之前省略 ident.CanIssueForTenant。
// tenant 7 的租户运营者将能 resolve tenant 8 的 dispute。
func TestAdminResolveTenantOperatorCannotCrossTenant(t *testing.T) {
	resolver := &disputeFakeResolver{}
	router := disputeAdminRouter(DisputeAdminDeps{
		Auth:     disputeFakeAdminAuth{ident: admin.AdminIdentity{TokenID: 88, Role: admin.RoleTenantOperator, ScopeTenantID: 7}},
		Resolver: resolver,
	})

	rec := doDisputeJSON(router, http.MethodPost, "/v1/admin/disputes/55/resolve",
		`{"tenant_id":8,"status":"rejected","operator_note":"wrong tenant"}`)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status=%d want 403 body=%s", rec.Code, rec.Body.String())
	}
	if resolver.resolveCalled {
		t.Fatal("ResolveDispute must not run for cross-tenant operator")
	}
}

// 部署者可跨租户查看争议，但资金性处理只能落在平台工作租户。删除所属
// 租户守卫后，resolver 会被调用，本测试立即变红。
func TestAdminResolvePlatformAdminCannotOperateDownstreamTenant(t *testing.T) {
	resolver := &disputeFakeResolver{}
	router := disputeAdminRouter(DisputeAdminDeps{
		Auth:             disputeFakeAdminAuth{ident: admin.AdminIdentity{TokenID: 77, Role: admin.RolePlatformAdmin}},
		Resolver:         resolver,
		PlatformTenantID: 7,
	})

	rec := doDisputeJSON(router, http.MethodPost, "/v1/admin/disputes/55/resolve",
		`{"tenant_id":8,"status":"resolved","operator_note":"downstream"}`)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status=%d want 403 body=%s", rec.Code, rec.Body.String())
	}
	if resolver.resolveCalled {
		t.Fatal("downstream dispute must not reach ResolveDispute")
	}
}

// 变异:把终态守卫零行错误继续映射成普通 404/503。
// 运营重放必须得到明确冲突，且不能误以为可以安全再试同一终态裁决。
func TestAdminResolveTerminalDisputeReturnsConflict(t *testing.T) {
	resolver := &disputeFakeResolver{resolveErr: audit.ErrDisputeNotResolvable}
	router := disputeAdminRouter(DisputeAdminDeps{
		Auth:     disputeFakeAdminAuth{ident: admin.AdminIdentity{TokenID: 77, Role: admin.RolePlatformAdmin}},
		Resolver: resolver,
	})
	rec := doDisputeJSON(router, http.MethodPost, "/v1/admin/disputes/55/resolve",
		`{"tenant_id":7,"status":"resolved","operator_note":"retry"}`)
	if rec.Code != http.StatusConflict || !bytes.Contains(rec.Body.Bytes(), []byte("dispute_not_resolvable")) {
		t.Fatalf("status=%d body=%s, want explicit 409 terminal conflict", rec.Code, rec.Body.String())
	}
}

// 变异:查不到 committed claim 时仍提交 resolved 状态。
// 组合层的明确失败必须保留为 400，不能伪装成后端暂时不可用或成功。
func TestAdminResolveWithoutCommittedChargeReturnsBadRequest(t *testing.T) {
	resolver := &disputeFakeResolver{resolveErr: audit.ErrDisputeNoCharge}
	router := disputeAdminRouter(DisputeAdminDeps{
		Auth:     disputeFakeAdminAuth{ident: admin.AdminIdentity{TokenID: 77, Role: admin.RolePlatformAdmin}},
		Resolver: resolver,
	})
	rec := doDisputeJSON(router, http.MethodPost, "/v1/admin/disputes/55/resolve",
		`{"tenant_id":7,"status":"resolved","operator_note":"support"}`)
	if rec.Code != http.StatusBadRequest || !bytes.Contains(rec.Body.Bytes(), []byte("dispute_charge_not_committed")) {
		t.Fatalf("status=%d body=%s, want explicit 400 no committed charge", rec.Code, rec.Body.String())
	}
}

func TestAdminResolveAmbiguousChargeReturnsConflict(t *testing.T) {
	resolver := &disputeFakeResolver{resolveErr: audit.ErrDisputeAmbiguousCharge}
	router := disputeAdminRouter(DisputeAdminDeps{
		Auth:     disputeFakeAdminAuth{ident: admin.AdminIdentity{TokenID: 77, Role: admin.RolePlatformAdmin}},
		Resolver: resolver,
	})
	rec := doDisputeJSON(router, http.MethodPost, "/v1/admin/disputes/55/resolve",
		`{"tenant_id":7,"status":"resolved","operator_note":"support"}`)
	if rec.Code != http.StatusConflict || !bytes.Contains(rec.Body.Bytes(), []byte("dispute_charge_ambiguous")) {
		t.Fatalf("status=%d body=%s, want explicit 409 ambiguous charge", rec.Code, rec.Body.String())
	}
}

// 变异:rejected 路径伪造退款字段，前端会误报用户已收到余额。
func TestAdminRejectDisputeOmitsRefundFields(t *testing.T) {
	resolver := &disputeFakeResolver{resolveReturn: audit.ResolveCostDisputeResult{
		Dispute: disputeResolved(55, 7, 42, "req-r", audit.DisputeStatusRejected, "charge upheld"),
	}}
	router := disputeAdminRouter(DisputeAdminDeps{
		Auth:     disputeFakeAdminAuth{ident: admin.AdminIdentity{TokenID: 77, Role: admin.RolePlatformAdmin}},
		Resolver: resolver,
	})
	rec := doDisputeJSON(router, http.MethodPost, "/v1/admin/disputes/55/resolve",
		`{"tenant_id":7,"status":"rejected","operator_note":"charge upheld"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if bytes.Contains(rec.Body.Bytes(), []byte("refund_micro_usd")) || bytes.Contains(rec.Body.Bytes(), []byte("refund_adjustment_ref")) {
		t.Fatalf("rejected response must omit refund fields: %s", rec.Body.String())
	}
}

// 变异:在 admin 列表中复用 ListUserDisputes 或以其它方式传入 user_id 过滤。
// 租户 admin 必须能看到同一 tenant 内多个用户的 dispute。
func TestAdminListDisputesSeesMultipleUsersInTenant(t *testing.T) {
	store := &disputeFakeStore{rows: []audit.CostDispute{
		dispute(1, 7, 42, "req-user-a", audit.DisputeStatusOpen),
		dispute(2, 7, 99, "req-user-b", audit.DisputeStatusReviewing),
		dispute(3, 8, 42, "req-other-tenant", audit.DisputeStatusOpen),
	}}
	router := disputeAdminRouter(DisputeAdminDeps{
		Auth:  disputeFakeAdminAuth{ident: admin.AdminIdentity{TokenID: 91, Role: admin.RoleTenantOperator, ScopeTenantID: 7}},
		Store: store,
	})

	rec := doDisputeJSON(router, http.MethodGet, "/v1/admin/disputes?limit=10", "")

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d want 200 body=%s", rec.Code, rec.Body.String())
	}
	if !store.adminListCalled {
		t.Fatal("ListForAdmin not called")
	}
	if store.adminListTenantID != 7 || store.adminListStatus != "" ||
		store.adminListLimit != 10 || store.adminListOffset != 0 {
		t.Fatalf("admin list args tenant=%d status=%q limit=%d offset=%d, want tenant=7 status='' limit=10 offset=0",
			store.adminListTenantID, store.adminListStatus, store.adminListLimit, store.adminListOffset)
	}
	var body struct {
		Disputes []struct {
			RequestID string `json:"request_id"`
			UserID    int64  `json:"user_id"`
			TenantID  int64  `json:"tenant_id"`
		} `json:"disputes"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v body=%s", err, rec.Body.String())
	}
	if len(body.Disputes) != 2 {
		t.Fatalf("disputes=%+v, want two tenant 7 rows across users", body.Disputes)
	}
	got := map[string]int64{}
	for _, row := range body.Disputes {
		got[row.RequestID] = row.UserID
		if row.TenantID != 7 {
			t.Fatalf("tenant leak row=%+v", row)
		}
	}
	if got["req-user-a"] != 42 || got["req-user-b"] != 99 {
		t.Fatalf("disputes=%+v, want req-user-a user 42 and req-user-b user 99", body.Disputes)
	}
}

// 变异:调用 store 之前忽略 status query 参数。
// fake store 按传入的 status 过滤;空 status 会返回 open 和 resolved 的行。
func TestAdminListDisputesStatusFilter(t *testing.T) {
	resolved := dispute(2, 7, 99, "req-resolved", audit.DisputeStatusResolved)
	resolved.RefundedMicroUSD = 54321
	store := &disputeFakeStore{rows: []audit.CostDispute{
		dispute(1, 7, 42, "req-open", audit.DisputeStatusOpen),
		resolved,
		dispute(3, 7, 100, "req-rejected", audit.DisputeStatusRejected),
	}}
	router := disputeAdminRouter(DisputeAdminDeps{
		Auth:  disputeFakeAdminAuth{ident: admin.AdminIdentity{TokenID: 92, Role: admin.RoleTenantOperator, ScopeTenantID: 7}},
		Store: store,
	})

	rec := doDisputeJSON(router, http.MethodGet, "/v1/admin/disputes?status=resolved", "")

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d want 200 body=%s", rec.Code, rec.Body.String())
	}
	if store.adminListStatus != audit.DisputeStatusResolved {
		t.Fatalf("status filter=%q want resolved", store.adminListStatus)
	}
	var body struct {
		Disputes []struct {
			RequestID        string `json:"request_id"`
			Status           string `json:"status"`
			RefundedMicroUSD int64  `json:"refunded_micro_usd"`
		} `json:"disputes"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v body=%s", err, rec.Body.String())
	}
	if len(body.Disputes) != 1 || body.Disputes[0].RequestID != "req-resolved" ||
		body.Disputes[0].Status != audit.DisputeStatusResolved || body.Disputes[0].RefundedMicroUSD != 54321 {
		t.Fatalf("disputes=%+v, want only resolved req-resolved", body.Disputes)
	}
}

// 变异:不对 limit 封顶或忽略 offset。
// handler 必须把封顶后的 limit=500 和 offset=2 发给 store。
func TestAdminListDisputesPaginationCapsLimitAndPassesOffset(t *testing.T) {
	store := &disputeFakeStore{rows: []audit.CostDispute{
		dispute(1, 7, 42, "req-0", audit.DisputeStatusOpen),
		dispute(2, 7, 42, "req-1", audit.DisputeStatusOpen),
		dispute(3, 7, 42, "req-2", audit.DisputeStatusOpen),
		dispute(4, 7, 42, "req-3", audit.DisputeStatusOpen),
	}}
	router := disputeAdminRouter(DisputeAdminDeps{
		Auth:  disputeFakeAdminAuth{ident: admin.AdminIdentity{TokenID: 93, Role: admin.RoleTenantOperator, ScopeTenantID: 7}},
		Store: store,
	})

	rec := doDisputeJSON(router, http.MethodGet, "/v1/admin/disputes?limit=999&offset=2", "")

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d want 200 body=%s", rec.Code, rec.Body.String())
	}
	if store.adminListLimit != 500 || store.adminListOffset != 2 {
		t.Fatalf("pagination limit=%d offset=%d, want capped limit=500 offset=2", store.adminListLimit, store.adminListOffset)
	}
}

// 变异:在 auth 解析出不受支持的 role 之后跳过 admin role 校验。
// 已解析但非 admin 的 role 必须在 store 运行前被拒绝。
func TestAdminListDisputesAuthRequired(t *testing.T) {
	store := &disputeFakeStore{}
	router := disputeAdminRouter(DisputeAdminDeps{
		Auth:  disputeFakeAdminAuth{ident: admin.AdminIdentity{TokenID: 94, Role: "viewer", ScopeTenantID: 7}},
		Store: store,
	})

	rec := doDisputeJSON(router, http.MethodGet, "/v1/admin/disputes/?tenant_id=7", "")

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status=%d want 403 body=%s", rec.Code, rec.Body.String())
	}
	if store.adminListCalled {
		t.Fatal("ListForAdmin must not run for non-admin role")
	}
}

func disputeUserRouter(d DisputeUserDeps, ident sessionauth.SessionIdentity) http.Handler {
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			ctx := sessionauth.ContextWithSession(req.Context(), ident)
			next.ServeHTTP(w, req.WithContext(ctx))
		})
	})
	r.Post("/v1/receipts/{request_id}/disputes", NewCreateDisputeHandler(d))
	r.Get("/v1/me/disputes", NewListUserDisputesHandler(d))
	return r
}

func disputeAdminRouter(d DisputeAdminDeps) http.Handler {
	if d.PlatformTenantID == 0 {
		d.PlatformTenantID = 7
	}
	r := chi.NewRouter()
	r.Get("/v1/admin/disputes", NewAdminListDisputesHandler(d))
	r.Get("/v1/admin/disputes/", NewAdminListDisputesHandler(d))
	r.Post("/v1/admin/disputes/{id}/resolve", NewAdminResolveDisputeHandler(d))
	return r
}

func doDisputeJSON(h http.Handler, method, target, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

type disputeFakeReceiptReader struct {
	receipt *audit.CostReceipt
	err     error

	gotRequestID string
	gotTenantID  int64
	gotUserID    int64
}

func (f *disputeFakeReceiptReader) GetReceiptForUser(_ context.Context, requestID string, tenantID, userID int64) (*audit.CostReceipt, error) {
	f.gotRequestID = requestID
	f.gotTenantID = tenantID
	f.gotUserID = userID
	if f.err != nil {
		return nil, f.err
	}
	return f.receipt, nil
}

type disputeFakeStore struct {
	rows []audit.CostDispute

	createCalled bool
	createArg    audit.CreateCostDisputeInput
	createReturn audit.CostDispute
	createErr    error

	listTenantID int64
	listUserID   int64

	adminListCalled   bool
	adminListTenantID int64
	adminListStatus   string
	adminListLimit    int32
	adminListOffset   int32
}

func (f *disputeFakeStore) CreateDispute(_ context.Context, in audit.CreateCostDisputeInput) (audit.CostDispute, error) {
	f.createCalled = true
	f.createArg = in
	if f.createErr != nil {
		return audit.CostDispute{}, f.createErr
	}
	if f.createReturn.ID != 0 {
		return f.createReturn, nil
	}
	return dispute(10, in.TenantID, in.UserID, in.RequestID, audit.DisputeStatusOpen), nil
}

func (f *disputeFakeStore) ListUserDisputes(_ context.Context, tenantID, userID int64, _ int32) ([]audit.CostDispute, error) {
	f.listTenantID = tenantID
	f.listUserID = userID
	out := make([]audit.CostDispute, 0, len(f.rows))
	for _, row := range f.rows {
		if row.TenantID == tenantID && row.UserID == userID {
			out = append(out, row)
		}
	}
	return out, nil
}

func (f *disputeFakeStore) ListForAdmin(_ context.Context, tenantID int64, status string, limit, offset int32) ([]audit.CostDispute, error) {
	f.adminListCalled = true
	f.adminListTenantID = tenantID
	f.adminListStatus = status
	f.adminListLimit = limit
	f.adminListOffset = offset
	out := make([]audit.CostDispute, 0, len(f.rows))
	for _, row := range f.rows {
		if row.TenantID != tenantID {
			continue
		}
		if status != "" && row.Status != status {
			continue
		}
		out = append(out, row)
	}
	if offset > 0 {
		if int(offset) >= len(out) {
			return nil, nil
		}
		out = out[offset:]
	}
	if limit > 0 && int(limit) < len(out) {
		out = out[:limit]
	}
	return out, nil
}

type disputeFakeResolver struct {
	resolveCalled bool
	resolveArg    audit.ResolveCostDisputeInput
	resolveReturn audit.ResolveCostDisputeResult
	resolveErr    error
}

func (f *disputeFakeResolver) ResolveDispute(_ context.Context, in audit.ResolveCostDisputeInput) (audit.ResolveCostDisputeResult, error) {
	f.resolveCalled = true
	f.resolveArg = in
	if f.resolveErr != nil {
		return audit.ResolveCostDisputeResult{}, f.resolveErr
	}
	return f.resolveReturn, nil
}

type disputeFakeAdminAuth struct {
	ident admin.AdminIdentity
	err   error
}

func (f disputeFakeAdminAuth) Resolve(context.Context, *http.Request) (admin.AdminIdentity, error) {
	if f.err != nil {
		return admin.AdminIdentity{}, f.err
	}
	return f.ident, nil
}

func dispute(id, tenantID, userID int64, requestID, status string) audit.CostDispute {
	return audit.CostDispute{
		ID:        id,
		DisputeID: "disp_" + requestID,
		TenantID:  tenantID,
		UserID:    userID,
		RequestID: requestID,
		Reason:    "cost does not match receipt",
		Status:    status,
		CreatedAt: time.Date(2026, 6, 3, 9, 0, 0, 0, time.UTC),
	}
}

func disputeResolved(id, tenantID, userID int64, requestID, status, note string) audit.CostDispute {
	row := dispute(id, tenantID, userID, requestID, status)
	row.OperatorNote = note
	resolvedAt := time.Date(2026, 6, 3, 10, 0, 0, 0, time.UTC)
	row.ResolvedAt = &resolvedAt
	return row
}

var _ = errors.Is
