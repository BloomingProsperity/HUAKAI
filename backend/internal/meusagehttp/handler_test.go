package meusagehttp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/shopspring/decimal"

	"github.com/BloomingProsperity/HUAKAI/internal/auth"
	dbbilling "github.com/BloomingProsperity/HUAKAI/internal/db/billing"
)

type authStub struct {
	identity auth.Identity
	err      error
}

func (s authStub) Resolve(context.Context, *http.Request) (auth.Identity, error) {
	if s.err != nil {
		return auth.Identity{}, s.err
	}
	return s.identity, nil
}

type usageStoreStub struct {
	rows    []dbbilling.ListUsageRecordsRow
	listArg dbbilling.ListUsageRecordsParams
}

func (s *usageStoreStub) ListUsageRecords(_ context.Context, arg dbbilling.ListUsageRecordsParams) ([]dbbilling.ListUsageRecordsRow, error) {
	s.listArg = arg
	rows := s.filter(arg.TenantID, arg.APIKeyID, arg.FromTs, arg.ToTs)
	if arg.HasCursor {
		out := rows[:0]
		for _, row := range rows {
			if row.CreatedAt.Valid && (row.CreatedAt.Time.Before(arg.CursorCreatedAt.Time) || row.CreatedAt.Time.Equal(arg.CursorCreatedAt.Time) && row.ID < arg.CursorID) {
				out = append(out, row)
			}
		}
		rows = out
	}
	if int32(len(rows)) > arg.PageLimit {
		rows = rows[:arg.PageLimit]
	}
	return rows, nil
}

func (s *usageStoreStub) filter(tenantID, apiKeyID *int64, fromTs, toTs pgtype.Timestamptz) []dbbilling.ListUsageRecordsRow {
	out := make([]dbbilling.ListUsageRecordsRow, 0, len(s.rows))
	for _, row := range s.rows {
		if tenantID != nil && row.TenantID != *tenantID {
			continue
		}
		if apiKeyID != nil && row.APIKeyID != *apiKeyID {
			continue
		}
		if fromTs.Valid && row.CreatedAt.Time.Before(fromTs.Time) {
			continue
		}
		if toTs.Valid && row.CreatedAt.Time.After(toTs.Time) {
			continue
		}
		out = append(out, row)
	}
	return out
}

func TestMeUsageScopesToAuthenticatedAPIKeyAndKeepsTrustFields(t *testing.T) {
	userA := auth.Identity{TenantID: 7, APIKeyID: 30, UserID: 40}
	otherProviderAccount := int64(99)
	store := &usageStoreStub{rows: []dbbilling.ListUsageRecordsRow{
		meUsageRow(2, userA.TenantID, userA.APIKeyID, userA.UserID, "claude-opus-4", "claude-opus-4-20260514", "ledger-a", "anthropic"),
		meUsageRow(1, 8, 31, 41, "gpt-4o", "gpt-4o-mini", "ledger-b", "openai"),
	}}
	store.rows[1].ProviderAccountID = &otherProviderAccount
	h := NewHandler(Deps{Auth: authStub{identity: userA}, Store: store})

	rec := invokeMeUsage(h, "/v1/me/usage?tenant_id=8&api_key_id=31&limit=20&from=2026-05-14T00:00:00Z&to=2026-05-14T00:00:03Z")

	assertMeStatus(t, rec, http.StatusOK)
	var body struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v body=%s", err, rec.Body.String())
	}
	if len(body.Items) != 1 {
		t.Fatalf("auth scope must return exactly user A rows; body=%s", rec.Body.String())
	}
	item := body.Items[0]
	if item["ledger_id"] != "ledger-a" {
		t.Fatalf("leaked or wrong record ledger_id=%v body=%s", item["ledger_id"], rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "ledger-b") || strings.Contains(rec.Body.String(), "gpt-4o-mini") {
		t.Fatalf("response leaked another tenant/key record: %s", rec.Body.String())
	}
	if got, want := store.listArg.TenantID, userA.TenantID; got == nil || *got != want {
		t.Fatalf("list tenant scope = %v want %d", got, want)
	}
	if got, want := store.listArg.APIKeyID, userA.APIKeyID; got == nil || *got != want {
		t.Fatalf("list api key scope = %v want %d", got, want)
	}
	if !store.listArg.FromTs.Valid || !store.listArg.ToTs.Valid {
		t.Fatalf("time range was not passed to list: %+v", store.listArg)
	}
	for _, key := range []string{"tenant_id", "api_key_id", "user_id", "body", "prompt", "messages"} {
		if _, ok := item[key]; ok {
			t.Fatalf("end-user usage response must not expose %q: %v", key, item)
		}
	}
	assertStringField(t, item, "requested_model", "claude-opus-4")
	assertStringField(t, item, "upstream_model", "claude-opus-4-20260514")
	assertStringField(t, item, "actual_cost", "0.01000000")
	assertStringField(t, item, "provider", "anthropic")
	assertStringField(t, item, "ledger_id", "ledger-a")
	assertStringField(t, item, "status", "non_streaming")
	verifyHint, ok := item["verify_hint"].(map[string]any)
	if !ok {
		t.Fatalf("verify_hint missing or wrong type: %v", item["verify_hint"])
	}
	assertStringField(t, verifyHint, "trust_verify_path", "/v1/trust/verify")
	assertStringField(t, verifyHint, "audit_verify_path", "/v1/audit/verify")
	assertStringField(t, verifyHint, "ledger_id", "ledger-a")
	if got, ok := item["provider_account_id"].(float64); !ok || got != 50 {
		t.Fatalf("provider_account_id=%v want 50", item["provider_account_id"])
	}
}

