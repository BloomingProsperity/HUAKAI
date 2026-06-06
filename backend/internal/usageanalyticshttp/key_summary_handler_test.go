package usageanalyticshttp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"

	sessionauth "github.com/BloomingProsperity/HUAKAI/internal/auth"
	dbbilling "github.com/BloomingProsperity/HUAKAI/internal/db/billing"
	"github.com/BloomingProsperity/HUAKAI/internal/userkey"
)

type keySummaryOwnerStub struct {
	calls []struct{ tenantID, userID, apiKeyID int64 }
	err   error
}

func (s *keySummaryOwnerStub) Get(_ context.Context, tenantID, userID, apiKeyID int64) (userkey.KeyDescriptor, error) {
	s.calls = append(s.calls, struct{ tenantID, userID, apiKeyID int64 }{tenantID, userID, apiKeyID})
	if s.err != nil {
		return userkey.KeyDescriptor{}, s.err
	}
	return userkey.KeyDescriptor{APIKeyID: apiKeyID}, nil
}

type keySummaryStoreStub struct {
	row   dbbilling.AggregateMyUsageTotalsRow
	arg   dbbilling.AggregateMyUsageTotalsParams
	calls int
	err   error
}

func (s *keySummaryStoreStub) AggregateMyUsageTotals(_ context.Context, arg dbbilling.AggregateMyUsageTotalsParams) (dbbilling.AggregateMyUsageTotalsRow, error) {
	s.calls++
	s.arg = arg
	if s.err != nil {
		return dbbilling.AggregateMyUsageTotalsRow{}, s.err
	}
	return s.row, nil
}

func mountKeySummaryWithSession(t *testing.T, d KeyUsageSummaryDeps, ident sessionauth.SessionIdentity, passIdent bool) *chi.Mux {
	t.Helper()
	r := chi.NewMux()
	r.Route("/v1/me", func(r chi.Router) {
		if passIdent {
			r.Use(func(next http.Handler) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
					ctx := sessionauth.ContextWithSession(req.Context(), ident)
					next.ServeHTTP(w, req.WithContext(ctx))
				})
			})
		}
		r.Get("/keys/{id}/usage-summary", NewKeyUsageSummaryHandler(d))
	})
	return r
}

// TestKeyUsageSummary_OwnershipEnforced is the key security regression test.
// Mutation: skip KeyOwner.Get and aggregate by path id only -> user B can read
// user A's key totals -> this test goes red because it expects 404 and no store call.
func TestKeyUsageSummary_OwnershipEnforced(t *testing.T) {
	owner := &keySummaryOwnerStub{err: userkey.ErrNotFound}
	store := &keySummaryStoreStub{row: dbbilling.AggregateMyUsageTotalsRow{TotalCost: "9.99000000", RequestCount: 9}}
	mux := mountKeySummaryWithSession(t, KeyUsageSummaryDeps{Keys: owner, Store: store}, sessionauth.SessionIdentity{TenantID: 7, UserID: 2002}, true)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/me/keys/31/usage-summary", nil))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status=%d want 404 for non-owned key body=%s", rec.Code, rec.Body.String())
	}
	if len(owner.calls) != 1 || owner.calls[0].tenantID != 7 || owner.calls[0].userID != 2002 || owner.calls[0].apiKeyID != 31 {
		t.Fatalf("ownership call=%v want tenant=7 user=2002 key=31", owner.calls)
	}
	if store.calls != 0 {
		t.Fatalf("store calls=%d want 0 when ownership fails", store.calls)
	}
}

