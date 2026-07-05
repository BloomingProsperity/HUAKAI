package main

import (
	"github.com/go-chi/chi/v5"

	sessionauth "github.com/BloomingProsperity/HUAKAI/internal/auth"
	"github.com/BloomingProsperity/HUAKAI/internal/clientip"
	"github.com/BloomingProsperity/HUAKAI/internal/controlhttp"
	"github.com/BloomingProsperity/HUAKAI/internal/subscriptionhttp"
	"github.com/BloomingProsperity/HUAKAI/internal/usernoticehttp"
)

func mountNotificationRoutes(r chi.Router, d *deps) {
	var adminAuth subscriptionhttp.AdminAuth
	var reader subscriptionhttp.WorkerStatsReader
	var settings controlhttp.NotifySettingsService
	var inbox usernoticehttp.Service
	var sessions sessionauth.SessionValidator
	var clientIPResolver *clientip.Resolver
	if d != nil {
		adminAuth = d.adminAuth
		reader = newSubscriptionWorkerStatsReader(d.subReminderWorker, d.subExpiryWorker, d.subAutoRenewWorker)
		settings = d.notificationSettings
		inbox = d.userNoticeService
		sessions = d.userSessions
		clientIPResolver = d.clientIPResolver
	}
	r.Get("/v1/admin/notifications/worker-stats", subscriptionhttp.NewAdminWorkerStatsHandler(subscriptionhttp.AdminWorkerStatsDeps{
		Auth:   adminAuth,
		Reader: reader,
	}))
	r.Group(func(r chi.Router) {
		r.Use(sessionauth.SessionMiddleware(sessions, clientIPResolver))
		controlhttp.MountNotifyUserRoutes(r, controlhttp.NotifyUserDeps{Service: settings})
		usernoticehttp.MountUserRoutes(r, usernoticehttp.UserDeps{Service: inbox})
	})
	controlhttp.MountNotifyAdminRoutes(r, controlhttp.NotifyAdminDeps{
		Auth:    adminAuth,
		Service: settings,
	})
	usernoticehttp.MountAdminRoutes(r, usernoticehttp.AdminDeps{
		Auth:    adminAuth,
		Service: inbox,
	})
}
