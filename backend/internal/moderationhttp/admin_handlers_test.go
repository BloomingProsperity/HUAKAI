package moderationhttp

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/admintest"
	"github.com/BloomingProsperity/HUAKAI/internal/moderation"
)

func TestAdminKeywords_PostAddsKeyword(t *testing.T) {
	store := &adminStoreStub{}
	rec := invokeModerationAdmin(t, ModerationAdminDeps{
		Auth:  adminAuthStub{ident: platformAdmin()},
		Store: store,
	}, http.MethodPost, "/admin/v1/moderation/keywords",
		`{"tenant_id":7,"keyword":"forbidden","reason_code":"policy_keyword","enabled":true}`)

	assertStatus(t, rec, http.StatusCreated)
	if store.createCalls != 1 {
		t.Fatalf("createCalls=%d want 1", store.createCalls)
	}
	if store.created.TenantID != 7 || store.created.Keyword != "forbidden" {
		t.Fatalf("created params mismatch: %+v", store.created)
	}
	var body keywordResponse
	decodeBody(t, rec, &body)
	if body.ID == 0 || body.Keyword != "forbidden" || !body.Enabled {
		t.Fatalf("response mismatch: %+v", body)
	}
}

func TestAdminKeywords_DuplicateReturns409(t *testing.T) {
	store := &adminStoreStub{createErr: moderation.ErrKeywordExists}
	rec := invokeModerationAdmin(t, ModerationAdminDeps{
		Auth:  adminAuthStub{ident: platformAdmin()},
		Store: store,
	}, http.MethodPost, "/admin/v1/moderation/keywords",
		`{"tenant_id":7,"keyword":"forbidden","reason_code":"policy_keyword","enabled":true}`)

	assertStatus(t, rec, http.StatusConflict)
	if store.createCalls != 1 {
		t.Fatalf("createCalls=%d want 1", store.createCalls)
	}
}

func TestAdminConfig_GetReturnsDefaults(t *testing.T) {
	store := &adminStoreStub{config: moderation.DefaultConfig(7)}
	rec := invokeModerationAdmin(t, ModerationAdminDeps{
		Auth:  adminAuthStub{ident: platformAdmin()},
		Store: store,
	}, http.MethodGet, "/admin/v1/moderation/config?tenant_id=7", nil)

	assertStatus(t, rec, http.StatusOK)
	var body configResponse
	decodeBody(t, rec, &body)
	if body.TenantID != 7 || body.Enabled || !body.FailClosed {
		t.Fatalf("default config mismatch: %+v", body)
	}
	var raw map[string]json.RawMessage
	decodeBody(t, rec, &raw)
	if _, ok := raw["violation_fee_usd"]; ok {
		t.Fatalf("config response exposed violation_fee_usd; MUTATION: restoring removed fee API field makes this red")
	}
}

func TestAdminConfig_PutPersistsConfig(t *testing.T) {
	store := &adminStoreStub{}
	rec := invokeModerationAdmin(t, ModerationAdminDeps{
		Auth:  adminAuthStub{ident: platformAdmin()},
		Store: store,
	}, http.MethodPut, "/admin/v1/moderation/config",
		`{"tenant_id":7,"enabled":true,"fail_closed":false,"sample_rate_pct":25,"ban_threshold":4,"ban_window_seconds":600}`)

	assertStatus(t, rec, http.StatusOK)
	if store.upsertCalls != 1 {
		t.Fatalf("upsertCalls=%d want 1", store.upsertCalls)
	}
	if !store.upserted.Enabled || store.upserted.FailClosed || store.upserted.SampleRatePct != 25 {
		t.Fatalf("upserted config mismatch: %+v", store.upserted)
	}
}

func TestAdminKeywords_TenantOperatorCannotCrossTenant(t *testing.T) {
	store := &adminStoreStub{}
	rec := invokeModerationAdmin(t, ModerationAdminDeps{
		Auth:  adminAuthStub{ident: tenantOperator(7)},
		Store: store,
	}, http.MethodPost, "/admin/v1/moderation/keywords",
		`{"tenant_id":8,"keyword":"forbidden","enabled":true}`)

	assertStatus(t, rec, http.StatusForbidden)
	if store.createCalls != 0 {
		t.Fatalf("cross-tenant request touched store: calls=%d", store.createCalls)
	}
}