func TestMeUsagePaginatesWithEndpointSpecificCursor(t *testing.T) {
	userA := auth.Identity{TenantID: 7, APIKeyID: 30, UserID: 40}
	store := &usageStoreStub{rows: []dbbilling.ListUsageRecordsRow{
		meUsageRow(3, userA.TenantID, userA.APIKeyID, userA.UserID, "m3", "u3", "ledger-3", "anthropic"),
		meUsageRow(2, userA.TenantID, userA.APIKeyID, userA.UserID, "m2", "u2", "ledger-2", "anthropic"),
		meUsageRow(1, userA.TenantID, userA.APIKeyID, userA.UserID, "m1", "u1", "ledger-1", "anthropic"),
	}}
	h := NewHandler(Deps{Auth: authStub{identity: userA}, Store: store})

	first := invokeMeUsage(h, "/v1/me/usage?limit=2")
	assertMeStatus(t, first, http.StatusOK)
	var body struct {
		Items      []map[string]any `json:"items"`
		NextCursor string           `json:"next_cursor"`
	}
	if err := json.Unmarshal(first.Body.Bytes(), &body); err != nil || len(body.Items) != 2 || body.NextCursor == "" {
		t.Fatalf("bad first page body=%s err=%v", first.Body.String(), err)
	}

	second := invokeMeUsage(h, "/v1/me/usage?limit=2&cursor="+body.NextCursor)
	assertMeStatus(t, second, http.StatusOK)
	if !store.listArg.HasCursor || store.listArg.CursorID != 2 {
		t.Fatalf("cursor not passed to store: %+v", store.listArg)
	}
}

func TestMeUsageAuthErrorsMatchInboundAPIKeyPath(t *testing.T) {
	store := &usageStoreStub{}
	cases := []struct {
		name string
		err  error
		want int
	}{
		{name: "misconfigured", err: auth.ErrAuthMisconfigured, want: http.StatusServiceUnavailable},
		{name: "backend", err: auth.ErrAuthBackend, want: http.StatusServiceUnavailable},
		{name: "unauthorized", err: auth.ErrUnauthorized, want: http.StatusUnauthorized},
		{name: "wrapped backend", err: errors.Join(auth.ErrAuthBackend, errors.New("pg")), want: http.StatusServiceUnavailable},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := NewHandler(Deps{Auth: authStub{err: tc.err}, Store: store})
			assertMeStatus(t, invokeMeUsage(h, "/v1/me/usage"), tc.want)
		})
	}
}

