// HUAKAI · iKun

package main

import (
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/subscription"
)

// TestWorkerStatsAdapterAutoRenewWiring 守 B10: AutoRenewWorker money 指标接进
// worker-stats。autoRenew 注入时 Enabled=true 且计数来自真实 worker; nil(默认关)
// 时 Enabled=false, money 计数恒 0。
// mutation: 适配器不填 stats.AutoRenew → Enabled 恒 false → 第一断言红。
func TestWorkerStatsAdapterAutoRenewWiring(t *testing.T) {
	reminder := subscription.NewReminderWorker(subscription.ReminderWorkerConfig{})
	expiry := subscription.NewExpiryWorker(subscription.ExpiryWorkerConfig{})
	autoRenew := subscription.NewAutoRenewWorker(subscription.AutoRenewWorkerConfig{})

	// 启用: 指标暴露且 Enabled=true。
	reader := newSubscriptionWorkerStatsReader(reminder, expiry, autoRenew)
	if reader == nil {
		t.Fatal("reader 不应为 nil")
	}
	stats := reader.ReadWorkerStats()
	if !stats.AutoRenew.Enabled {
		t.Fatal("autoRenew 已注入却 Enabled=false —— money 指标没接上(死指标)")
	}

	// 未启用(nil): Enabled=false, 计数恒 0(不误报运行中)。
	nilReader := newSubscriptionWorkerStatsReader(reminder, expiry, nil)
	nilStats := nilReader.ReadWorkerStats()
	if nilStats.AutoRenew.Enabled || nilStats.AutoRenew.TickCount != 0 {
		t.Fatalf("autoRenew=nil 时 stats=%+v, want Enabled=false/0", nilStats.AutoRenew)
	}
}
