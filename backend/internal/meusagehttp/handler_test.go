package meusagehttp

import (
	"context"
	"encoding/json"
	"errors"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
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

type generationStoreStub struct {
	rows  []dbbilling.GetUsageRecordByRequestIDRow
	err   error
	arg   dbbilling.GetUsageRecordByRequestIDParams
	calls int
}

func (s *generationStoreStub) GetUsageRecordByRequestID(_ context.Context, arg dbbilling.GetUsageRecordByRequestIDParams) (dbbilling.GetUsageRecordByRequestIDRow, error) {
	s.arg = arg
	s.calls++
	if s.err != nil {
		return dbbilling.GetUsageRecordByRequestIDRow{}, s.err
	}
	for _, row := range s.rows {
		if row.TenantID == arg.TenantID && row.UserID == arg.UserID && row.APIKeyID == arg.APIKeyID && row.RequestID == arg.RequestID {
			return row, nil
		}
	}
	return dbbilling.GetUsageRecordByRequestIDRow{}, pgx.ErrNoRows
}

// TestGenerationLookupScopesToAuthenticatedUserByRequestID guards the
// OpenRouter-compatible single-request attribution path. Mutation check:
// remove the SQL user_id predicate and the R_B lookup can return user B's row
// to user A; this test's A-vs-B fixture must stay discriminating.
func TestGenerationLookupScopesToAuthenticatedUserByRequestID(t *testing.T) {
	userA := auth.Identity{TenantID: 7, APIKeyID: 30, UserID: 40}
	userB := auth.Identity{TenantID: 7, APIKeyID: 31, UserID: 41}
	rowA := generationUsageRow(1, userA.TenantID, userA.APIKeyID, userA.UserID, "R_A", "ledger-a", "anthropic")
	rowB := generationUsageRow(2, userB.TenantID, userB.APIKeyID, userB.UserID, "R_B", "ledger-b", "openai")
	rowA.TokensInput = 123
	rowA.TokensOutput = 45
	rowB.TokensInput = 999
	rowB.TokensOutput = 888
	store := &generationStoreStub{rows: []dbbilling.GetUsageRecordByRequestIDRow{rowA, rowB}}
	h := NewGenerationHandler(GenerationDeps{Auth: authStub{identity: userA}, Store: store})

	own := invokeGeneration(h, "/v1/generation?id=R_A")
	assertMeStatus(t, own, http.StatusOK)
	var item map[string]any
	if err := json.Unmarshal(own.Body.Bytes(), &item); err != nil {
		t.Fatalf("decode generation response: %v body=%s", err, own.Body.String())
	}
	assertStringField(t, item, "request_id", "R_A")
	assertStringField(t, item, "ledger_id", "ledger-a")
	assertStringField(t, item, "actual_cost", "0.01000000")
	tokens, ok := item["tokens"].(map[string]any)
	if !ok || tokens["input"] != float64(123) || tokens["output"] != float64(45) {
		t.Fatalf("tokens=%v want input=123 output=45 body=%s", item["tokens"], own.Body.String())
	}
	if strings.Contains(own.Body.String(), "R_B") || strings.Contains(own.Body.String(), "ledger-b") {
		t.Fatalf("generation response leaked another user's record: %s", own.Body.String())
	}
	if store.arg.TenantID != userA.TenantID || store.arg.UserID != userA.UserID || store.arg.APIKeyID != userA.APIKeyID || store.arg.RequestID != "R_A" {
		t.Fatalf("lookup scope = tenant:%d user:%d api_key:%d request:%q want tenant:%d user:%d api_key:%d request:R_A",
			store.arg.TenantID, store.arg.UserID, store.arg.APIKeyID, store.arg.RequestID, userA.TenantID, userA.UserID, userA.APIKeyID)
	}
	for _, key := range []string{"tenant_id", "api_key_id", "user_id", "body", "prompt", "messages"} {
		if _, ok := item[key]; ok {
			t.Fatalf("generation response must reuse me usage projection and not expose %q: %v", key, item)
		}
	}

	otherUser := invokeGeneration(h, "/v1/generation?id=R_B")
	assertMeStatus(t, otherUser, http.StatusNotFound)
	if strings.Contains(otherUser.Body.String(), "R_B") || strings.Contains(otherUser.Body.String(), "ledger-b") {
		t.Fatalf("404 body leaked existence of another user's request: %s", otherUser.Body.String())
	}

	missing := invokeGeneration(h, "/v1/generation?id=R_MISSING")
	assertMeStatus(t, missing, http.StatusNotFound)
}

func TestGenerationLookupScopesToAuthenticatedAPIKey(t *testing.T) {
	// 同一 tenant/user 的不同 key 不能互查 generation。Mutation: store/SQL 只按
	// tenant+user+request_id 查，这个 key-B fixture 会 200 泄漏 ledger-b。
	userA := auth.Identity{TenantID: 7, APIKeyID: 30, UserID: 40}
	rowB := generationUsageRow(2, userA.TenantID, 31, userA.UserID, "R_KEY_B", "ledger-b", "openai")
	rowB.TokensInput = 999
	rowB.TokensOutput = 888
	store := &generationStoreStub{rows: []dbbilling.GetUsageRecordByRequestIDRow{rowB}}
	h := NewGenerationHandler(GenerationDeps{Auth: authStub{identity: userA}, Store: store})

	rec := invokeGeneration(h, "/v1/generation?id=R_KEY_B")

	assertMeStatus(t, rec, http.StatusNotFound)
	if strings.Contains(rec.Body.String(), "R_KEY_B") || strings.Contains(rec.Body.String(), "ledger-b") {
		t.Fatalf("404 body leaked another api key's request: %s", rec.Body.String())
	}
}

func TestGenerationRequiresRequestID(t *testing.T) {
	userA := auth.Identity{TenantID: 7, APIKeyID: 30, UserID: 40}
	store := &generationStoreStub{}
	h := NewGenerationHandler(GenerationDeps{Auth: authStub{identity: userA}, Store: store})

	for _, target := range []string{"/v1/generation", "/v1/generation?id=%20%20"} {
		rec := invokeGeneration(h, target)
		assertMeStatus(t, rec, http.StatusBadRequest)
	}
	if store.calls != 0 {
		t.Fatalf("missing request id must fail before store lookup, calls=%d", store.calls)
	}
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

// TestMeUsageExposesStreamShapeAndTiming — request-shape/timing residual for the
// self-service usage record. usage_records already stores stream / stream_terminated_reason
// / requested_at and ListUsageRecords already SELECTs them; the DTO dropped them.
// These are the caller's own request attributes (and are already on the admin view),
// not third-party PII like ip/user_agent. Mutation: drop any of the three projections
// (or DTO fields) -> stream reads false / the omitempty string keys go absent -> red.
func TestMeUsageExposesStreamShapeAndTiming(t *testing.T) {
	userA := auth.Identity{TenantID: 7, APIKeyID: 30, UserID: 40}
	row := meUsageRow(1, userA.TenantID, userA.APIKeyID, userA.UserID, "claude-opus-4", "claude-opus-4-20260514", "ledger-a", "anthropic")
	row.Stream = true
	row.StreamTerminatedReason = strPtr("client_disconnect")
	row.RequestedAt = pgtype.Timestamptz{Time: time.Date(2026, 5, 14, 9, 30, 0, 0, time.UTC), Valid: true}
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
	item := body.Items[0]
	if item["stream"] != true {
		t.Fatalf("stream=%v want true (projection dropped?); body=%s", item["stream"], rec.Body.String())
	}
	if item["stream_terminated_reason"] != "client_disconnect" {
		t.Fatalf("stream_terminated_reason=%v want client_disconnect; body=%s", item["stream_terminated_reason"], rec.Body.String())
	}
	if item["requested_at"] != "2026-05-14T09:30:00Z" {
		t.Fatalf("requested_at=%v want 2026-05-14T09:30:00Z; body=%s", item["requested_at"], rec.Body.String())
	}
}

// TestGenerationExposesStreamShapeAndTiming guards the /v1/generation projection of
// stream/stream_terminated_reason/requested_at. mapGenerationUsageRecord plumbs them
// from GetUsageRecordByRequestIDRow; the list-path test does not exercise this path.
// Mutation: zero the gen-path plumb lines -> the single-record response loses the
// fields (stream reads false, the omitempty keys go absent) -> this test goes red.
func TestGenerationExposesStreamShapeAndTiming(t *testing.T) {
	userA := auth.Identity{TenantID: 7, APIKeyID: 30, UserID: 40}
	row := generationUsageRow(1, userA.TenantID, userA.APIKeyID, userA.UserID, "R_A", "ledger-a", "anthropic")
	row.Stream = true
	row.StreamTerminatedReason = strPtr("upstream_eof")
	row.RequestedAt = pgtype.Timestamptz{Time: time.Date(2026, 5, 14, 9, 30, 0, 0, time.UTC), Valid: true}
	store := &generationStoreStub{rows: []dbbilling.GetUsageRecordByRequestIDRow{row}}
	h := NewGenerationHandler(GenerationDeps{Auth: authStub{identity: userA}, Store: store})

	rec := invokeGeneration(h, "/v1/generation?id=R_A")
	assertMeStatus(t, rec, http.StatusOK)
	var item map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &item); err != nil {
		t.Fatalf("decode generation response: %v body=%s", err, rec.Body.String())
	}
	if item["stream"] != true {
		t.Fatalf("stream=%v want true (gen-path projection dropped?); body=%s", item["stream"], rec.Body.String())
	}
	if item["stream_terminated_reason"] != "upstream_eof" {
		t.Fatalf("stream_terminated_reason=%v want upstream_eof; body=%s", item["stream_terminated_reason"], rec.Body.String())
	}
	if item["requested_at"] != "2026-05-14T09:30:00Z" {
		t.Fatalf("requested_at=%v want 2026-05-14T09:30:00Z; body=%s", item["requested_at"], rec.Body.String())
	}
}

// TestMeUsageDoesNotLeakClientIPOrUserAgent — PII boundary guard.
//
// The shared ListUsageRecordsRow now carries ip_address/user_agent (projected
// into the ADMIN observability list as an audit close-loop). The user-facing
// "me" usage mapper must NEVER surface these: a relay caller may not see the
// client IP/UA captured at settlement. This test seeds DISTINCTIVE sentinels on
// the row and asserts neither the sentinel nor any ip/ua JSON key appears in the
// me response body.
//
// Discriminating: the sentinels ("203.0.113.7" / "probe-UA/1.0") never occur in
// the legitimate me payload (model/cost/tokens/provider/verify_hint), so if the
// me mapper ever copied either field through, the substring assertion goes red.
// (Mutation-verified by adding the field to usageRecord + mapUsageRecord.)
func TestMeUsageDoesNotLeakClientIPOrUserAgent(t *testing.T) {
	userA := auth.Identity{TenantID: 7, APIKeyID: 30, UserID: 40}
	row := meUsageRow(1, userA.TenantID, userA.APIKeyID, userA.UserID, "claude-opus-4", "claude-opus-4-20260514", "ledger-a", "anthropic")
	const sentinelIP = "203.0.113.7"
	const sentinelUA = "probe-UA/1.0"
	const sentinelTool = "cc_tool_sentinel"
	row.IPAddress = strPtr(sentinelIP)
	row.UserAgent = strPtr(sentinelUA)
	row.ClientTool = strPtr(sentinelTool)
	store := &usageStoreStub{rows: []dbbilling.ListUsageRecordsRow{row}}
	h := NewHandler(Deps{Auth: authStub{identity: userA}, Store: store})

	rec := invokeMeUsage(h, "/v1/me/usage")
	assertMeStatus(t, rec, http.StatusOK)
	body := rec.Body.String()
	if strings.Contains(body, sentinelIP) {
		t.Fatalf("PII LEAK: me usage response exposed client ip %q; body=%s", sentinelIP, body)
	}
	if strings.Contains(body, sentinelUA) {
		t.Fatalf("PII LEAK: me usage response exposed user agent %q; body=%s", sentinelUA, body)
	}
	if strings.Contains(body, "ip_address") || strings.Contains(body, "user_agent") {
		t.Fatalf("PII LEAK: me usage response exposed an ip/ua JSON key; body=%s", body)
	}
	// client_tool (migration 0137) is also admin-only attribution: the me mapper
	// shape stays frozen, so neither the value nor the key may appear here. If a
	// later change adds client_tool to the me surface, this flags it for review.
	if strings.Contains(body, sentinelTool) || strings.Contains(body, "client_tool") {
		t.Fatalf("BOUNDARY DRIFT: me usage response exposed client_tool; body=%s", body)
	}
}

func strPtr(s string) *string { return &s }

func invokeMeUsage(h http.HandlerFunc, target string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, target, strings.NewReader(""))
	rec := httptest.NewRecorder()
	h(rec, req)
	return rec
}

