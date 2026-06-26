package main

import (
	"github.com/go-chi/chi/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/sitepublichttp"
	"github.com/BloomingProsperity/HUAKAI/internal/tenancy"
)

// mountSiteConfigRoute 接线匿名的 GET /v1/site/config 引导（bootstrap）端点。
// 它使用具体的 *platformsettings.Service 指针判空，以避免 typed-nil 接口陷阱
// （把 nil 指针赋给接口后，与 nil 比较结果为 != nil）；随后 handler 自身的
// nil 守卫会降级返回 503。
func mountSiteConfigRoute(r chi.Router, d *deps) {
	var settings sitepublichttp.Settings
	if d != nil && d.platformSettings != nil {
		settings = d.platformSettings
	}
	r.Get("/v1/site/config", sitepublichttp.NewHandler(sitepublichttp.Deps{
		Settings: settings,
		TenantID: tenancy.DefaultWorkingTenantID,
	}))
}