func TestAdminHashes_PostAddsHashAndFeedsHotPath(t *testing.T) {
	// 判别 hash 封禁可运营:POST /hashes 后同一个 hash store 必须驱动热路径
	// block_hash。Mutation:去掉 route 或只返回 201 不写 store,会 404 或 pass。
	payloadHash := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	store := &adminStoreStub{
		config: moderation.ModerationConfig{TenantID: 7, Enabled: true, FailClosed: true},
		hashes: map[string]moderation.HashMatch{},
	}
	rec := invokeModerationAdmin(t, ModerationAdminDeps{
		Auth:  adminAuthStub{ident: platformAdmin()},
		Store: store,
	}, http.MethodPost, "/admin/v1/moderation/hashes",
		`{"tenant_id":7,"hash_hex":"`+payloadHash+`","reason_code":"known_bad_payload","enabled":true}`)

	assertStatus(t, rec, http.StatusCreated)
	screener := moderation.NewScreener(moderation.ScreenerDeps{
		Config: store, Keywords: store, Hashes: store,
	})
	result, err := screener.Screen(context.Background(), moderation.ScreenRequest{
		TenantID: 7, APIKeyID: 30, UserID: 40, RequestID: "req-hash", PayloadHash: payloadHash,
	})
	if err != nil {
		t.Fatalf("screen returned error: %v", err)
	}
	if result.Decision != moderation.DecisionBlockHash || result.MatchedHashID == nil {
		t.Fatalf("screen result=%+v want block_hash with matched hash id", result)
	}
}

func TestAdminModerationLogs_ListPassesTenantFilterAndPage(t *testing.T) {
	now := time.Date(2026, 6, 6, 12, 0, 0, 0, time.UTC)
	store := &adminStoreStub{
		moderationLogs: []moderation.ModerationLog{{
			ID:          90,
			TenantID:    7,
			APIKeyID:    30,
			UserID:      40,
			RequestID:   "req-visible",
			PayloadHash: "payload-hash-visible",
			Decision:    moderation.DecisionBlockKeyword,
			ReasonCode:  "policy_keyword",
			OccurredAt:  now,
		}},
	}
	rec := invokeModerationAdmin(t, ModerationAdminDeps{
		Auth:  adminAuthStub{ident: platformAdmin()},
		Store: store,
	}, http.MethodGet, "/admin/v1/moderation/logs?tenant_id=7&api_key_id=30&limit=2&offset=1", nil)

	assertStatus(t, rec, http.StatusOK)
	if store.listLogTenantID != 7 || store.listLogAPIKeyID == nil || *store.listLogAPIKeyID != 30 ||
		store.listLogLimit != 2 || store.listLogOffset != 1 {
		t.Fatalf("list log params mismatch tenant=%d api_key=%v limit=%d offset=%d",
			store.listLogTenantID, store.listLogAPIKeyID, store.listLogLimit, store.listLogOffset)
	}
	var body moderationLogListResponse
	decodeBody(t, rec, &body)
	if body.Object != "moderation_logs_list" || len(body.Items) != 1 ||
		body.Items[0].ID != 90 || body.Items[0].PayloadHash != "payload-hash-visible" {
		t.Fatalf("moderation logs response mismatch: %+v", body)
	}
	var raw map[string]any
	decodeBody(t, rec, &raw)
	items, ok := raw["items"].([]any)
	if !ok || len(items) != 1 {
		t.Fatalf("raw moderation logs items mismatch: %+v", raw["items"])
	}
	item, ok := items[0].(map[string]any)
	if !ok {
		t.Fatalf("raw moderation log item mismatch: %+v", items[0])
	}
	if _, ok := item["violation_fee_usd"]; ok {
		t.Fatalf("log response exposed violation_fee_usd; MUTATION: restoring removed fee API field makes this red")
	}
	if _, ok := item["billing_event_id"]; ok {
		t.Fatalf("log response exposed billing_event_id; MUTATION: restoring removed billing API field makes this red")
	}
}

