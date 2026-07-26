package main

import (
	"context"
	"expvar"
	"log/slog"

	dbbilling "github.com/BloomingProsperity/HUAKAI/internal/db/billing"
	"github.com/BloomingProsperity/HUAKAI/internal/subscription"
	"github.com/BloomingProsperity/HUAKAI/internal/subscriptionhttp"
)

var pendingReconciliationUsageRecordsGauge = expvar.NewInt("pending_reconciliation_usage_records")

type pendingReconciliationCounter interface {
	CountPendingReconciliationUsageRecords(context.Context) (int64, error)
}

type subscriptionWorkerStatsAdapter struct {
	reminder                     *subscription.ReminderWorker
	expiry                       *subscription.ExpiryWorker
	autoRenew                    *subscription.AutoRenewWorker // nil = 部署者显式停用或装配失败
	pendingReconciliationCounter pendingReconciliationCounter
}

func newSubscriptionWorkerStatsReader(
	reminder *subscription.ReminderWorker,
	expiry *subscription.ExpiryWorker,
	autoRenew *subscription.AutoRenewWorker,
	pendingCounter pendingReconciliationCounter,
) subscriptionhttp.WorkerStatsReader {
	if reminder == nil || expiry == nil {
		return nil
	}
	return subscriptionWorkerStatsAdapter{reminder: reminder, expiry: expiry, autoRenew: autoRenew, pendingReconciliationCounter: pendingCounter}
}

var _ pendingReconciliationCounter = (*dbbilling.Queries)(nil)

func (a subscriptionWorkerStatsAdapter) ReadWorkerStats(ctx context.Context) subscriptionhttp.WorkerStats {
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
	// autoRenew 正常默认注入；只有部署者显式应急停用或装配失败时为 nil，
	// 此时 Enabled=false 且资金计数保持 0，不能误报 worker 正在运行。
	if a.autoRenew != nil {
		stats.AutoRenew = subscriptionhttp.AutoRenewWorkerStats{
			Enabled:      true,
			TickCount:    a.autoRenew.TickCount(),
			RenewedTotal: a.autoRenew.RenewedTotal(),
			SkippedTotal: a.autoRenew.SkippedTotal(),
			FailedTicks:  a.autoRenew.FailedTicks(),
		}
	}
	if a.pendingReconciliationCounter != nil {
		count, err := a.pendingReconciliationCounter.CountPendingReconciliationUsageRecords(ctx)
		if err != nil {
			stats.PendingReconciliation.QueryFailed = true
			slog.WarnContext(ctx, "读取 pending_reconciliation usage_records 计数失败",
				slog.String("error", err.Error()))
		} else {
			stats.PendingReconciliation.UsageRecords = count
			pendingReconciliationUsageRecordsGauge.Set(count)
		}
	}
	return stats
}
