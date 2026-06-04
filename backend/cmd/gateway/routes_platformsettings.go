package main

import (
	"github.com/go-chi/chi/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/platformsettings"
	"github.com/BloomingProsperity/HUAKAI/internal/platformsettingshttp"
)

func mountPlatformSettingsRoutes(r chi.Router, d *deps) {
	var auth platformsettingshttp.Auth
	if d != nil {
		auth = d.adminAuth
	}
	r.Route("/v1/admin/platform-settings", func(r chi.Router) {
		platformsettingshttp.MountPlatformSettingsRoutes(r, platformsettingshttp.Deps{
			Auth:                    auth,
			Service:                 platformSettingsRouteService(d),
			CaptchaSecretConfigured: captchaTurnstileSecret() != "",
		})
	})
}

func platformSettingsRouteService(d *deps) platformsettingshttp.Service {
	if d == nil {
		return nil
	}
	if d.platformSettings != nil {
		return d.platformSettings
	}
	if d.pgPool != nil {
		return platformsettings.NewService(platformsettings.NewPostgresStore(d.pgPool), nil)
	}
	return nil
}