func TestAdminModerationBannedKeys_ListUsesTenantPage(t *testing.T) {
	now := time.Date(2026, 6, 6, 12, 5, 0, 0, time.UTC)
	store := &adminStoreStub{
		bannedKeys: []moderation.BannedAPIKey{{
			ID:              30,
			TenantID:        7,
			UserID:          40,
			Name:            "risk-key",
			KeyPrefix:       "hk_test_risk",
			Status:          "disabled",
			ViolationCount:  3,
			LastViolationAt: now,
			UpdatedAt:       now,
		}},
	}
	rec := invokeModerationAdmin(t, ModerationAdminDeps{
		Auth:  adminAuthStub{ident: platformAdmin()},
		Store: store,
	}, http.MethodGet, "/admin/v1/moderation/banned?tenant_id=7&limit=10&offset=5", nil)

	assertStatus(t, rec, http.StatusOK)
	if store.listBannedTenantID != 7 || store.listBannedLimit != 10 || store.listBannedOffset != 5 {
		t.Fatalf("list banned params mismatch tenant=%d limit=%d offset=%d",
			store.listBannedTenantID, store.listBannedLimit, store.listBannedOffset)
	}
	var body bannedAPIKeyListResponse
	decodeBody(t, rec, &body)
	if body.Object != "moderation_banned_keys_list" || len(body.Items) != 1 ||
		body.Items[0].ID != 30 || body.Items[0].Status != "disabled" || body.Items[0].KeyPrefix == "" {
		t.Fatalf("banned keys response mismatch: %+v", body)
	}
}

func TestAdminModerationUnban_PassesActorReasonAndReturnsAudit(t *testing.T) {
	now := time.Date(2026, 6, 6, 12, 10, 0, 0, time.UTC)
	store := &adminStoreStub{
		unbanResult: moderation.UnbanAPIKeyResult{
			APIKeyID:   30,
			TenantID:   7,
			Status:     "active",
			AuditLogID: 77,
			UpdatedAt:  now,
		},
	}
	rec := invokeModerationAdmin(t, ModerationAdminDeps{
		Auth:  adminAuthStub{ident: platformAdmin()},
		Store: store,
	}, http.MethodPost, "/admin/v1/moderation/api-keys/30/unban",
		`{"tenant_id":7,"reason":"manual review cleared"}`)

	assertStatus(t, rec, http.StatusOK)
	if store.unbanCalls != 1 {
		t.Fatalf("unban calls=%d want 1", store.unbanCalls)
	}
	if store.unbanReq.TenantID != 7 || store.unbanReq.APIKeyID != 30 ||
		store.unbanReq.ActorID != "admin_token:1" || store.unbanReq.Reason != "manual review cleared" {
		t.Fatalf("unban request mismatch: %+v", store.unbanReq)
	}
	var body unbanAPIKeyResponse
	decodeBody(t, rec, &body)
	if body.APIKeyID != 30 || body.Status != "active" || body.AuditLogID != 77 {
		t.Fatalf("unban response mismatch: %+v", body)
	}
}

func TestAdminModerationUnban_AdminAuthRequired(t *testing.T) {
	// 变异：绕过 resolveAdmin 直接调用 Store.UnbanAPIKey；这时本测试会返回 200
	// 并使 unbanCalls 自增，而不是保住 403。
	store := &adminStoreStub{}
	rec := invokeModerationAdmin(t, ModerationAdminDeps{
		Auth:  adminAuthStub{err: admin.ErrAdminForbidden},
		Store: store,
	}, http.MethodPost, "/admin/v1/moderation/api-keys/30/unban",
		`{"tenant_id":7,"reason":"manual review cleared"}`)

	assertStatus(t, rec, http.StatusForbidden)
	if store.unbanCalls != 0 {
		t.Fatalf("non-admin request touched unban store: calls=%d", store.unbanCalls)
	}
}

