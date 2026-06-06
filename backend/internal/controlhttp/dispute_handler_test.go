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
