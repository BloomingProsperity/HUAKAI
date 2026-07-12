package moderationhttp

import (
	"context"
	"net/http"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/adminsessionauthtest"
	"github.com/BloomingProsperity/HUAKAI/internal/moderation"
)

// fakeStore:非 nil 后端,返回良性零值——让 handler 越过鉴权前 nil 后端 503 兜底,走到真鉴权。
type fakeStore struct{}

func (fakeStore) CreateKeyword(context.Context, moderation.CreateKeywordRequest) (moderation.KeywordRule, error) {
	return moderation.KeywordRule{}, nil
}
func (fakeStore) BulkCreateKeywords(context.Context, moderation.BulkCreateKeywordsRequest) (moderation.BulkCreateResult, error) {
	return moderation.BulkCreateResult{}, nil
}
func (fakeStore) ListKeywords(context.Context, int64, int32, int32) ([]moderation.KeywordRule, error) {
	return nil, nil
}
func (fakeStore) DeleteKeyword(context.Context, int64, int64) error { return nil }
func (fakeStore) CreateHash(context.Context, moderation.CreateHashRequest) (moderation.HashRule, error) {
	return moderation.HashRule{}, nil
}
func (fakeStore) BulkCreateHashes(context.Context, moderation.BulkCreateHashesRequest) (moderation.BulkCreateResult, error) {
	return moderation.BulkCreateResult{}, nil
}
func (fakeStore) ListHashes(context.Context, int64, int32, int32) ([]moderation.HashRule, error) {
	return nil, nil
}
func (fakeStore) DeleteHash(context.Context, int64, int64) error { return nil }
func (fakeStore) GetConfig(context.Context, int64) (moderation.ModerationConfig, error) {
	return moderation.ModerationConfig{}, nil
}
func (fakeStore) UpsertConfig(context.Context, moderation.ModerationConfig) (moderation.ModerationConfig, error) {
	return moderation.ModerationConfig{}, nil
}
func (fakeStore) ListModerationLogs(context.Context, int64, *int64, int32, int32) ([]moderation.ModerationLog, error) {
	return nil, nil
}
func (fakeStore) ListBannedAPIKeys(context.Context, int64, int32, int32) ([]moderation.BannedAPIKey, error) {
	return nil, nil
}
func (fakeStore) UnbanAPIKey(context.Context, moderation.UnbanAPIKeyRequest) (moderation.UnbanAPIKeyResult, error) {
	return moderation.UnbanAPIKeyResult{}, nil
}

func mountModeration() http.Handler {
	r := chi.NewRouter()
	MountModerationAdminRoutes(r, ModerationAdminDeps{Auth: adminsessionauthtest.Resolver(), Store: fakeStore{}})
	return r
}

// SessionSafe 写端点(审核规则增删 + 解封)过鉴权≠401;token-only 的 PUT /config 对 session 仍 401。
// 变异:摘某 SessionSafe 路由的 safe → 401 → 首断言 RED;给 /config 误挂 safe → 不再 401 → 次断言 RED。
func TestModerationWriteGate(t *testing.T) {
	h := mountModeration()
	sess := adminsessionauthtest.SessionBearer

	for _, tc := range []struct{ m, p string }{
		{http.MethodPost, "/keywords"},
		{http.MethodPost, "/keywords/bulk"},
		{http.MethodDelete, "/keywords/5"},
		{http.MethodPost, "/hashes"},
		{http.MethodPost, "/hashes/bulk"},
		{http.MethodDelete, "/hashes/5"},
		{http.MethodPost, "/api-keys/5/unban"},
	} {
		if code := adminsessionauthtest.Status(h, tc.m, tc.p, sess); code == http.StatusUnauthorized {
			t.Fatalf("SessionSafe 写 %s %s 应过鉴权(≠401),得 401", tc.m, tc.p)
		}
	}

	// token-only:审核配置(调开关/阈值)对 session-admin fail-closed 401。
	if code := adminsessionauthtest.Status(h, http.MethodPut, "/config", sess); code != http.StatusUnauthorized {
		t.Fatalf("token-only PUT /config 对 session-admin 应 fail-closed 401,得 %d", code)
	}
}


// token 通道豁免:hk_admin 令牌写 token-only 的 /config 也过鉴权≠401。
func TestModerationTokenExempt(t *testing.T) {
	h := mountModeration()
	if code := adminsessionauthtest.Status(h, http.MethodPut, "/config", adminsessionauthtest.TokenBearer); code == http.StatusUnauthorized {
		t.Fatalf("hk_admin 令牌写 /config 应过鉴权(≠401),得 401")
	}
}