// TestKeyUsageSummary_Totals proves the selected owned key, not all caller keys,
// drives the aggregate. Mutation: drop the api_key_id filter in SQL or pass
// another key id -> totals/arg assertions go red.
func TestKeyUsageSummary_Totals(t *testing.T) {
	owner := &keySummaryOwnerStub{}
	store := &keySummaryStoreStub{row: dbbilling.AggregateMyUsageTotalsRow{
		TotalCost:                "0.15000000",
		TotalTokensInput:         120,
		TotalTokensOutput:        35,
		TotalCacheReadTokens:     8,
		TotalCacheCreationTokens: 4,
		RequestCount:             2,
	}}
	mux := mountKeySummaryWithSession(t, KeyUsageSummaryDeps{Keys: owner, Store: store}, sessionauth.SessionIdentity{TenantID: 7, UserID: 42}, true)
	target := "/v1/me/keys/31/usage-summary?from=2026-06-01T00:00:00Z&to=2026-06-02T00:00:00Z&tenant_id=999&api_key_id=888"

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, target, nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d want 200 body=%s", rec.Code, rec.Body.String())
	}
	if store.calls != 1 {
		t.Fatalf("store calls=%d want 1", store.calls)
	}
	if store.arg.TenantID != 7 || store.arg.APIKeyID != 31 {
		t.Fatalf("store scope=(tenant:%d key:%d) want (7,31); query tenant_id/api_key_id must be ignored", store.arg.TenantID, store.arg.APIKeyID)
	}
	if !store.arg.FromTs.Valid || !store.arg.ToTs.Valid {
		t.Fatalf("from/to should be valid when supplied: from=%+v to=%+v", store.arg.FromTs, store.arg.ToTs)
	}
	assertTS(t, store.arg.FromTs, "2026-06-01T00:00:00Z")
	assertTS(t, store.arg.ToTs, "2026-06-02T00:00:00Z")

	var body struct {
		APIKeyID                 int64   `json:"api_key_id"`
		TotalCost                string  `json:"total_cost"`
		TotalTokensInput         int64   `json:"total_tokens_input"`
		TotalTokensOutput        int64   `json:"total_tokens_output"`
		TotalCacheReadTokens     int64   `json:"total_cache_read_tokens"`
		TotalCacheCreationTokens int64   `json:"total_cache_creation_tokens"`
		RequestCount             int64   `json:"request_count"`
		From                     *string `json:"from"`
		To                       *string `json:"to"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v body=%s", err, rec.Body.String())
	}
	if body.APIKeyID != 31 || body.TotalCost != "0.15000000" || body.TotalTokensInput != 120 || body.TotalTokensOutput != 35 || body.RequestCount != 2 {
		t.Fatalf("summary body drift: %+v", body)
	}
	if body.TotalCacheReadTokens != 8 || body.TotalCacheCreationTokens != 4 {
		t.Fatalf("cache token totals drift: %+v", body)
	}
	if body.From == nil || *body.From != "2026-06-01T00:00:00Z" || body.To == nil || *body.To != "2026-06-02T00:00:00Z" {
		t.Fatalf("response window from/to drift: from=%v to=%v", body.From, body.To)
	}
}

// TestKeyUsageSummary_TenantScoped proves cross-tenant key ids collapse to 404
// before aggregation. Mutation: drop tenant scope from the ownership lookup ->
// a same-id key from another tenant is treated as owned and the store is called.
func TestKeyUsageSummary_TenantScoped(t *testing.T) {
	owner := &keySummaryOwnerStub{err: userkey.ErrNotFound}
	store := &keySummaryStoreStub{}
	mux := mountKeySummaryWithSession(t, KeyUsageSummaryDeps{Keys: owner, Store: store}, sessionauth.SessionIdentity{TenantID: 70, UserID: 42}, true)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/me/keys/31/usage-summary", nil))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status=%d want 404 for cross-tenant/non-owned key body=%s", rec.Code, rec.Body.String())
	}
	if len(owner.calls) != 1 || owner.calls[0].tenantID != 70 || owner.calls[0].userID != 42 || owner.calls[0].apiKeyID != 31 {
		t.Fatalf("ownership call=%v want tenant=70 user=42 key=31", owner.calls)
	}
	if store.calls != 0 {
		t.Fatalf("store calls=%d want 0 when cross-tenant ownership fails", store.calls)
	}
}

// TestKeyUsageSummary_AuthRequired covers the session-only mount contract.
// Mutation: remove SessionFromContext validation -> the handler queries with
// zero tenant/user and this test goes red by observing a non-401 response.
func TestKeyUsageSummary_AuthRequired(t *testing.T) {
	owner := &keySummaryOwnerStub{}
	store := &keySummaryStoreStub{}
	mux := mountKeySummaryWithSession(t, KeyUsageSummaryDeps{Keys: owner, Store: store}, sessionauth.SessionIdentity{}, false)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/me/keys/31/usage-summary", nil))

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d want 401 without session body=%s", rec.Code, rec.Body.String())
	}
	if len(owner.calls) != 0 || store.calls != 0 {
		t.Fatalf("no-session request must not touch owner/store; owner=%v store=%d", owner.calls, store.calls)
	}
}

// TestKeyUsageSummary_OmittedWindowQueriesFullHistory locks the optional
// from/to contract. Mutation: reuse the time-series parseWindow requiring both
// bounds -> this request returns 400 and the test goes red.
func TestKeyUsageSummary_OmittedWindowQueriesFullHistory(t *testing.T) {
	owner := &keySummaryOwnerStub{}
	store := &keySummaryStoreStub{row: dbbilling.AggregateMyUsageTotalsRow{TotalCost: "0.00000000"}}
	mux := mountKeySummaryWithSession(t, KeyUsageSummaryDeps{Keys: owner, Store: store}, sessionauth.SessionIdentity{TenantID: 7, UserID: 42}, true)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/me/keys/31/usage-summary", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d want 200 for omitted window body=%s", rec.Code, rec.Body.String())
	}
	if store.arg.FromTs.Valid || store.arg.ToTs.Valid {
		t.Fatalf("omitted from/to should pass invalid timestamptz params; got from=%+v to=%+v", store.arg.FromTs, store.arg.ToTs)
	}
	var body struct {
		From *string `json:"from"`
		To   *string `json:"to"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.From != nil || body.To != nil {
		t.Fatalf("omitted window should return null from/to; got from=%v to=%v", body.From, body.To)
	}
}

