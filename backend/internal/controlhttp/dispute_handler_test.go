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

// Mutation: skip GetReceiptForUser or call it with the wrong user_id.
// User A would be able to create a dispute for user B's receipt; this must go red.
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

// Mutation: take tenant_id/user_id from JSON or query instead of session.
// The created dispute would be scoped to attacker-supplied identity.
func TestCreateDisputeUsesSessionScopeAndRejectsDuplicate(t *testing.T) {
	receipts := &disputeFakeReceiptReader{receipt: &audit.CostReceipt{TenantID: 7, UserID: 42, RequestID: "req-own"}}
	store := &disputeFakeStore{createErr: audit.ErrDisputeDuplicate}
	router := disputeUserRouter(DisputeUserDeps{Receipts: receipts, Store: store}, sessionauth.SessionIdentity{TenantID: 7, UserID: 42})

	rec := doDisputeJSON(router, http.MethodPost, "/v1/receipts/req-own/disputes", `{"reason":"charged twice","tenant_id":99,"user_id":99}`)

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

// Mutation: handler passes zero/wrong user_id to DisputeStore.ListUserDisputes.
// The fake store filters by the supplied args; wrong scope leaks or drops the discriminating row.
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

// Mutation: resolve handler ignores status/operator_note or keeps old status.
// Operator recovery must persist the state transition visibly.
func TestAdminResolveDisputeChangesStatusAndNote(t *testing.T) {
	store := &disputeFakeStore{
		resolveReturn: disputeResolved(55, 7, 42, "req-r", audit.DisputeStatusResolved, "receipt checked"),
	}
	router := disputeAdminRouter(DisputeAdminDeps{
		Auth:  disputeFakeAdminAuth{ident: admin.AdminIdentity{TokenID: 77, Role: admin.RolePlatformAdmin}},
		Store: store,
	})

	rec := doDisputeJSON(router, http.MethodPost, "/v1/admin/disputes/55/resolve",
		`{"tenant_id":7,"status":"resolved","operator_note":"receipt checked"}`)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d want 200 body=%s", rec.Code, rec.Body.String())
	}
	if !store.resolveCalled {
		t.Fatal("ResolveDispute not called")
	}
	if store.resolveArg.TenantID != 7 || store.resolveArg.ID != 55 ||
		store.resolveArg.Status != audit.DisputeStatusResolved ||
		store.resolveArg.OperatorNote != "receipt checked" {
		t.Fatalf("resolve arg=%+v, want tenant=7 id=55 status=resolved note", store.resolveArg)
	}
	if !bytes.Contains(rec.Body.Bytes(), []byte(`"status":"resolved"`)) ||
		!bytes.Contains(rec.Body.Bytes(), []byte(`"operator_note":"receipt checked"`)) {
		t.Fatalf("response did not expose updated status/note: %s", rec.Body.String())
	}
}

// Mutation: omit ident.CanIssueForTenant before resolve.
// A tenant operator for tenant 7 could resolve tenant 8 disputes.
func TestAdminResolveTenantOperatorCannotCrossTenant(t *testing.T) {
	store := &disputeFakeStore{}
	router := disputeAdminRouter(DisputeAdminDeps{
		Auth:  disputeFakeAdminAuth{ident: admin.AdminIdentity{TokenID: 88, Role: admin.RoleTenantOperator, ScopeTenantID: 7}},
		Store: store,
	})

	rec := doDisputeJSON(router, http.MethodPost, "/v1/admin/disputes/55/resolve",
		`{"tenant_id":8,"status":"rejected","operator_note":"wrong tenant"}`)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status=%d want 403 body=%s", rec.Code, rec.Body.String())
	}
	if store.resolveCalled {
		t.Fatal("ResolveDispute must not run for cross-tenant operator")
	}
}

// Mutation: reuse ListUserDisputes or otherwise pass a user_id filter for admin list.
// A tenant admin must see disputes from multiple users in the same tenant.
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

// Mutation: ignore status query parameter before calling the store.
// The fake store filters by the supplied status; an empty status would return open and resolved rows.
func TestAdminListDisputesStatusFilter(t *testing.T) {
	store := &disputeFakeStore{rows: []audit.CostDispute{
		dispute(1, 7, 42, "req-open", audit.DisputeStatusOpen),
		dispute(2, 7, 99, "req-resolved", audit.DisputeStatusResolved),
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
			RequestID string `json:"request_id"`
			Status    string `json:"status"`
		} `json:"disputes"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v body=%s", err, rec.Body.String())
	}
	if len(body.Disputes) != 1 || body.Disputes[0].RequestID != "req-resolved" || body.Disputes[0].Status != audit.DisputeStatusResolved {
		t.Fatalf("disputes=%+v, want only resolved req-resolved", body.Disputes)
	}
}

// Mutation: do not cap limit or ignore offset.
// The handler must send capped limit=500 and offset=2 to the store.
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

// Mutation: skip admin role validation after auth resolves an unsupported role.
// A resolved but non-admin role must be rejected before the store runs.
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

	resolveCalled bool
	resolveArg    audit.ResolveCostDisputeInput
	resolveReturn audit.CostDispute
	resolveErr    error
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

func (f *disputeFakeStore) ResolveDispute(_ context.Context, in audit.ResolveCostDisputeInput) (audit.CostDispute, error) {
	f.resolveCalled = true
	f.resolveArg = in
	if f.resolveErr != nil {
		return audit.CostDispute{}, f.resolveErr
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