func TestBulkAdminAuthRequired(t *testing.T) {
	// 变异：把 bulk handler 挂在 resolveAdmin 之外，或在鉴权之前就调用 Store；
	// 这些请求会返回 200 并使 bulk store 调用数自增，而不是 403。
	for _, tc := range []struct {
		name string
		path string
		body string
	}{
		{
			name: "keywords",
			path: "/admin/v1/moderation/keywords/bulk",
			body: `{"tenant_id":7,"items":[{"keyword":"forbidden","enabled":true}]}`,
		},
		{
			name: "hashes",
			path: "/admin/v1/moderation/hashes/bulk",
			body: `{"tenant_id":7,"items":[{"hash_hex":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","enabled":true}]}`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &adminStoreStub{}
			rec := invokeModerationAdmin(t, ModerationAdminDeps{
				Auth:  adminAuthStub{err: admin.ErrAdminForbidden},
				Store: store,
			}, http.MethodPost, tc.path, tc.body)

			assertStatus(t, rec, http.StatusForbidden)
			if store.bulkKeywordCalls != 0 || store.bulkHashCalls != 0 {
				t.Fatalf("non-admin request touched bulk store: keyword_calls=%d hash_calls=%d",
					store.bulkKeywordCalls, store.bulkHashCalls)
			}
		})
	}
}