func TestKeyUsageSummary_InvalidWindowReturns400WithoutQuery(t *testing.T) {
	owner := &keySummaryOwnerStub{}
	store := &keySummaryStoreStub{}
	mux := mountKeySummaryWithSession(t, KeyUsageSummaryDeps{Keys: owner, Store: store}, sessionauth.SessionIdentity{TenantID: 7, UserID: 42}, true)

	rec := httptest.NewRecorder()
	target := "/v1/me/keys/31/usage-summary?from=2026-06-02T00:00:00Z&to=2026-06-01T00:00:00Z"
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, target, nil))

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d want 400 for invalid window body=%s", rec.Code, rec.Body.String())
	}
	if len(owner.calls) != 0 || store.calls != 0 {
		t.Fatalf("invalid window must not touch owner/store; owner=%v store=%d", owner.calls, store.calls)
	}
}

func TestKeyUsageSummary_BackendErrorsMapTo503(t *testing.T) {
	cases := []struct {
		name     string
		ownerErr error
		storeErr error
	}{
		{name: "owner backend", ownerErr: userkey.ErrBackend},
		{name: "store backend", storeErr: errors.New("db down")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			owner := &keySummaryOwnerStub{err: tc.ownerErr}
			store := &keySummaryStoreStub{err: tc.storeErr}
			mux := mountKeySummaryWithSession(t, KeyUsageSummaryDeps{Keys: owner, Store: store}, sessionauth.SessionIdentity{TenantID: 7, UserID: 42}, true)

			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/me/keys/31/usage-summary", nil))

			if rec.Code != http.StatusServiceUnavailable {
				t.Fatalf("status=%d want 503 body=%s", rec.Code, rec.Body.String())
			}
		})
	}
}

func assertTS(t *testing.T, got pgtype.Timestamptz, want string) {
	t.Helper()
	ts, err := time.Parse(time.RFC3339, want)
	if err != nil {
		t.Fatalf("bad test timestamp %q: %v", want, err)
	}
	if !got.Valid || !got.Time.Equal(ts) {
		t.Fatalf("timestamp=%+v want %s", got, want)
	}
}

func TestKeyUsageSummary_InvalidIDReturns400(t *testing.T) {
	owner := &keySummaryOwnerStub{}
	store := &keySummaryStoreStub{}
	mux := mountKeySummaryWithSession(t, KeyUsageSummaryDeps{Keys: owner, Store: store}, sessionauth.SessionIdentity{TenantID: 7, UserID: 42}, true)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/me/keys/not-int/usage-summary", strings.NewReader("")))

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d want 400 body=%s", rec.Code, rec.Body.String())
	}
	if len(owner.calls) != 0 || store.calls != 0 {
		t.Fatalf("bad id must not touch owner/store; owner=%v store=%d", owner.calls, store.calls)
	}
}
