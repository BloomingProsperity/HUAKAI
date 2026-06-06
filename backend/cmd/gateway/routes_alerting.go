package main

import (
	"github.com/go-chi/chi/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/alerting"
	"github.com/BloomingProsperity/HUAKAI/internal/alertinghttp"
)

func mountAlertingAdminRoutes(r chi.Router, d *deps) {
	var auth alertinghttp.AdminAuth
	var service alertinghttp.Service
	if d != nil {
		auth = d.adminAuth
		if d.pgPool != nil {
			service = alerting.NewService(alerting.NewPostgresStore(d.pgPool))
		}
	}
	alertinghttp.MountAdminRoutes(r, alertinghttp.AdminDeps{
		Auth:    auth,
		Service: service,
	})
}
