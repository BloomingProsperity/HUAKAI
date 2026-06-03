package main

import (
	"github.com/go-chi/chi/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/auth"
	"github.com/BloomingProsperity/HUAKAI/internal/userkeycontrols"
	"github.com/BloomingProsperity/HUAKAI/internal/userkeycontrolshttp"
)

func mountUserKeyControlsRoutes(r chi.Router, d *deps) {
	var controlsSvc userkeycontrolshttp.ControlsService
	if d != nil {
		controlsSvc = userkeycontrols.NewKeyControlService(d.pgPool, nil)
	}
	r.Route("/v1/api-keys", func(r chi.Router) {
		if d != nil {
			r.Use(auth.SessionMiddleware(d.userSessions, d.clientIPResolver))
		}
		userkeycontrolshttp.MountRoutes(r, userkeycontrolshttp.Deps{Service: controlsSvc})
	})
}
