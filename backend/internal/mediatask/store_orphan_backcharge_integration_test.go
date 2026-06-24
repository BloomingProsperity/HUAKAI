//go:build integration_pg

package mediatask

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"
)

// TestReconcileOrphanBackChargeIdempotent 是防双扣亏钱的判别测试(命门 C)。
//
// 场景:提交一个媒体任务 → 余额预扣 1.23(held=1.23, balance=10);为该任务持久化一条孤儿
// 线索;管理员显式带 back_charge=true 对账两次。正确实现下追扣只发生一次(把 1.23 的预扣
// capture 成真实扣费:held 归 0、balance 减到 8.77),第二次对账因状态门(reconcile_status
// 已 reconciled)直接 no-op,余额一分不动。
//
// 变异举证(逐项,任一去掉都让本测试 RED):
//   - 去掉 ReconcileOrphan 的 reconcile_status='pending' 状态门(允许已 reconciled 再追扣)
//     → 第二次对账会再次进 Capture。虽然 billing.Capture 的 hold.State 门此时仍兜底为 no-op,
//     但 advanced 会返回 true(本应 false),第二次断言 advanced2==false RED;
//   - 同时把 billing.Capture 换成无条件扣款(去掉 hold.State!="held" 守卫)→ balance 被扣两次
//     到 7.54,余额断言 RED。两道闸一起测,任一失效都能抓住双扣。
//   - 把追扣额从 estimated_cents 改成别的口径 → captured_cents / balance 断言 RED。
func TestReconcileOrphanBackChargeIdempotent(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	pool := openMediaPool(t, ctx)
	seed := seedMediaUser(t, ctx, pool, "orphan-backcharge")
	svc := newIntegrationService(pool)
	store := svc.store.(*PostgresStore)

	// 1) 提交任务:预扣 1.23(held)。
	task, err := svc.Submit(ctx, seed.tenantID, seed.userID, submitInput("req-orphan-bc"))
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if _, held := readBalance(t, ctx, pool, seed.tenantID, seed.userID); !held.Equal(decimal.RequireFromString("1.23")) {
		t.Fatalf("提交后 held=%s want 1.23", held)
	}

	// 2) 为该任务持久化孤儿线索。
	if err := store.PersistOrphan(ctx, OrphanRecord{
		TaskID: task.ID, TenantID: seed.tenantID, UserID: seed.userID, Provider: "http",
		ProviderTaskID: "up-orphan-bc", LeaseOwner: "it-bc-owner", ObservedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("PersistOrphan: %v", err)
	}
	orphanID := mustOrphanID(t, ctx, pool, task.ID, "up-orphan-bc")

	// 3) 第一次追扣对账:把预扣 1.23 capture 成真实扣费。
	res1, ok1, err := store.ReconcileOrphan(ctx, orphanID, "reconciled", true, time.Now().UTC(), nil)
	if err != nil || !ok1 {
		t.Fatalf("首次追扣应成功 ok=%v err=%v", ok1, err)
	}
	if res1.CapturedCents != 123 || !res1.BackCharged {
		t.Fatalf("首次追扣结果错 captured=%d backcharged=%v want 123/true", res1.CapturedCents, res1.BackCharged)
	}
	bal1, held1 := readBalance(t, ctx, pool, seed.tenantID, seed.userID)
	if !bal1.Equal(decimal.RequireFromString("8.77")) || !held1.Equal(decimal.Zero) {
		t.Fatalf("首次追扣后 balance/held=%s/%s want 8.77/0", bal1, held1)
	}

	// 4) 第二次追扣对账(同一孤儿):状态门拦截,no-op,余额不动(防双扣命门)。
	res2, ok2, err := store.ReconcileOrphan(ctx, orphanID, "reconciled", true, time.Now().UTC(), nil)
	if err != nil {
		t.Fatalf("二次追扣 err=%v", err)
	}
	if ok2 {
		t.Fatalf("二次追扣应返回 advanced=false(已 reconciled,幂等)")
	}
	if res2.BackCharged || res2.CapturedCents != 0 {
		t.Fatalf("二次追扣不应再扣 captured=%d backcharged=%v", res2.CapturedCents, res2.BackCharged)
	}
	bal2, held2 := readBalance(t, ctx, pool, seed.tenantID, seed.userID)
	if !bal2.Equal(decimal.RequireFromString("8.77")) || !held2.Equal(decimal.Zero) {
		t.Fatalf("二次追扣后 balance/held=%s/%s want 8.77/0(不得双扣)", bal2, held2)
	}
}

// TestReconcileOrphanMarkOnlyDoesNotChargeBalance 证默认动作(仅标记 reconciled、不追扣)
// 绝不动钱:back_charge=false 时余额预扣保持原样(held 不变、balance 不变),只推进状态。
//
// 变异:若把 ReconcileOrphan 在 backCharge=false 时也误调 Capture → held/balance 被改 RED。
func TestReconcileOrphanMarkOnlyDoesNotChargeBalance(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	pool := openMediaPool(t, ctx)
	seed := seedMediaUser(t, ctx, pool, "orphan-markonly")
	svc := newIntegrationService(pool)
	store := svc.store.(*PostgresStore)

	task, err := svc.Submit(ctx, seed.tenantID, seed.userID, submitInput("req-orphan-mark"))
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if err := store.PersistOrphan(ctx, OrphanRecord{
		TaskID: task.ID, TenantID: seed.tenantID, UserID: seed.userID, Provider: "http",
		ProviderTaskID: "up-orphan-mark", LeaseOwner: "it-mark-owner", ObservedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("PersistOrphan: %v", err)
	}
	orphanID := mustOrphanID(t, ctx, pool, task.ID, "up-orphan-mark")

	// 默认动作:仅标记 reconciled,back_charge=false。
	res, ok, err := store.ReconcileOrphan(ctx, orphanID, "reconciled", false, time.Now().UTC(), nil)
	if err != nil || !ok {
		t.Fatalf("仅标记对账应成功 ok=%v err=%v", ok, err)
	}
	if res.BackCharged || res.CapturedCents != 0 {
		t.Fatalf("仅标记不应追扣 captured=%d backcharged=%v", res.CapturedCents, res.BackCharged)
	}
	bal, held := readBalance(t, ctx, pool, seed.tenantID, seed.userID)
	if !bal.Equal(decimal.RequireFromString("10.00")) || !held.Equal(decimal.RequireFromString("1.23")) {
		t.Fatalf("仅标记后 balance/held=%s/%s want 10.00/1.23(预扣保持,不动钱)", bal, held)
	}
}

// TestReconcileOrphanAuditHookRollsBackOnError 证审计 hook 与状态推进 + 追扣同事务原子:
// hook 返回错误时整笔回滚——孤儿仍 pending、余额预扣保持不变。
//
// 变异:若把 audit hook 移出事务(对账先提交再写审计)→ hook 失败后孤儿已 reconciled / 钱已扣,
// 本测试断言 pending + held=1.23 RED。
func TestReconcileOrphanAuditHookRollsBackOnError(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	pool := openMediaPool(t, ctx)
	seed := seedMediaUser(t, ctx, pool, "orphan-auditrollback")
	svc := newIntegrationService(pool)
	store := svc.store.(*PostgresStore)

	task, err := svc.Submit(ctx, seed.tenantID, seed.userID, submitInput("req-orphan-audit"))
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if err := store.PersistOrphan(ctx, OrphanRecord{
		TaskID: task.ID, TenantID: seed.tenantID, UserID: seed.userID, Provider: "http",
		ProviderTaskID: "up-orphan-audit", LeaseOwner: "it-audit-owner", ObservedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("PersistOrphan: %v", err)
	}
	orphanID := mustOrphanID(t, ctx, pool, task.ID, "up-orphan-audit")

	boom := func(ctx context.Context, tx pgx.Tx, _ OrphanReconcileResult) error {
		return context.Canceled // 模拟审计写入失败
	}
	_, ok, err := store.ReconcileOrphan(ctx, orphanID, "reconciled", true, time.Now().UTC(), boom)
	if err == nil || ok {
		t.Fatalf("审计失败应整笔失败回滚 ok=%v err=%v", ok, err)
	}
	// 孤儿仍 pending、预扣保持。
	var status string
	if err := pool.QueryRow(ctx, `SELECT reconcile_status FROM media_task_orphans WHERE id=$1`, orphanID).Scan(&status); err != nil {
		t.Fatalf("read orphan status: %v", err)
	}
	if status != "pending" {
		t.Fatalf("审计回滚后孤儿状态=%q want pending", status)
	}
	bal, held := readBalance(t, ctx, pool, seed.tenantID, seed.userID)
	if !bal.Equal(decimal.RequireFromString("10.00")) || !held.Equal(decimal.RequireFromString("1.23")) {
		t.Fatalf("审计回滚后 balance/held=%s/%s want 10.00/1.23", bal, held)
	}
}

func mustOrphanID(t *testing.T, ctx context.Context, pool interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, taskID int64, providerTaskID string) int64 {
	t.Helper()
	var id int64
	if err := pool.QueryRow(ctx,
		`SELECT id FROM media_task_orphans WHERE task_id=$1 AND provider_task_id=$2`,
		taskID, providerTaskID).Scan(&id); err != nil {
		t.Fatalf("lookup orphan id: %v", err)
	}
	return id
}
