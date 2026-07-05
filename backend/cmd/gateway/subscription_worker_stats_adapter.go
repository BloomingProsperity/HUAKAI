package main

import (
	"github.com/BloomingProsperity/HUAKAI/internal/subscription"
	"github.com/BloomingProsperity/HUAKAI/internal/subscriptionhttp"
)

type subscriptionWorkerStatsAdapter struct {
	reminder  *subscription.ReminderWorker
	expiry    *subscription.ExpiryWorker
	autoRenew *subscription.AutoRenewWorker // nil = worker 未启用(默认关)
}

func newSubscriptionWorkerStatsReader(
	reminder *subscription.ReminderWorker,
	expiry *subscription.ExpiryWorker,
	autoRenew *subscription.AutoRenewWorker,
) subscriptionhttp.WorkerStatsReader {
	if reminder == nil || expiry == nil {
		return nil
	}
	return subscriptionWorkerStatsAdapter{reminder: reminder, expiry: expiry, autoRenew: autoRenew}
}

func (a subscriptionWorkerStatsAdapter) ReadWorkerStats() subscriptionhttp.WorkerStats {
	stats := subscriptionhttp.WorkerStats{
		Reminder: subscriptionhttp.ReminderWorkerStats{
			TickCount:   a.reminder.TickCount(),
			SentTotal:   a.reminder.SentTotal(),
			FailedTicks: a.reminder.FailedTicks(),
		},
		Expiry: subscriptionhttp.ExpiryWorkerStats{
			TickCount:    a.expiry.TickCount(),
			ExpiredTotal: a.expiry.ExpiredTotal(),
			FailedTicks:  a.expiry.FailedTicks(),
		},
	}
	// autoRenew 默认关(nil): Enabled=false, money 计数恒 0; 启用则暴露真实计数。
	if a.autoRenew != nil {
		stats.AutoRenew = subscriptionhttp.AutoRenewWorkerStats{
			Enabled:      true,
			TickCount:    a.autoRenew.TickCount(),
			RenewedTotal: a.autoRenew.RenewedTotal(),
			SkippedTotal: a.autoRenew.SkippedTotal(),
			FailedTicks:  a.autoRenew.FailedTicks(),
		}
	}
	return stats
}
