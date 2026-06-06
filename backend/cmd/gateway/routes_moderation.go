package main

import (
	"os"
	"strings"

	"github.com/go-chi/chi/v5"

	dbmoderation "github.com/BloomingProsperity/HUAKAI/internal/db/moderation"
	"github.com/BloomingProsperity/HUAKAI/internal/moderation"
	"github.com/BloomingProsperity/HUAKAI/internal/moderationhttp"
)

const contentModerationEnabledEnv = "HUAKAI_CONTENT_MODERATION_ENABLED"

// mountModerationAdminRoutes wires the moderation admin control plane.
func mountModerationAdminRoutes(r chi.Router, d *deps) {
	r.Route("/admin/v1/moderation", func(r chi.Router) {
		store := moderation.NewSQLStore(dbmoderation.New(d.pgPool))
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
	store := moderation.NewSQLStore(dbmoderation.New(d.pgPool))
	configStore := moderation.NewExternalSettingsConfigStore(store, d.platformSettings)
	cacheOpts := moderation.CacheOptions{}
	return moderation.NewScreener(moderation.ScreenerDeps{
		Config:   configStore,
		Keywords: moderation.NewKeywordStore(store, cacheOpts),
		Hashes:   moderation.NewHashStore(store, cacheOpts),
		Audit:    moderation.NewAuditLogger(store),
		Ban:      moderation.NewBanCounter(store),
		External: moderation.NewExternalModerator(moderation.ExternalModeratorDeps{}),
	})
}

func contentModerationRuntimeEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(contentModerationEnabledEnv))) {
	case "1", "true":
		return true
	default:
		return false
	}
}
