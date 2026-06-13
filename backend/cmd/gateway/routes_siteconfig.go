package main

import (
	"github.com/go-chi/chi/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/sitepublichttp"
	"github.com/BloomingProsperity/HUAKAI/internal/tenancy"
)

// mountSiteConfigRoute wires the anonymous GET /v1/site/config bootstrap
// endpoint. It uses the concrete *platformsettings.Service pointer check to
// avoid the typed-nil interface trap (a nil pointer assigned to an interface
// compares != nil); the handler's own nil guard then degrades to 503.
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