func invokeGeneration(h http.HandlerFunc, target string) *httptest.ResponseRecorder {
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

func numericFromDecimal(value decimal.Decimal) pgtype.Numeric {
	return pgtype.Numeric{
		Int:   new(big.Int).Set(value.Coefficient()),
		Exp:   value.Exponent(),
		Valid: true,
	}
}

func generationUsageRow(id, tenantID, apiKeyID, userID int64, requestID, ledgerID, provider string) dbbilling.GetUsageRecordByRequestIDRow {
	providerAccountID := int64(50)
	upstreamModel := "claude-opus-4-20260514"
	created := time.Date(2026, 5, 14, 0, 0, int(id), 0, time.UTC)
	requested := created.Add(-time.Second)
	return dbbilling.GetUsageRecordByRequestIDRow{
		ID:                    id,
		TenantID:              tenantID,
		ClaimID:               200 + id,
		APIKeyID:              apiKeyID,
		UserID:                userID,
		ProviderAccountID:     &providerAccountID,
		AttemptSeq:            1,
		ActualCost:            numericFromDecimal(decimal.RequireFromString("0.01000000")),
		EndClass:              "non_streaming",
		UsageSource:           "reported",
		CreatedAt:             pgtype.Timestamptz{Time: created, Valid: true},
		RequestedAt:           pgtype.Timestamptz{Time: requested, Valid: true},
		RequestedModel:        "claude-opus-4",
		UpstreamModel:         &upstreamModel,
		Provider:              &provider,
		RequestID:             requestID,
		AuditLedgerID:         &ledgerID,
		PendingReconciliation: false,
	}
}
