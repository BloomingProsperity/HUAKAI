package usageanalyticshttp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/BloomingProsperity/HUAKAI/internal/auth"
	dbbilling "github.com/BloomingProsperity/HUAKAI/internal/db/billing"
)

type authStub struct {
	identity auth.Identity
	err      error
}

func (s authStub) Resolve(context.Context, *http.Request) (auth.Identity, error) {
	return s.identity, s.err
}

type storeStub struct {
	rows []dbbilling.AggregateMyUsageByDayRow
	arg  dbbilling.AggregateMyUsageByDayParams
	err  error
}

func (s *storeStub) AggregateMyUsageByDay(_ context.Context, arg dbbilling.AggregateMyUsageByDayParams) ([]dbbilling.AggregateMyUsageByDayRow, error) {
	s.arg = arg
	return s.rows, s.err
}

func invoke(h http.HandlerFunc, target string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, target, strings.NewReader(""))
	rec := httptest.NewRecorder()
	h(rec, req)
	return rec
}

const win = "?from=2026-05-01T00:00:00Z&to=2026-05-08T00:00:00Z"

// TestTimeSeries_LockedToOwnAPIKey is the self-serve isolation invariant: even
// when the caller injects ?api_key_id=999 and ?tenant_id=88, the SQL scope MUST
// come from the resolved identity. Mutation: read api_key_id/tenant_id from the
// query string -> arg.APIKeyID becomes 999 -> this test goes red.
func TestTimeSeries_LockedToOwnAPIKey(t *testing.T) {
	ident := auth.Identity{TenantID: 7, APIKeyID: 30, UserID: 40}
	store := &storeStub{}
	h := NewTimeSeriesHandler(Deps{Auth: authStub{identity: ident}, Store: store})
	rec := invoke(h, "/v1/me/analytics/time-series"+win+"&api_key_id=999&tenant_id=88")
	if rec.Code != http.StatusOK {
		t.Fatalf("code=%d want 200 body=%s", rec.Code, rec.Body.String())
	}
	if store.arg.APIKeyID != 30 {
		t.Fatalf("scoped api_key_id=%d; want 30 (caller-supplied 999 must be ignored)", store.arg.APIKeyID)
	}
	if store.arg.TenantID != 7 {
		t.Fatalf("scoped tenant_id=%d; want 7 (caller-supplied 88 must be ignored)", store.arg.TenantID)
	}
}

func TestTimeSeries_MissingFromReturns400(t *testing.T) {
	h := NewTimeSeriesHandler(Deps{Auth: authStub{identity: auth.Identity{TenantID: 7, APIKeyID: 30}}, Store: &storeStub{}})
	if rec := invoke(h, "/v1/me/analytics/time-series?to=2026-05-08T00:00:00Z"); rec.Code != http.StatusBadRequest {
		t.Fatalf("code=%d want 400", rec.Code)
	}
}

func TestTimeSeries_WindowTooLargeReturns400(t *testing.T) {
	h := NewTimeSeriesHandler(Deps{Auth: authStub{identity: auth.Identity{TenantID: 7, APIKeyID: 30}}, Store: &storeStub{}})
	// ~60-day window exceeds the 31-day cap.
	rec := invoke(h, "/v1/me/analytics/time-series?from=2026-03-01T00:00:00Z&to=2026-04-30T00:00:00Z")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("code=%d want 400 (window too large)", rec.Code)
	}
}

func TestTimeSeries_NilDepsReturns503(t *testing.T) {
	if rec := invoke(NewTimeSeriesHandler(Deps{}), "/v1/me/analytics/time-series"+win); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("code=%d want 503", rec.Code)
	}
}

func TestTimeSeries_AuthErrorsMapToStatus(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want int
	}{
		{"misconfigured", auth.ErrAuthMisconfigured, http.StatusServiceUnavailable},
		{"backend", auth.ErrAuthBackend, http.StatusServiceUnavailable},
		{"unauthorized", auth.ErrUnauthorized, http.StatusUnauthorized},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := NewTimeSeriesHandler(Deps{Auth: authStub{err: tc.err}, Store: &storeStub{}})
			if rec := invoke(h, "/v1/me/analytics/time-series"+win); rec.Code != tc.want {
				t.Fatalf("code=%d want %d", rec.Code, tc.want)
			}
		})
	}
}

func TestTimeSeries_HappyPathMapsCostAndTokens(t *testing.T) {
	day := time.Date(2026, 5, 5, 0, 0, 0, 0, time.UTC)
	store := &storeStub{rows: []dbbilling.AggregateMyUsageByDayRow{{
		Day:                      pgtype.Timestamptz{Time: day, Valid: true},
		RequestedModel:           "claude-opus-4",
		TotalCost:                "0.12340000",
		TotalTokensInput:         1000,
		TotalTokensOutput:        500,
		TotalCacheReadTokens:     20,
		TotalCacheCreationTokens: 10,
		RequestCount:             3,
	}}}
	h := NewTimeSeriesHandler(Deps{Auth: authStub{identity: auth.Identity{TenantID: 7, APIKeyID: 30}}, Store: store})
	rec := invoke(h, "/v1/me/analytics/time-series"+win)
	if rec.Code != http.StatusOK {
		t.Fatalf("code=%d want 200 body=%s", rec.Code, rec.Body.String())
	}
	var body struct {
		Items  []map[string]any `json:"items"`
		Period map[string]any   `json:"period"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || len(body.Items) != 1 {
		t.Fatalf("decode/len body=%s err=%v", rec.Body.String(), err)
	}
	item := body.Items[0]
	if item["day"] != "2026-05-05" {
		t.Fatalf("day=%v want 2026-05-05", item["day"])
	}
	if item["total_cost"] != "0.12340000" {
		t.Fatalf("total_cost=%v want 0.12340000", item["total_cost"])
	}
	tokens, ok := item["tokens"].(map[string]any)
	if !ok || tokens["input"] != float64(1000) || tokens["output"] != float64(500) {
		t.Fatalf("tokens=%v want input1000/output500", item["tokens"])
	}
	if tokens["cache_read"] != float64(20) || tokens["cache_creation"] != float64(10) {
		t.Fatalf("cache tokens=%v want 20/10", item["tokens"])
	}
	if item["request_count"] != float64(3) {
		t.Fatalf("request_count=%v want 3", item["request_count"])
	}
	if body.Period["from"] != "2026-05-01T00:00:00Z" {
		t.Fatalf("period.from=%v want 2026-05-01T00:00:00Z", body.Period["from"])
	}
}
