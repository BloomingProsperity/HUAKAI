package main

import (
	"os"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/moderation"
	"github.com/BloomingProsperity/HUAKAI/internal/moderationhttp"
)

const contentModerationEnabledEnv = "HUAKAI_CONTENT_MODERATION_ENABLED"

// mountModerationAdminRoutes 接线内容审核（moderation）的管理控制面。
func mountModerationAdminRoutes(r chi.Router, d *deps) {
	r.Route("/admin/v1/moderation", func(r chi.Router) {
		store := moderation.NewSQLStoreWithPool(d.pgPool)
		moderationhttp.MountModerationAdminRoutes(r, moderationhttp.ModerationAdminDeps{
			Auth:  d.adminAuth,
			Store: store,
		})
	})
}

func moderationScreener(d *deps) moderation.Screener {
	if !contentModerationRuntimeEnabled() || d == nil || d.pgPool == nil {
		return nil
	}
	store := moderation.NewSQLStoreWithPool(d.pgPool)
	configStore := moderation.NewExternalSettingsConfigStore(store, d.platformSettings)
	return moderation.NewScreener(moderation.ScreenerDeps{
		Config:         configStore,
		Keywords:       store,
		Hashes:         store,
		Audit:          moderation.NewAuditLogger(store),
		Ban:            moderation.NewBanCounter(store),
		External:       moderation.NewExternalModerator(moderation.ExternalModeratorDeps{}),
		ConfigCacheTTL: -1,
	})
}

func contentModerationRuntimeEnabled() bool {
	// 执行器默认接线,但租户级 DefaultConfig 保持 Enabled=false,未配置租户仍会
	// 全量放行。翻开运行时 gate 只让管理员 PUT enabled=true 后真实生效;事故时
	// 可用 HUAKAI_CONTENT_MODERATION_ENABLED=false/0 关闭执行器。
	switch strings.ToLower(strings.TrimSpace(os.Getenv(contentModerationEnabledEnv))) {
	case "0", "false":
		return false
	default:
		return true
	}
}