func invokeModerationAdmin(t *testing.T, deps ModerationAdminDeps, method string, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	r := chi.NewRouter()
	r.Route("/admin/v1/moderation", func(r chi.Router) {
		MountModerationAdminRoutes(r, deps)
	})
	var reader *bytes.Reader
	switch v := body.(type) {
	case nil:
		reader = bytes.NewReader(nil)
	case string:
		reader = bytes.NewReader([]byte(v))
	default:
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		reader = bytes.NewReader(b)
	}
	req := httptest.NewRequest(method, path, reader)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func assertStatus(t *testing.T, rec *httptest.ResponseRecorder, want int) {
	t.Helper()
	if rec.Code != want {
		t.Fatalf("status=%d want %d body=%s", rec.Code, want, rec.Body.String())
	}
}

func decodeBody(t *testing.T, rec *httptest.ResponseRecorder, dst any) {
	t.Helper()
	if err := json.Unmarshal(rec.Body.Bytes(), dst); err != nil {
		t.Fatalf("decode body: %v body=%s", err, rec.Body.String())
	}
}

type adminAuthStub struct {
	ident admin.AdminIdentity
	err   error
}

func (s adminAuthStub) Resolve(context.Context, *http.Request) (admin.AdminIdentity, error) {
	if s.err != nil {
		return admin.AdminIdentity{}, s.err
	}
	return s.ident, nil
}

func platformAdmin() admin.AdminIdentity {
	return admintest.Platform(1)
}

func tenantOperator(tenantID int64) admin.AdminIdentity {
	return admintest.TenantOperator(2, tenantID)
}

type adminStoreStub struct {
	createCalls      int
	created          moderation.CreateKeywordRequest
	createErr        error
	config           moderation.ModerationConfig
	upsertCalls      int
	upserted         moderation.ModerationConfig
	hashes           map[string]moderation.HashMatch
	hashRules        []moderation.HashRule
	hashCreates      int
	bulkKeywordCalls int
	bulkKeywordReq   moderation.BulkCreateKeywordsRequest
	bulkKeywordRes   moderation.BulkCreateResult
	bulkKeywordErr   error
	bulkHashCalls    int
	bulkHashReq      moderation.BulkCreateHashesRequest
	bulkHashRes      moderation.BulkCreateResult
	bulkHashErr      error

	moderationLogs  []moderation.ModerationLog
	listLogTenantID int64
	listLogAPIKeyID *int64
	listLogLimit    int32
	listLogOffset   int32

	bannedKeys         []moderation.BannedAPIKey
	listBannedTenantID int64
	listBannedLimit    int32
	listBannedOffset   int32

	unbanCalls  int
	unbanReq    moderation.UnbanAPIKeyRequest
	unbanResult moderation.UnbanAPIKeyResult
	unbanErr    error
}

func (s *adminStoreStub) CreateKeyword(_ context.Context, req moderation.CreateKeywordRequest) (moderation.KeywordRule, error) {
	s.createCalls++
	s.created = req
	if s.createErr != nil {
		return moderation.KeywordRule{}, s.createErr
	}
	return moderation.KeywordRule{
		ID:         33,
		TenantID:   req.TenantID,
		Keyword:    req.Keyword,
		ReasonCode: req.ReasonCode,
		Enabled:    req.Enabled,
	}, nil
}

func (s *adminStoreStub) BulkCreateKeywords(_ context.Context, req moderation.BulkCreateKeywordsRequest) (moderation.BulkCreateResult, error) {
	s.bulkKeywordCalls++
	s.bulkKeywordReq = req
	if s.bulkKeywordErr != nil {
		return moderation.BulkCreateResult{}, s.bulkKeywordErr
	}
	return s.bulkKeywordRes, nil
}

func (s *adminStoreStub) ListKeywords(context.Context, int64, int32, int32) ([]moderation.KeywordRule, error) {
	return nil, nil
}

func (s *adminStoreStub) ListEnabled(context.Context, int64) ([]moderation.KeywordRule, error) {
	return nil, nil
}

func (s *adminStoreStub) DeleteKeyword(context.Context, int64, int64) error {
	return nil
}

func (s *adminStoreStub) CreateHash(_ context.Context, req moderation.CreateHashRequest) (moderation.HashRule, error) {
	s.hashCreates++
	id := int64(70 + s.hashCreates)
	row := moderation.HashRule{
		ID:         id,
		TenantID:   req.TenantID,
		HashHex:    req.HashHex,
		ReasonCode: req.ReasonCode,
		Enabled:    req.Enabled,
	}
	s.hashRules = append(s.hashRules, row)
	if s.hashes == nil {
		s.hashes = map[string]moderation.HashMatch{}
	}
	if req.Enabled {
		s.hashes[req.HashHex] = moderation.HashMatch{Matched: true, ID: id, ReasonCode: req.ReasonCode}
	}
	return row, nil
}

func (s *adminStoreStub) BulkCreateHashes(_ context.Context, req moderation.BulkCreateHashesRequest) (moderation.BulkCreateResult, error) {
	s.bulkHashCalls++
	s.bulkHashReq = req
	if s.bulkHashErr != nil {
		return moderation.BulkCreateResult{}, s.bulkHashErr
	}
	return s.bulkHashRes, nil
}

func (s *adminStoreStub) ListHashes(context.Context, int64, int32, int32) ([]moderation.HashRule, error) {
	return s.hashRules, nil
}

func (s *adminStoreStub) DeleteHash(context.Context, int64, int64) error {
	return nil
}

func (s *adminStoreStub) GetConfig(_ context.Context, tenantID int64) (moderation.ModerationConfig, error) {
	if s.config.TenantID == 0 {
		return moderation.DefaultConfig(tenantID), nil
	}
	return s.config, nil
}

func (s *adminStoreStub) UpsertConfig(_ context.Context, cfg moderation.ModerationConfig) (moderation.ModerationConfig, error) {
	s.upsertCalls++
	s.upserted = cfg
	return cfg, nil
}

func (s *adminStoreStub) ListModerationLogs(_ context.Context, tenantID int64, apiKeyID *int64, limit int32, offset int32) ([]moderation.ModerationLog, error) {
	s.listLogTenantID = tenantID
	s.listLogAPIKeyID = apiKeyID
	s.listLogLimit = limit
	s.listLogOffset = offset
	return s.moderationLogs, nil
}

func (s *adminStoreStub) ListBannedAPIKeys(_ context.Context, tenantID int64, limit int32, offset int32) ([]moderation.BannedAPIKey, error) {
	s.listBannedTenantID = tenantID
	s.listBannedLimit = limit
	s.listBannedOffset = offset
	return s.bannedKeys, nil
}

func (s *adminStoreStub) UnbanAPIKey(_ context.Context, req moderation.UnbanAPIKeyRequest) (moderation.UnbanAPIKeyResult, error) {
	s.unbanCalls++
	s.unbanReq = req
	if s.unbanErr != nil {
		return moderation.UnbanAPIKeyResult{}, s.unbanErr
	}
	return s.unbanResult, nil
}

func (s *adminStoreStub) Contains(_ context.Context, tenantID int64, hashHex string) (moderation.HashMatch, error) {
	if s.hashes == nil {
		return moderation.HashMatch{}, nil
	}
	match := s.hashes[hashHex]
	if !match.Matched {
		return moderation.HashMatch{}, nil
	}
	return match, nil
}
