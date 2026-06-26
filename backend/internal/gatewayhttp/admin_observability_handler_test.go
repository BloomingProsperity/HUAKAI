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
	dbbilling "github.com/BloomingProsperity/HUAKAI/internal/db/billing"
	"github.com/BloomingProsperity/HUAKAI/internal/proto"
	"github.com/BloomingProsperity/HUAKAI/internal/trust"
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
	usage, usageCursor []dbbilling.ListUsageRecordsRow
	claims             []dbbilling.ListBillingClaimsRow
	audit              []dbbilling.ListAuditEventsRow
	usageCountArg      dbbilling.CountUsageRecordsParams
	usageArg           dbbilling.ListUsageRecordsParams
	claimsArg          dbbilling.ListBillingClaimsParams
	auditArg           dbbilling.ListAuditEventsParams
	usageCountCalls    int
	usageListCalls     int
}

func (s *obsStoreStub) CountUsageRecords(_ context.Context, arg dbbilling.CountUsageRecordsParams) (int64, error) {
	s.usageCountArg = arg
	s.usageCountCalls++
	return int64(len(s.usage)), nil
}
func (s *obsStoreStub) ListUsageRecords(_ context.Context, arg dbbilling.ListUsageRecordsParams) ([]dbbilling.ListUsageRecordsRow, error) {
	s.usageArg = arg
	s.usageListCalls++
	if arg.HasCursor && s.usageCursor != nil {
		return s.usageCursor, nil
	}
	return s.usage, nil
}
func (s *obsStoreStub) CountBillingClaims(context.Context, dbbilling.CountBillingClaimsParams) (int64, error) {
	return int64(len(s.claims)), nil
}
func (s *obsStoreStub) ListBillingClaims(_ context.Context, arg dbbilling.ListBillingClaimsParams) ([]dbbilling.ListBillingClaimsRow, error) {
	s.claimsArg = arg
	return s.claims, nil
}
func (s *obsStoreStub) CountAuditEvents(context.Context, dbbilling.CountAuditEventsParams) (int64, error) {
	return int64(len(s.audit)), nil
}
func (s *obsStoreStub) ListAuditEvents(_ context.Context, arg dbbilling.ListAuditEventsParams) ([]dbbilling.ListAuditEventsRow, error) {
	s.auditArg = arg
	return s.audit, nil
}

func TestAdminObsUsageSuccessWithFilters(t *testing.T) {
	store := &obsStoreStub{usage: []dbbilling.ListUsageRecordsRow{usageRow(1)}}
	rec := invokeObs(NewUsageHandler(obsDepsStub{auth: obsAuthStub{}, s: store}), "/admin/v1/usage?from=2026-05-01T00:00:00Z&provider=anthropic&pool_id=9&limit=50")
	assertStatus(t, rec, http.StatusOK)
	if store.usageArg.PageLimit != 51 || store.usageArg.Provider == nil || *store.usageArg.Provider != "anthropic" || store.usageArg.PoolID == nil || *store.usageArg.PoolID != 9 {
		t.Fatalf("usage params mismatch: %+v", store.usageArg)
	}
}

func TestAdminObsUsageTrustFieldsFromAuditLedger(t *testing.T) {
	hopJSON, err := json.Marshal([]proto.HopAttestation{
		{Hop: proto.HopProvider, Provider: "anthropic"},
	})
	if err != nil {
		t.Fatalf("marshal hop chain: %v", err)
	}
	modelJSON, err := json.Marshal(&proto.ModelChain{
		Requested:    "claude-opus-4",
		RouteDecided: "claude-opus-4-20260514",
	})
	if err != nil {
		t.Fatalf("marshal model chain: %v", err)
	}
	ledgerID := "ledger-trust-admin"
	fp := "fp-trust-admin"
	row := usageRow(1)
	row.Provider = strPtr("openai")
	row.RequestedModel = "claude-opus-4"
	row.UpstreamModel = strPtr("wrong-usage-row-model")
	row.RequestID = "req-trust-admin"
	row.AuditLedgerID = &ledgerID
	row.AuditPubkeyFingerprint = &fp
	row.AuditHopChain = hopJSON
	row.AuditModelChain = modelJSON
	store := &obsStoreStub{usage: []dbbilling.ListUsageRecordsRow{row}}

	rec := invokeObs(NewUsageHandler(obsDepsStub{auth: obsAuthStub{}, s: store}), "/admin/v1/usage?limit=20")
	assertStatus(t, rec, http.StatusOK)
	var body struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || len(body.Items) != 1 {
		t.Fatalf("bad usage body=%s err=%v", rec.Body.String(), err)
	}
	item := body.Items[0]
	if item["provider"] != "anthropic" || item["upstream_model"] != "claude-opus-4-20260514" || item["request_id"] != "req-trust-admin" {
		t.Fatalf("trust metadata item=%v", item)
	}
	if item["trust_status"] != string(trust.StatusUnverified) {
		t.Fatalf("trust_status=%v want %q", item["trust_status"], trust.StatusUnverified)
	}
}

