package gatewayhttp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/shopspring/decimal"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/db"
)

type obsAuthStub struct{ err error }

func (a obsAuthStub) Resolve(context.Context, *http.Request) (admin.AdminIdentity, error) {
	if a.err != nil {
		return admin.AdminIdentity{}, a.err
	}
	return admin.AdminIdentity{TokenID: 1, Role: admin.RoleTenantOperator, ScopeTenantID: 7}, nil
}

type obsDepsStub struct {
	auth obsAuthStub
	s    *obsStoreStub
}

func (d obsDepsStub) AdminObservabilityAuth() AdminObservabilityAuth   { return d.auth }
func (d obsDepsStub) AdminObservabilityStore() AdminObservabilityStore { return d.s }

type obsStoreStub struct {
	usage, usageCursor []db.ListUsageRecordsRow
	claims             []db.ListBillingClaimsRow
	audit              []db.ListAuditEventsRow
	usageArg           db.ListUsageRecordsParams
	claimsArg          db.ListBillingClaimsParams
	auditArg           db.ListAuditEventsParams
}

func (s *obsStoreStub) CountUsageRecords(context.Context, db.CountUsageRecordsParams) (int64, error) {
	return int64(len(s.usage)), nil
}
func (s *obsStoreStub) ListUsageRecords(_ context.Context, arg db.ListUsageRecordsParams) ([]db.ListUsageRecordsRow, error) {
	s.usageArg = arg
	if arg.HasCursor && s.usageCursor != nil {
		return s.usageCursor, nil
	}
	return s.usage, nil
}
func (s *obsStoreStub) CountBillingClaims(context.Context, db.CountBillingClaimsParams) (int64, error) {
	return int64(len(s.claims)), nil
}
func (s *obsStoreStub) ListBillingClaims(_ context.Context, arg db.ListBillingClaimsParams) ([]db.ListBillingClaimsRow, error) {
	s.claimsArg = arg
	return s.claims, nil
}
func (s *obsStoreStub) CountAuditEvents(context.Context, db.CountAuditEventsParams) (int64, error) {
	return int64(len(s.audit)), nil
}
func (s *obsStoreStub) ListAuditEvents(_ context.Context, arg db.ListAuditEventsParams) ([]db.ListAuditEventsRow, error) {
	s.auditArg = arg
	return s.audit, nil
}

func TestAdminObsUsageSuccessWithFilters(t *testing.T) {
	store := &obsStoreStub{usage: []db.ListUsageRecordsRow{usageRow(1)}}
	rec := invokeObs(NewUsageHandler(obsDepsStub{auth: obsAuthStub{}, s: store}), "/admin/v1/usage?from=2026-05-01T00:00:00Z&provider=anthropic&pool_id=9&limit=50")
	assertStatus(t, rec, http.StatusOK)
	if store.usageArg.PageLimit != 51 || store.usageArg.Provider == nil || *store.usageArg.Provider != "anthropic" || store.usageArg.PoolID == nil || *store.usageArg.PoolID != 9 {
		t.Fatalf("usage params mismatch: %+v", store.usageArg)
	}
}

func TestAdminObsClaimsSuccess(t *testing.T) {
	store := &obsStoreStub{claims: []db.ListBillingClaimsRow{claimRow(3)}}
	rec := invokeObs(NewClaimsHandler(obsDepsStub{auth: obsAuthStub{}, s: store}), "/admin/v1/billing/claims?status=committed")
	assertStatus(t, rec, http.StatusOK)
	if store.claimsArg.Status == nil || *store.claimsArg.Status != "committed" {
		t.Fatalf("claims status filter missing: %+v", store.claimsArg)
	}
}

func TestAdminObsAuditSuccess(t *testing.T) {
	store := &obsStoreStub{audit: []db.ListAuditEventsRow{auditRow(5, "error", "ledger-5")}}
	rec := invokeObs(NewAuditEventsHandler(obsDepsStub{auth: obsAuthStub{}, s: store}), "/admin/v1/audit-events?event_class=rate_limit")
	assertStatus(t, rec, http.StatusOK)
	if store.auditArg.EventClass == nil || *store.auditArg.EventClass != "rate_limit" {
		t.Fatalf("audit event_class filter missing: %+v", store.auditArg)
	}
}

func TestAdminObsUsageUnauthorized(t *testing.T) {
	h := NewUsageHandler(obsDepsStub{auth: obsAuthStub{err: admin.ErrAdminUnauthorized}, s: &obsStoreStub{}})
	assertStatus(t, invokeObs(h, "/admin/v1/usage"), http.StatusUnauthorized)
}

func TestAdminObsClaimsUnauthorized(t *testing.T) {
	h := NewClaimsHandler(obsDepsStub{auth: obsAuthStub{err: admin.ErrAdminUnauthorized}, s: &obsStoreStub{}})
	assertStatus(t, invokeObs(h, "/admin/v1/billing/claims"), http.StatusUnauthorized)
}

