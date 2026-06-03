package main

import (
	"github.com/go-chi/chi/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/subscriptionhttp"
)

func mountNotificationRoutes(r chi.Router, d *deps) {
	var auth subscriptionhttp.AdminAuth
	var reader subscriptionhttp.WorkerStatsReader
	if d != nil {
		auth = d.adminAuth
		reader = newSubscriptionWorkerStatsReader(d.subReminderWorker, d.subExpiryWorker)
	}
	r.Get("/v1/admin/notifications/worker-stats", subscriptionhttp.NewAdminWorkerStatsHandler(subscriptionhttp.AdminWorkerStatsDeps{
		Auth:   auth,
		Reader: reader,
	}))
}