// TestMeUsageExposesPerRequestTokenCounts is the discriminating test for the
// "relay request log" residual. usage_records already stores token counts and
// ListUsageRecords already SELECTs them, but the DTO dropped them. Mutation:
// remove the Tokens mapping (or the DTO field) -> item["tokens"] is absent ->
// this test goes red.
func TestMeUsageExposesPerRequestTokenCounts(t *testing.T) {
	userA := auth.Identity{TenantID: 7, APIKeyID: 30, UserID: 40}
	row := meUsageRow(1, userA.TenantID, userA.APIKeyID, userA.UserID, "claude-opus-4", "claude-opus-4-20260514", "ledger-a", "anthropic")
	row.TokensInput = 1234
	row.TokensOutput = 567
	row.CacheCreationTokens = 89
	row.CacheReadTokens = 12
	store := &usageStoreStub{rows: []dbbilling.ListUsageRecordsRow{row}}
	h := NewHandler(Deps{Auth: authStub{identity: userA}, Store: store})

	rec := invokeMeUsage(h, "/v1/me/usage")
	assertMeStatus(t, rec, http.StatusOK)
	var body struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || len(body.Items) != 1 {
		t.Fatalf("decode/len body=%s err=%v", rec.Body.String(), err)
	}
	tokens, ok := body.Items[0]["tokens"].(map[string]any)
	if !ok {
		t.Fatalf("per-request token counts missing from usage log; body=%s", rec.Body.String())
	}
	if tokens["input"] != float64(1234) || tokens["output"] != float64(567) {
		t.Fatalf("tokens input/output=%v/%v want 1234/567; body=%s", tokens["input"], tokens["output"], rec.Body.String())
	}
	if tokens["cache_creation"] != float64(89) || tokens["cache_read"] != float64(12) {
		t.Fatalf("cache tokens=%v/%v want 89/12; body=%s", tokens["cache_creation"], tokens["cache_read"], rec.Body.String())
	}
}

func invokeMeUsage(h http.HandlerFunc, target string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, target, strings.NewReader(""))
	rec := httptest.NewRecorder()
	h(rec, req)
	return rec
}

func assertMeStatus(t *testing.T, rec *httptest.ResponseRecorder, want int) {
	t.Helper()
	if rec.Code != want {
		t.Fatalf("status=%d want=%d body=%s", rec.Code, want, rec.Body.String())
	}
}

func assertStringField(t *testing.T, item map[string]any, key, want string) {
	t.Helper()
	got, ok := item[key].(string)
	if !ok || got != want {
		t.Fatalf("%s=%v want %q in %v", key, item[key], want, item)
	}
}

func meUsageRow(id, tenantID, apiKeyID, userID int64, requestedModel, upstreamModel, ledgerID, provider string) dbbilling.ListUsageRecordsRow {
	providerAccountID := int64(50)
	created := time.Date(2026, 5, 14, 0, 0, int(id), 0, time.UTC)
	requested := created.Add(-time.Second)
	return dbbilling.ListUsageRecordsRow{
		ID:                    id,
		TenantID:              tenantID,
		ClaimID:               200 + id,
		APIKeyID:              apiKeyID,
		UserID:                userID,
		ProviderAccountID:     &providerAccountID,
		AttemptSeq:            1,
		ActualCost:            decimal.RequireFromString("0.01000000"),
		EndClass:              "non_streaming",
		UsageSource:           "reported",
		CreatedAt:             pgtype.Timestamptz{Time: created, Valid: true},
		RequestedAt:           pgtype.Timestamptz{Time: requested, Valid: true},
		RequestedModel:        requestedModel,
		UpstreamModel:         &upstreamModel,
		Provider:              &provider,
		RequestID:             "req-" + ledgerID,
		AuditLedgerID:         &ledgerID,
		PendingReconciliation: false,
	}
}
