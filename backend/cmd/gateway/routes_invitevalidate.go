package main

import (
	"github.com/go-chi/chi/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/invitevalidatehttp"
)

func mountInviteValidateRoutes(r chi.Router, d *deps) {
	var store invitevalidatehttp.Store
	var settings invitevalidatehttp.Settings
	if d != nil {
		if d.userAuth != nil && d.userAuth.Store != nil {
			if s, ok := d.userAuth.Store.(invitevalidatehttp.Store); ok {
				store = s
			}
		}
		settings = d.platformSettings
	}
	r.Post("/validate-invitation-code", invitevalidatehttp.NewHandler(invitevalidatehttp.Deps{
		Store:    store,
		Settings: settings,
	}))
}
