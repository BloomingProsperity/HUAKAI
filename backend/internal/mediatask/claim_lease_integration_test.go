//go:build integration_pg

package mediatask

import (
	"context"
	"testing"
	"time"
)

// TestMediaTaskClaimLeaseCoversTaskTimeout 端到端 money 守卫:新建 media task 的
// billing claim 的 lease_expires_at 必须覆盖任务整个生命周期(TaskTimeout),远 >
// 旧的硬编码 90s。否则跑得久的合法任务(视频等)的 claim 会被 billing LeaseSweeper
// 在 90s 后提前误 abort,任务真完成时无法 commit 计费 → 亏钱。
//
// 用一个明显 > 90s 的 TaskTimeout(15min)建 service,这样旧的 90s claim lease 会
// 直接暴露 bug。MUTATION:把 service 的 ClaimLeaseWindow 或 insertReservedTask 的
// claim lease 写回 now+90s,window 变成 ~90s < 10min,本断言转红。
func TestMediaTaskClaimLeaseCoversTaskTimeout(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	pool := openMediaPool(t, ctx)
	seed := seedMediaUser(t, ctx, pool, "claim-lease-window")

	store := NewPostgresStore(pool, PostgresStoreConfig{BillingPolicyVersion: "test-policy", RequestClass: "standard"})
	cfg := integrationConfig()
	cfg.TaskTimeout = 15 * time.Minute
	svc := NewService(store, StaticConfigSource{Config: cfg}, StaticProviderRegistry{"http": NewNoopProvider()})

	task, err := svc.Submit(ctx, seed.tenantID, seed.userID, submitInput("req-claim-lease"))
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}

	var leaseExpires time.Time
	if err := pool.QueryRow(ctx,
		`SELECT lease_expires_at FROM billing_ledger_claims WHERE id=$1`,
		mustClaimID(t, task.HoldRef),
	).Scan(&leaseExpires); err != nil {
		t.Fatalf("query claim lease: %v", err)
	}
	// claim 刚创建,lease_expires_at 应落在 ~20min(TaskTimeout 15min + grace 5min)之后。
	// 旧的硬编码 90s 会让这里只剩 ~90s,远不足覆盖任务生命周期。
	remaining := time.Until(leaseExpires)
	if remaining < 10*time.Minute {
		t.Fatalf("claim lease 距现在=%s 必须覆盖 TaskTimeout(15min)、远 > 90s,否则 LeaseSweeper 提前 abort 亏钱", remaining)
	}
}
