package main

import (
	"github.com/go-chi/chi/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/controlhttp"
	"github.com/BloomingProsperity/HUAKAI/internal/platformsettings"
)

func mountPlatformSettingsRoutes(r chi.Router, d *deps) {
	var auth controlhttp.PlatformSettingsAuth
	if d != nil {
		auth = d.adminAuth
	}
	r.Route("/v1/admin/platform-settings", func(r chi.Router) {
		controlhttp.MountPlatformSettingsRoutes(r, controlhttp.PlatformSettingsDeps{
			Auth:                    auth,
			Service:                 platformSettingsRouteService(d),
			CaptchaSecretConfigured: captchaTurnstileSecret() != "",
		})
	})
}

// platformSettingsRouteService 用具体指针判空:nil *platformsettings.Service 赋给接口会变 typed-nil
// (!=nil),会让 pgPool 兜底永不触发。必须先判具体指针,再决定用它还是兜底/nil。
func platformSettingsRouteService(d *deps) controlhttp.PlatformSettingsService {
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
