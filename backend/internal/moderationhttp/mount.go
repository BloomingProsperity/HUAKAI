package moderationhttp

import (
	"context"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/adminsessionauth"
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
	ListModerationViolations(context.Context, int64, *int64, *int64, int32, int32) ([]moderation.ModerationViolation, error)
	ListBannedAPIKeys(context.Context, int64, int32, int32) ([]moderation.BannedAPIKey, error)
	DisableAPIKey(context.Context, moderation.DisableAPIKeyRequest) (moderation.DisableAPIKeyResult, error)
	UnbanAPIKey(context.Context, moderation.UnbanAPIKeyRequest) (moderation.UnbanAPIKeyResult, error)
}

func MountModerationAdminRoutes(r chi.Router, deps ModerationAdminDeps) {
	// 规则和配置只允许部署者管理；租户管理员只能读取本租户违规投影并恢复仍由审核停用的 Key。
	// 人工停用必须引用同租户、同 Key 且已经越线的永久违规事件。
	safe := adminsessionauth.AllowSessionWrite(adminsessionauth.SessionSafe)
	r.Get("/keywords", newKeywordListHandler(deps))
	r.With(safe).Post("/keywords", newKeywordCreateHandler(deps))
	r.With(safe).Post("/keywords/bulk", newKeywordBulkCreateHandler(deps))
	r.With(safe).Delete("/keywords/{id}", newKeywordDeleteHandler(deps))
	r.Get("/hashes", newHashListHandler(deps))
	r.With(safe).Post("/hashes", newHashCreateHandler(deps))
	r.With(safe).Post("/hashes/bulk", newHashBulkCreateHandler(deps))
	r.With(safe).Delete("/hashes/{id}", newHashDeleteHandler(deps))
	r.Get("/config", newConfigGetHandler(deps))
	r.Put("/config", newConfigPutHandler(deps))
	r.Get("/logs", newLogListHandler(deps))
	r.Get("/violations", newViolationListHandler(deps))
	r.Get("/banned", newBannedListHandler(deps))
	r.With(safe).Post("/api-keys/{id}/disable", newAPIKeyDisableHandler(deps))
	r.With(safe).Post("/api-keys/{id}/unban", newAPIKeyUnbanHandler(deps))
}
