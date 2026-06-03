package main

import (
	"github.com/BloomingProsperity/HUAKAI/internal/subscription"
	"github.com/BloomingProsperity/HUAKAI/internal/subscriptionhttp"
)

type subscriptionWorkerStatsAdapter struct {
	reminder *subscription.ReminderWorker
	expiry   *subscription.ExpiryWorker
}

func newSubscriptionWorkerStatsReader(
	reminder *subscription.ReminderWorker,
	expiry *subscription.ExpiryWorker,
) subscriptionhttp.WorkerStatsReader {
	if reminder == nil || expiry == nil {
		return nil
	}
	return subscriptionWorkerStatsAdapter{reminder: reminder, expiry: expiry}
}

func (a subscriptionWorkerStatsAdapter) ReadWorkerStats() subscriptionhttp.WorkerStats {
	return subscriptionhttp.WorkerStats{
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
}
