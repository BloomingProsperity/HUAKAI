package main

import (
	"testing"
	"time"

	runtimeconfig "github.com/BloomingProsperity/HUAKAI/internal/config"
	legacydlq "github.com/BloomingProsperity/HUAKAI/internal/dlq"
)

// TestBuildDLQRuntimeRegistersDeclaredKinds 守卫 DLQ 接线完备性:buildDLQRuntime 里
// 无条件注册的 kind 一个都不能缺——缺注册的 kind 在处理时走 ErrNoHandler 直接隔离,
// 记录永久堆积(时效性信号 account_health/metrics 曾因此隔离堆积)。
// 变异:删 lifecycle.go 里任一 Register(如两个时效性信号丢弃 handler)→ 对应断言 RED。
func TestBuildDLQRuntimeRegistersDeclaredKinds(t *testing.T) {
	cfg := &runtimeconfig.ObsDLQConfig{
		BaseBackoff: time.Second,
		CapBackoff:  time.Minute,
		MaxAttempts: 3,
		DLQAfter:    time.Hour,
	}
	_, service, _, _, closeReplica := buildDLQRuntime(nil, cfg, nil, nil)
	if closeReplica != nil {
		defer closeReplica()
	}
	if service == nil {
		t.Fatal("buildDLQRuntime 返回 nil service")
	}
	// 无条件注册的 kind(replica 双 kind 仅在配置 ReplicaDSN 时注册,故不在此断言)。
	for _, kind := range []legacydlq.EventKind{
		legacydlq.EventKindUsageRecord,
		legacydlq.EventKindAuditLedgerEntry,
		legacydlq.EventKindAccountHealth,
		legacydlq.EventKindMetrics,
	} {
		if !service.HasHandler(kind) {
			t.Errorf("kind %q 未注册 handler:该 kind 的事件将被 ErrNoHandler 永久隔离", kind)
		}
	}
}