func TestAdminObsClaimsSuccess(t *testing.T) {
	store := &obsStoreStub{claims: []dbbilling.ListBillingClaimsRow{claimRow(3)}}
	rec := invokeObs(NewClaimsHandler(obsDepsStub{auth: obsAuthStub{}, s: store}), "/admin/v1/billing/claims?status=committed")
	assertStatus(t, rec, http.StatusOK)
	if store.claimsArg.Status == nil || *store.claimsArg.Status != "committed" {
		t.Fatalf("claims status filter missing: %+v", store.claimsArg)
	}
}

func TestAdminObsAuditSuccess(t *testing.T) {
	store := &obsStoreStub{audit: []dbbilling.ListAuditEventsRow{auditRow(5, "error", "ledger-5")}}
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

func TestUsageOutcomeInvalid(t *testing.T) {
	store := &obsStoreStub{}
	rec := invokeObs(NewUsageHandler(obsDepsStub{auth: obsAuthStub{}, s: store}), "/admin/v1/usage?outcome=bogus")
	assertStatus(t, rec, http.StatusBadRequest)
	// 变异:接受任意 outcome 会触达 store 并返回一个非 invalid_outcome 的响应。
	if !strings.Contains(rec.Body.String(), "invalid_outcome") || store.usageCountCalls != 0 || store.usageListCalls != 0 {
		t.Fatalf("invalid outcome response/body/store calls mismatch: status=%d body=%s countCalls=%d listCalls=%d", rec.Code, rec.Body.String(), store.usageCountCalls, store.usageListCalls)
	}
}

func TestUsageOutcomeDefaultAll(t *testing.T) {
	store := &obsStoreStub{usage: []dbbilling.ListUsageRecordsRow{
		usageRowWithEndClass(1, "non_streaming"),
		usageRowWithEndClass(2, "upstream_error_5xx"),
	}}
	rec := invokeObs(NewUsageHandler(obsDepsStub{auth: obsAuthStub{}, s: store}), "/admin/v1/usage")
	assertStatus(t, rec, http.StatusOK)
	// 变异:把缺省的 outcome 默认成 error 或 success,会改变贯穿传递的 query 值。
	if store.usageArg.Outcome == nil || *store.usageArg.Outcome != "all" || store.usageCountArg.Outcome == nil || *store.usageCountArg.Outcome != "all" {
		t.Fatalf("default outcome params mismatch: count=%+v list=%+v", store.usageCountArg, store.usageArg)
	}
	var body struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || len(body.Items) != 2 {
		t.Fatalf("default outcome should keep both success and error rows: body=%s err=%v", rec.Body.String(), err)
	}
}

func TestAdminObsLargeLimit(t *testing.T) {
	deps := obsDepsStub{auth: obsAuthStub{}, s: &obsStoreStub{}}
	assertStatus(t, invokeObs(NewClaimsHandler(deps), "/admin/v1/billing/claims?limit=201"), http.StatusBadRequest)
}

func TestAdminObsPaginationRoundTrip(t *testing.T) {
	store := &obsStoreStub{usage: []dbbilling.ListUsageRecordsRow{usageRow(3), usageRow(2), usageRow(1)}, usageCursor: []dbbilling.ListUsageRecordsRow{usageRow(1)}}
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
	store := &obsStoreStub{audit: []dbbilling.ListAuditEventsRow{auditRow(9, "error", "ledger-9")}}
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

func usageRow(id int64) dbbilling.ListUsageRecordsRow {
	providerAccountID := int64(50)
	return dbbilling.ListUsageRecordsRow{ID: id, TenantID: 7, ClaimID: 20 + id, APIKeyID: 30, UserID: 40, ProviderAccountID: &providerAccountID, AttemptSeq: 1, ActualCost: decimal.RequireFromString("0.01000000"), EndClass: "non_streaming", UsageSource: "reported", CreatedAt: ts(id)}
}
func usageRowWithEndClass(id int64, endClass string) dbbilling.ListUsageRecordsRow {
	row := usageRow(id)
	row.EndClass = endClass
	return row
}
func claimRow(id int64) dbbilling.ListBillingClaimsRow {
	return dbbilling.ListBillingClaimsRow{ID: id, TenantID: 7, IdempotencyKey: "idem", APIKeyID: 1, UserID: 2, LogicalRequestID: "lr", EndpointFamily: "chat", RequestedModel: "m", AttemptSeq: 1, PredictedCost: decimal.RequireFromString("0.01000000"), CurrencyCode: "USD", Status: "committed", CreatedAt: ts(id)}
}
func auditRow(id int64, severity, ledgerID string) dbbilling.ListAuditEventsRow {
	return dbbilling.ListAuditEventsRow{ID: id, TenantID: 7, EventClass: "rate_limit", EventType: "permanent_disable_set", Severity: severity, LedgerID: ledgerID, Payload: []byte(`{"ok":true}`), CreatedAt: ts(id)}
}
func ts(id int64) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: time.Date(2026, 5, 14, 0, 0, int(id), 0, time.UTC), Valid: true}
}