func TestAdminObsAuditUnauthorized(t *testing.T) {
	h := NewAuditEventsHandler(obsDepsStub{auth: obsAuthStub{err: admin.ErrAdminUnauthorized}, s: &obsStoreStub{}})
	assertStatus(t, invokeObs(h, "/admin/v1/audit-events"), http.StatusUnauthorized)
}

func TestAdminObsBadCursor(t *testing.T) {
	deps := obsDepsStub{auth: obsAuthStub{}, s: &obsStoreStub{}}
	assertStatus(t, invokeObs(NewUsageHandler(deps), "/admin/v1/usage?cursor=@@@"), http.StatusBadRequest)
}

func TestAdminObsLargeLimit(t *testing.T) {
	deps := obsDepsStub{auth: obsAuthStub{}, s: &obsStoreStub{}}
	assertStatus(t, invokeObs(NewClaimsHandler(deps), "/admin/v1/billing/claims?limit=201"), http.StatusBadRequest)
}

func TestAdminObsPaginationRoundTrip(t *testing.T) {
	store := &obsStoreStub{usage: []db.ListUsageRecordsRow{usageRow(3), usageRow(2), usageRow(1)}, usageCursor: []db.ListUsageRecordsRow{usageRow(1)}}
	h := NewUsageHandler(obsDepsStub{auth: obsAuthStub{}, s: store})
	rec := invokeObs(h, "/admin/v1/usage?limit=2")
	assertStatus(t, rec, http.StatusOK)
	var body struct {
		Items      []map[string]any `json:"items"`
		NextCursor string           `json:"next_cursor"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || len(body.Items) != 2 || body.NextCursor == "" {
		t.Fatalf("bad first page body=%s err=%v", rec.Body.String(), err)
	}
	rec = invokeObs(h, "/admin/v1/usage?limit=2&cursor="+body.NextCursor)
	assertStatus(t, rec, http.StatusOK)
	if !store.usageArg.HasCursor || store.usageArg.CursorID != 2 {
		t.Fatalf("cursor not replayed into query params: %+v", store.usageArg)
	}
}

func TestAdminObsAuditFiltersNarrowQuery(t *testing.T) {
	store := &obsStoreStub{audit: []db.ListAuditEventsRow{auditRow(9, "error", "ledger-9")}}
	rec := invokeObs(NewAuditEventsHandler(obsDepsStub{auth: obsAuthStub{}, s: store}), "/admin/v1/audit-events?event_type=permanent_disable_set&severity=error&ledger_id=ledger-9")
	assertStatus(t, rec, http.StatusOK)
	if *store.auditArg.EventType != "permanent_disable_set" || *store.auditArg.Severity != "error" || *store.auditArg.LedgerID != "ledger-9" {
		t.Fatalf("audit filters mismatch: %+v", store.auditArg)
	}
}

func invokeObs(h http.HandlerFunc, target string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, target, strings.NewReader(""))
	rec := httptest.NewRecorder()
	h(rec, req)
	return rec
}

func assertStatus(t *testing.T, rec *httptest.ResponseRecorder, want int) {
	t.Helper()
	if rec.Code != want {
		t.Fatalf("status=%d want=%d body=%s", rec.Code, want, rec.Body.String())
	}
}

func usageRow(id int64) db.ListUsageRecordsRow {
	return db.ListUsageRecordsRow{ID: id, TenantID: 7, ClaimID: 20 + id, APIKeyID: 30, UserID: 40, ProviderAccountID: 50, AttemptSeq: 1, ActualCost: decimal.RequireFromString("0.01000000"), EndClass: "non_streaming", UsageSource: "reported", CreatedAt: ts(id)}
}
func claimRow(id int64) db.ListBillingClaimsRow {
	return db.ListBillingClaimsRow{ID: id, TenantID: 7, IdempotencyKey: "idem", APIKeyID: 1, UserID: 2, LogicalRequestID: "lr", EndpointFamily: "chat", RequestedModel: "m", AttemptSeq: 1, PredictedCost: decimal.RequireFromString("0.01000000"), CurrencyCode: "USD", Status: "committed", CreatedAt: ts(id)}
}
func auditRow(id int64, severity, ledgerID string) db.ListAuditEventsRow {
	return db.ListAuditEventsRow{ID: id, TenantID: 7, EventClass: "rate_limit", EventType: "permanent_disable_set", Severity: severity, LedgerID: ledgerID, Payload: []byte(`{"ok":true}`), CreatedAt: ts(id)}
}
func ts(id int64) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: time.Date(2026, 5, 14, 0, 0, int(id), 0, time.UTC), Valid: true}
}
