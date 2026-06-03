package main

import (
	"github.com/go-chi/chi/v5"

	dbmoderation "github.com/BloomingProsperity/HUAKAI/internal/db/moderation"
	"github.com/BloomingProsperity/HUAKAI/internal/moderation"
	"github.com/BloomingProsperity/HUAKAI/internal/moderationhttp"
)

// mountModerationAdminRoutes is the slice-1 integration point. It is
// intentionally not called from routes.go yet; the PM wires it after review.
func mountModerationAdminRoutes(r chi.Router, d *deps) {
	r.Route("/admin/v1/moderation", func(r chi.Router) {
		store := moderation.NewSQLStore(dbmoderation.New(d.pgPool))
		moderationhttp.MountModerationAdminRoutes(r, moderationhttp.ModerationAdminDeps{
			Auth:  d.adminAuth,
			Store: store,
		})
	})
}
