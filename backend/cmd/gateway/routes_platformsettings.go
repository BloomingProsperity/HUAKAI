package main

import (
	"github.com/go-chi/chi/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/platformsettings"
	"github.com/BloomingProsperity/HUAKAI/internal/platformsettingshttp"
)

func mountPlatformSettingsRoutes(r chi.Router, d *deps) {
	var auth platformsettingshttp.Auth
	var service platformsettingshttp.Service
	if d != nil {
		auth = d.adminAuth
		service = platformsettings.NewService(platformsettings.NewPostgresStore(d.pgPool), nil)
	}
	r.Route("/v1/admin/platform-settings", func(r chi.Router) {
		platformsettingshttp.MountPlatformSettingsRoutes(r, platformsettingshttp.Deps{
			Auth:    auth,
			Service: service,
		})
	})
}
