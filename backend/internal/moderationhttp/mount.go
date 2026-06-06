package moderationhttp

import (
	"context"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/moderation"
)

type ModerationAdminDeps struct {
	Auth  adminAuth
	Store adminStore
}

type adminAuth interface {
	Resolve(context.Context, *http.Request) (admin.AdminIdentity, error)
}

type adminStore interface {
	CreateKeyword(context.Context, moderation.CreateKeywordRequest) (moderation.KeywordRule, error)
	BulkCreateKeywords(context.Context, moderation.BulkCreateKeywordsRequest) (moderation.BulkCreateResult, error)
	ListKeywords(context.Context, int64, int32, int32) ([]moderation.KeywordRule, error)
	DeleteKeyword(context.Context, int64, int64) error
	CreateHash(context.Context, moderation.CreateHashRequest) (moderation.HashRule, error)
	BulkCreateHashes(context.Context, moderation.BulkCreateHashesRequest) (moderation.BulkCreateResult, error)
	ListHashes(context.Context, int64, int32, int32) ([]moderation.HashRule, error)
	DeleteHash(context.Context, int64, int64) error
	GetConfig(context.Context, int64) (moderation.ModerationConfig, error)
	UpsertConfig(context.Context, moderation.ModerationConfig) (moderation.ModerationConfig, error)
	ListModerationLogs(context.Context, int64, *int64, int32, int32) ([]moderation.ModerationLog, error)
	ListBannedAPIKeys(context.Context, int64, int32, int32) ([]moderation.BannedAPIKey, error)
	UnbanAPIKey(context.Context, moderation.UnbanAPIKeyRequest) (moderation.UnbanAPIKeyResult, error)
}

func MountModerationAdminRoutes(r chi.Router, deps ModerationAdminDeps) {
	r.Get("/keywords", newKeywordListHandler(deps))
	r.Post("/keywords", newKeywordCreateHandler(deps))
	r.Post("/keywords/bulk", newKeywordBulkCreateHandler(deps))
	r.Delete("/keywords/{id}", newKeywordDeleteHandler(deps))
	r.Get("/hashes", newHashListHandler(deps))
	r.Post("/hashes", newHashCreateHandler(deps))
	r.Post("/hashes/bulk", newHashBulkCreateHandler(deps))
	r.Delete("/hashes/{id}", newHashDeleteHandler(deps))
	r.Get("/config", newConfigGetHandler(deps))
	r.Put("/config", newConfigPutHandler(deps))
	r.Get("/logs", newLogListHandler(deps))
	r.Get("/banned", newBannedListHandler(deps))
	r.Post("/api-keys/{id}/unban", newAPIKeyUnbanHandler(deps))
}
