// HUAKAI · iKun

package main

import (
	"context"
	"errors"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/subscription"
)

type fakePendingReconciliationCounter struct {
	count int64
	err   error
}

func (f fakePendingReconciliationCounter) CountPendingReconciliationUsageRecords(context.Context) (int64, error) {
	return f.count, f.err
}

// TestWorkerStatsAdapterAutoRenewWiring 守 B10: AutoRenewWorker money 指标接进
// worker-stats。autoRenew 注入时 Enabled=true 且计数来自真实 worker; nil(默认关)
// 时 Enabled=false, money 计数恒 0。
// mutation: 适配器不填 stats.AutoRenew → Enabled 恒 false → 第一断言红。
func TestWorkerStatsAdapterAutoRenewWiring(t *testing.T) {
	reminder := subscription.NewReminderWorker(subscription.ReminderWorkerConfig{})
	expiry := subscription.NewExpiryWorker(subscription.ExpiryWorkerConfig{})
	autoRenew := subscription.NewAutoRenewWorker(subscription.AutoRenewWorkerConfig{})

	// 启用: 指标暴露且 Enabled=true。
	reader := newSubscriptionWorkerStatsReader(reminder, expiry, autoRenew, nil)
	if reader == nil {
		t.Fatal("reader 不应为 nil")
	}
	stats := reader.ReadWorkerStats(context.Background())
	if !stats.AutoRenew.Enabled {
		t.Fatal("autoRenew 已注入却 Enabled=false —— money 指标没接上(死指标)")
	}

	// 未启用(nil): Enabled=false, 计数恒 0(不误报运行中)。
	nilReader := newSubscriptionWorkerStatsReader(reminder, expiry, nil, nil)
	nilStats := nilReader.ReadWorkerStats(context.Background())
	if nilStats.AutoRenew.Enabled || nilStats.AutoRenew.TickCount != 0 {
		t.Fatalf("autoRenew=nil 时 stats=%+v, want Enabled=false/0", nilStats.AutoRenew)
	}
}

// TestWorkerStatsAdapterPendingReconciliationWiring 守 C-2 一级:pending_reconciliation
// 未定稿行数必须进入 admin worker-stats 并同步 expvar。判别(§14):若适配器不调用 count 查询、
// 不填响应字段或不刷新 expvar,下面对应断言会转红。
func TestWorkerStatsAdapterPendingReconciliationWiring(t *testing.T) {
	pendingReconciliationUsageRecordsGauge.Set(0)
	reminder := subscription.NewReminderWorker(subscription.ReminderWorkerConfig{})
	expiry := subscription.NewExpiryWorker(subscription.ExpiryWorkerConfig{})
	reader := newSubscriptionWorkerStatsReader(reminder, expiry, nil, fakePendingReconciliationCounter{count: 3})

	stats := reader.ReadWorkerStats(context.Background())

	if stats.PendingReconciliation.UsageRecords != 3 || stats.PendingReconciliation.QueryFailed {
		t.Fatalf("pending_reconciliation stats=%+v want usage_records=3/query_failed=false", stats.PendingReconciliation)
	}
	if got := pendingReconciliationUsageRecordsGauge.Value(); got != 3 {
		t.Fatalf("pending_reconciliation_usage_records expvar=%d want 3", got)
	}
}

func TestWorkerStatsAdapterPendingReconciliationQueryFailure(t *testing.T) {
	reminder := subscription.NewReminderWorker(subscription.ReminderWorkerConfig{})
	expiry := subscription.NewExpiryWorker(subscription.ExpiryWorkerConfig{})
	reader := newSubscriptionWorkerStatsReader(reminder, expiry, nil, fakePendingReconciliationCounter{err: errors.New("db down")})

	stats := reader.ReadWorkerStats(context.Background())

	if !stats.PendingReconciliation.QueryFailed || stats.PendingReconciliation.UsageRecords != 0 {
		t.Fatalf("pending_reconciliation 失败 stats=%+v want query_failed=true/usage_records=0", stats.PendingReconciliation)
	}
}
