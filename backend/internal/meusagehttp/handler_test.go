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
	calls   int
}

func (s *usageStoreStub) ListUsageRecords(_ context.Context, arg dbbilling.ListUsageRecordsParams) ([]dbbilling.ListUsageRecordsRow, error) {
	s.calls++
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

// TestGenerationLookupScopesToAuthenticatedUserByRequestID 守护
// OpenRouter 兼容的单请求归属路径。变异:移除 SQL user_id 谓词后,
// R_B 查询可能把用户 B 的行返给用户 A;本测试的 A-vs-B fixture 必须保持区分度。
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
	// 同一 tenant/user 的不同 key 不能互查 generation。变异:store/SQL 只按
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

// TestMeUsageFilterParamsPassThrough 守护 model/provider/status 从查询参数到
// 数据库查询参数的完整接线。变异:漏传任一字段会留下 nil 或错误值并使断言变红。
func TestMeUsageFilterParamsPassThrough(t *testing.T) {
	userA := auth.Identity{TenantID: 7, APIKeyID: 30, UserID: 40}
	store := &usageStoreStub{}
	h := NewHandler(Deps{Auth: authStub{identity: userA}, Store: store})

	rec := invokeMeUsage(h, "/v1/me/usage?model=gpt-4&status=error&provider=openai")

	assertMeStatus(t, rec, http.StatusOK)
	if store.calls != 1 {
		t.Fatalf("用量 Store 调用次数=%d,期望 1", store.calls)
	}
	if got := store.listArg.Model; got == nil || *got != "gpt-4" {
		t.Fatalf("Model=%v,期望非 nil 且值为 gpt-4", got)
	}
	if got := store.listArg.Provider; got == nil || *got != "openai" {
		t.Fatalf("Provider=%v,期望非 nil 且值为 openai", got)
	}
	if got := store.listArg.Outcome; got == nil || *got != "error" {
		t.Fatalf("Outcome=%v,期望非 nil 且值为 error", got)
	}
}

// TestMeUsageStatusSuccessMapsOutcome 守护 success 状态映射。
// 变异:删除或改错映射后,Outcome 会是 nil 或非 success,断言随即变红。
func TestMeUsageStatusSuccessMapsOutcome(t *testing.T) {
	userA := auth.Identity{TenantID: 7, APIKeyID: 30, UserID: 40}
	store := &usageStoreStub{}
	h := NewHandler(Deps{Auth: authStub{identity: userA}, Store: store})

	rec := invokeMeUsage(h, "/v1/me/usage?status=success")

	assertMeStatus(t, rec, http.StatusOK)
	if store.calls != 1 {
		t.Fatalf("用量 Store 调用次数=%d,期望 1", store.calls)
	}
	if got := store.listArg.Outcome; got == nil || *got != "success" {
		t.Fatalf("Outcome=%v,期望非 nil 且值为 success", got)
	}
}

// TestMeUsageRejectsInvalidStatus 守护非法状态在查询 Store 前被拒绝。
// 变异:静默忽略非法状态会返回 200 并调用 Store,状态与调用次数断言都会变红。
func TestMeUsageRejectsInvalidStatus(t *testing.T) {
	userA := auth.Identity{TenantID: 7, APIKeyID: 30, UserID: 40}
	store := &usageStoreStub{}
	h := NewHandler(Deps{Auth: authStub{identity: userA}, Store: store})

	rec := invokeMeUsage(h, "/v1/me/usage?status=foo")

	assertMeStatus(t, rec, http.StatusBadRequest)
	assertMeErrorCode(t, rec, "invalid_status")
	if store.calls != 0 {
		t.Fatalf("非法状态不应调用用量 Store,实际调用次数=%d", store.calls)
	}
}

// TestMeUsageNoFiltersLeavesParamsNil 守护无过滤条件时的缺省语义。
// 变异:误设任一过滤字段会使对应的 nil 断言变红。
func TestMeUsageNoFiltersLeavesParamsNil(t *testing.T) {
	userA := auth.Identity{TenantID: 7, APIKeyID: 30, UserID: 40}
	store := &usageStoreStub{}
	h := NewHandler(Deps{Auth: authStub{identity: userA}, Store: store})

	rec := invokeMeUsage(h, "/v1/me/usage")

	assertMeStatus(t, rec, http.StatusOK)
	if store.calls != 1 {
		t.Fatalf("用量 Store 调用次数=%d,期望 1", store.calls)
	}
	if store.listArg.Model != nil || store.listArg.Provider != nil || store.listArg.Outcome != nil {
		t.Fatalf("无过滤条件时过滤参数必须全为 nil,实际 Model=%v Provider=%v Outcome=%v",
			store.listArg.Model, store.listArg.Provider, store.listArg.Outcome)
	}
}

// TestMeUsageRejectsOverlongModel 守护 model 的 200 字符上限。
// 变异:移除长度校验会返回 200 并调用 Store,状态与调用次数断言都会变红。
func TestMeUsageRejectsOverlongModel(t *testing.T) {
	userA := auth.Identity{TenantID: 7, APIKeyID: 30, UserID: 40}
	store := &usageStoreStub{}
	h := NewHandler(Deps{Auth: authStub{identity: userA}, Store: store})

	rec := invokeMeUsage(h, "/v1/me/usage?model="+strings.Repeat("a", 201))

	assertMeStatus(t, rec, http.StatusBadRequest)
	assertMeErrorCode(t, rec, "invalid_model")
	if store.calls != 0 {
		t.Fatalf("超长 model 不应调用用量 Store,实际调用次数=%d", store.calls)
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

// TestMeUsageExposesPerRequestTokenCounts 是 "relay 请求日志" 残留缺口的
// 区分性测试。usage_records 已存储 token 计数,ListUsageRecords 也已 SELECT 它们,
// 但 DTO 把它们丢弃了。变异:移除 Tokens 映射(或 DTO 字段)后,
// item["tokens"] 缺失 -> 本测试变红。
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

// TestMeUsageExposesStreamShapeAndTiming —— 自助用量记录的请求形态/时序残留缺口。
// usage_records 已存储 stream / stream_terminated_reason / requested_at,
// ListUsageRecords 也已 SELECT 它们;但 DTO 把它们丢弃了。
// 这些是调用方自己的请求属性(且已在 admin 视图中),
// 而非 ip/user_agent 这类第三方 PII。变异:丢掉这三个投影中任一个
// (或 DTO 字段)-> stream 读出 false / 那些 omitempty 字符串键缺失 -> 变红。
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

// TestGenerationExposesStreamShapeAndTiming 守护 /v1/generation 对
// stream/stream_terminated_reason/requested_at 的投影。mapGenerationUsageRecord
// 把它们从 GetUsageRecordByRequestIDRow 接出;list 路径的测试不会走到本路径。
// 变异:把 gen 路径的接线行清零 -> 单条记录响应丢失这些字段
// (stream 读出 false,那些 omitempty 键缺失)-> 本测试变红。
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

// TestMeUsageDoesNotLeakClientIPOrUserAgent —— PII 边界守卫。
//
// 共享的 ListUsageRecordsRow 现在带有 ip_address/user_agent(作为审计闭环
// 投影进 ADMIN 可观测列表)。面向用户的 "me" 用量 mapper 绝不能暴露这些:
// relay 调用方不应看到 settlement 时捕获的客户端 IP/UA。本测试在行上播种
// 独特的 sentinel,并断言 sentinel 本身以及任何 ip/ua JSON 键都不会出现在
// me 响应 body 中。
//
// 区分度:这些 sentinel("203.0.113.7" / "probe-UA/1.0")绝不会出现在合法的
// me payload(model/cost/tokens/provider/verify_hint)里,因此一旦 me mapper
// 把任一字段透传出去,子串断言就会变红。
// (已通过把该字段加进 usageRecord + mapUsageRecord 做变异验证。)
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
	// client_tool(迁移 0137)同样是 admin-only 归属:me mapper 的形态保持冻结,
	// 因此其值与键都不得出现在这里。若日后改动把 client_tool 加进 me 面,
	// 本断言会把它标出来供复查。
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

func assertMeErrorCode(t *testing.T, rec *httptest.ResponseRecorder, want string) {
	t.Helper()
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("解析错误响应失败: %v body=%s", err, rec.Body.String())
	}
	if body.Error.Code != want {
		t.Fatalf("错误码=%q,期望 %q body=%s", body.Error.Code, want, rec.Body.String())
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
