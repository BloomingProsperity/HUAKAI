package moderationhttp

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
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
	return admin.AdminIdentity{TokenID: 1, Role: admin.RolePlatformAdmin}
}

func tenantOperator(tenantID int64) admin.AdminIdentity {
	return admin.AdminIdentity{TokenID: 2, Role: admin.RoleTenantOperator, ScopeTenantID: tenantID}
}

type adminStoreStub struct {
	createCalls int
	created     moderation.CreateKeywordRequest
	createErr   error
	config      moderation.ModerationConfig
	upsertCalls int
	upserted    moderation.ModerationConfig
	hashes      map[string]moderation.HashMatch
	hashRules   []moderation.HashRule
	hashCreates int
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
