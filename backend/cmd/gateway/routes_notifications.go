package main

import (
	"github.com/go-chi/chi/v5"

	sessionauth "github.com/BloomingProsperity/HUAKAI/internal/auth"
	"github.com/BloomingProsperity/HUAKAI/internal/clientip"
	"github.com/BloomingProsperity/HUAKAI/internal/notify/notifyhttp"
	"github.com/BloomingProsperity/HUAKAI/internal/subscriptionhttp"
)

func mountNotificationRoutes(r chi.Router, d *deps) {
	var adminAuth subscriptionhttp.AdminAuth
	var reader subscriptionhttp.WorkerStatsReader
	var settings notifyhttp.SettingsService
	var sessions sessionauth.SessionValidator
	var clientIPResolver *clientip.Resolver
	if d != nil {
		adminAuth = d.adminAuth
		reader = newSubscriptionWorkerStatsReader(d.subReminderWorker, d.subExpiryWorker)
		settings = d.notificationSettings
		sessions = d.userSessions
		clientIPResolver = d.clientIPResolver
	}
	r.Get("/v1/admin/notifications/worker-stats", subscriptionhttp.NewAdminWorkerStatsHandler(subscriptionhttp.AdminWorkerStatsDeps{
		Auth:   adminAuth,
		Reader: reader,
	}))
	r.Group(func(r chi.Router) {
		r.Use(sessionauth.SessionMiddleware(sessions, clientIPResolver))
		notifyhttp.MountUserRoutes(r, notifyhttp.UserDeps{Service: settings})
	})
	notifyhttp.MountAdminRoutes(r, notifyhttp.AdminDeps{
		Auth:    adminAuth,
		Service: settings,
	})
}
