package main

import (
	"github.com/go-chi/chi/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/userkeycontrols"
	"github.com/BloomingProsperity/HUAKAI/internal/userkeycontrolshttp"
)

// mountUserKeyControlsRoutes expects r to be scoped to /v1/api-keys inside
// the existing session-protected user API key route group.
func mountUserKeyControlsRoutes(r chi.Router, d *deps) {
	var controlsSvc userkeycontrolshttp.ControlsService
	if d != nil {
		controlsSvc = userkeycontrols.NewKeyControlService(d.pgPool, nil)
	}
	userkeycontrolshttp.MountRoutes(r, userkeycontrolshttp.Deps{Service: controlsSvc})
}
