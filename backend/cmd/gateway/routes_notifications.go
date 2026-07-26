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
	var userSettings controlhttp.NotifySettingsService
	var adminSettings controlhttp.NotifyAdminSettingsService
	var inbox usernoticehttp.Service
	var sessions sessionauth.SessionValidator
	var clientIPResolver *clientip.Resolver
	var platformTenantID int64
	if d != nil {
		adminAuth = d.adminAuth
		reader = newSubscriptionWorkerStatsReader(d.subReminderWorker, d.subExpiryWorker, d.subAutoRenewWorker, d.billingQueries)
		userSettings = d.notificationSettings
		adminSettings = d.notificationSettings
		inbox = d.userNoticeService
		sessions = d.userSessions
		clientIPResolver = d.clientIPResolver
		platformTenantID = d.platformTenantID
	}
	r.Get("/v1/admin/notifications/worker-stats", subscriptionhttp.NewAdminWorkerStatsHandler(subscriptionhttp.AdminWorkerStatsDeps{
		Auth:   adminAuth,
		Reader: reader,
	}))
	r.Group(func(r chi.Router) {
		r.Use(sessionauth.SessionMiddleware(sessions, clientIPResolver))
		controlhttp.MountNotifyUserRoutes(r, controlhttp.NotifyUserDeps{Service: userSettings})
		usernoticehttp.MountUserRoutes(r, usernoticehttp.UserDeps{Service: inbox})
	})
	controlhttp.MountNotifyAdminRoutes(r, controlhttp.NotifyAdminDeps{
		Auth:             adminAuth,
		Service:          adminSettings,
		PlatformTenantID: platformTenantID,
	})
	usernoticehttp.MountAdminRoutes(r, usernoticehttp.AdminDeps{
		Auth:             adminAuth,
		Service:          inbox,
		PlatformTenantID: platformTenantID,
	})
}
