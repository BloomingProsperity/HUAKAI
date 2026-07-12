//go:build integration_pg

package mediatask

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// TestOrphanPersistIdempotentAndReconcile 端到端验证孤儿对账台账:幂等持久化、空 ID 跳过、
// 待对账查询、终态推进幂等、非法状态拒绝。用独立 lease_owner marker 隔离 + 收尾清理,不污染 dev DB。
//
// 变异举证(逐项):
//   - 把 insertOrphanSQL 的 ON CONFLICT DO NOTHING 删掉 → 第 1 步幂等断言 RED(重复入账 2 行)。
//   - 把 PersistOrphan 的空 ProviderTaskID 跳过删掉 → 第 2 步断言 RED(空 ID 入账)。
//   - 把 markOrphanReconciledSQL 的 `reconcile_status='pending'` 终态守卫删掉 → 第 4 步二次对账 RED(返回 true)。
//   - 把 MarkOrphanReconciled 的状态白名单删掉 → 第 5 步 RED(非法状态未被拒)。
func TestOrphanPersistIdempotentAndReconcile(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	pool := openMediaPool(t, ctx)
	store := NewPostgresStore(pool, PostgresStoreConfig{BillingPolicyVersion: "test-policy", RequestClass: "standard"})

	const owner = "it-orphan-owner"
	const tenantID = int64(99_000_017)
	taskID := int64(99_000_101)
	cleanup := func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM media_task_orphans WHERE lease_owner=$1`, owner)
	}
	cleanup() // 清残留
	t.Cleanup(cleanup)

	observed := time.Date(2026, 6, 19, 10, 0, 0, 0, time.UTC)
	rec := OrphanRecord{
		TaskID: taskID, TenantID: tenantID, UserID: 42, Provider: "midjourney",
		ProviderTaskID: "up-1", LeaseOwner: owner, ObservedAt: observed,
	}

	// 1) 幂等:同一 (task_id, provider_task_id) 持久化两次,只应留 1 行。
	if err := store.PersistOrphan(ctx, rec); err != nil {
		t.Fatalf("PersistOrphan #1: %v", err)
	}
	if err := store.PersistOrphan(ctx, rec); err != nil {
		t.Fatalf("PersistOrphan #2: %v", err)
	}
	if got := countOrphans(t, ctx, pool, owner); got != 1 {
		t.Fatalf("幂等失败:重复持久化产生 %d 行 want 1", got)
	}

	// 2) 空 ProviderTaskID 无对账价值,跳过不入账。
	if err := store.PersistOrphan(ctx, OrphanRecord{
		TaskID: taskID + 1, TenantID: tenantID, LeaseOwner: owner, ProviderTaskID: "  ", ObservedAt: observed,
	}); err != nil {
		t.Fatalf("PersistOrphan 空 ID: %v", err)
	}
	if got := countOrphans(t, ctx, pool, owner); got != 1 {
		t.Fatalf("空 ProviderTaskID 不应入账,实际 %d 行", got)
	}

	// 3) ListPendingOrphans 按租户能查到刚持久化的孤儿。
	pending, err := store.ListPendingOrphans(ctx, tenantID, 100)
	if err != nil {
		t.Fatalf("ListPendingOrphans: %v", err)
	}
	found := findOrphanByOwner(t, pending, owner)
	if found.ProviderTaskID != "up-1" || found.TaskID != taskID || found.ReconcileStatus != "pending" {
		t.Fatalf("孤儿记录字段错 %+v", found)
	}

	// 4) MarkOrphanReconciled 推进终态:首次 true 且从 pending 消失;二次 false(已终态,幂等)。
	now := time.Now().UTC()
	ok, err := store.MarkOrphanReconciled(ctx, found.ID, "reconciled", now)
	if err != nil || !ok {
		t.Fatalf("首次对账应成功 ok=%v err=%v", ok, err)
	}
	ok2, err := store.MarkOrphanReconciled(ctx, found.ID, "reconciled", now)
	if err != nil {
		t.Fatalf("二次对账 err=%v", err)
	}
	if ok2 {
		t.Fatalf("二次对账应返回 false(已终态)")
	}
	pending2, err := store.ListPendingOrphans(ctx, tenantID, 100)
	if err != nil {
		t.Fatalf("ListPendingOrphans 二次: %v", err)
	}
	for _, r := range pending2 {
		if r.LeaseOwner == owner {
			t.Fatalf("对账后该孤儿仍出现在 pending 列表")
		}
	}

	// 5) 非法状态被拒:pending 自身 + 任意乱码值都必须拒。两个值一起断言,才能抓住"白名单被弱化成
	// 只拒 pending、放过任意非法值"的缺陷(仅测 pending 会被 DB CHECK 兜底掩盖,应用层判别失效)。
	for _, bad := range []string{"pending", "garbage", "DROP"} {
		if _, err := store.MarkOrphanReconciled(ctx, found.ID, bad, now); !errors.Is(err, ErrInvalidOrphanStatus) {
			t.Fatalf("非法状态 %q 应返回 ErrInvalidOrphanStatus,得 %v", bad, err)
		}
	}
}

func countOrphans(t *testing.T, ctx context.Context, pool *pgxpool.Pool, owner string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM media_task_orphans WHERE lease_owner=$1`, owner).Scan(&n); err != nil {
		t.Fatalf("count orphans: %v", err)
	}
	return n
}

func findOrphanByOwner(t *testing.T, recs []OrphanRecord, owner string) OrphanRecord {
	t.Helper()
	for _, r := range recs {
		if r.LeaseOwner == owner {
			return r
		}
	}
	t.Fatalf("待对账列表未含 lease_owner=%s 的孤儿", owner)
	return OrphanRecord{}
}
