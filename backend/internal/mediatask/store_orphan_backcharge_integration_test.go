//go:build integration_pg

package mediatask

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/shopspring/decimal"

	"github.com/BloomingProsperity/HUAKAI/internal/billing"
)

// TestReconcileOrphanBackChargeNoOpWhenHoldReleased 守 S2:hold 已被 Release 后追扣静默
// 失效却把孤儿标 reconciled、漏扣被永久掩盖。
//
// 场景(孤儿对账要追回的真实漏扣):提交任务预扣 1.23(held)→ 模拟 ExpireTask→abortTask→
// billing.Release 把预扣退还客户(held→0, balance 复原, hold.State=released)→ 但上游实际
// 跑完扣了平台账号的钱 → admin 带 back_charge=true 对账。此时 billing.Capture 因 hold 非
// "held" 是 no-op、0 扣。
//
// 修复后正确行为:不假报已扣、不推进 reconciled 终态,孤儿保持 pending(可重试),
// outcome=hold_released_needs_manual_charge,提示该笔已释放预扣只能走人工政策处理。
//
// 变异举证(去掉 captureOrphanHold 里的 HoldCapturable 先验守卫 → 退回直接 Capture+无条件
// 返回 estimated_cents):captured_cents 变 123、advanced 变 true、孤儿被标 reconciled →
// 下面四段断言(advanced=false / captured=0 / outcome / 仍 pending)全部 RED。
func TestReconcileOrphanBackChargeNoOpWhenHoldReleased(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	pool := openMediaPool(t, ctx)
	seed := seedMediaUser(t, ctx, pool, "orphan-holdreleased")
	svc := newIntegrationService(pool)
	store := svc.store.(*PostgresStore)

	// 1) 提交任务:预扣 1.23(held)。
	task, err := svc.Submit(ctx, seed.tenantID, seed.userID, submitInput("req-orphan-released"))
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}

	// 2) 模拟 ExpireTask→abortTask→billing.Release:预扣退还客户,hold.State=released。
	var holdRef string
	if err := pool.QueryRow(ctx, `SELECT hold_ref FROM media_tasks WHERE id=$1`, task.ID).Scan(&holdRef); err != nil {
		t.Fatalf("read hold_ref: %v", err)
	}
	claimID, err := claimIDFromHoldRef(holdRef)
	if err != nil {
		t.Fatalf("claimIDFromHoldRef(%q): %v", holdRef, err)
	}
	releaseHoldInTx(t, ctx, pool, claimID)
	if bal, held := readBalance(t, ctx, pool, seed.tenantID, seed.userID); !bal.Equal(decimal.RequireFromString("10.00")) || !held.Equal(decimal.Zero) {
		t.Fatalf("释放后 balance/held=%s/%s want 10.00/0", bal, held)
	}

	// 3) 持久化孤儿线索。
	if err := store.PersistOrphan(ctx, OrphanRecord{
		TaskID: task.ID, TenantID: seed.tenantID, UserID: seed.userID, Provider: "http",
		ProviderTaskID: "up-orphan-released", LeaseOwner: "it-released-owner", ObservedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("PersistOrphan: %v", err)
	}
	orphanID := mustOrphanID(t, ctx, pool, task.ID, "up-orphan-released")

	// 4) admin 带 back_charge=true 对账:hold 已 released → Capture no-op、0 扣。
	res, advanced, err := store.ReconcileOrphan(ctx, orphanID, "reconciled", true, time.Now().UTC(), nil)
	if err != nil {
		t.Fatalf("ReconcileOrphan err=%v", err)
	}

	// 判别核心①:追扣未真发生,绝不推进 reconciled 终态。
	if advanced {
		t.Fatalf("hold 已 released、追扣未发生,绝不应推进 reconciled(advanced 应为 false)")
	}
	// ②:0 扣,不得假报已扣(否则漏扣被掩盖)。
	if res.CapturedCents != 0 {
		t.Fatalf("hold released 时 0 扣,captured_cents 应为 0,实际 %d(假报已扣=漏扣被静默掩盖)", res.CapturedCents)
	}
	// ③:outcome 明确告知已释放预扣不能靠原 hold 自动追回。
	if res.BackChargeOutcome != "hold_released_needs_manual_charge" {
		t.Fatalf("outcome 应为 hold_released_needs_manual_charge,实际 %q", res.BackChargeOutcome)
	}
	// ④:孤儿必须仍 pending(可重试),不得被标 reconciled 永久掩盖漏扣。
	var status string
	if err := pool.QueryRow(ctx, `SELECT reconcile_status FROM media_task_orphans WHERE id=$1`, orphanID).Scan(&status); err != nil {
		t.Fatalf("read orphan status: %v", err)
	}
	if status != "pending" {
		t.Fatalf("追扣未发生时孤儿必须保持 pending(可重试),实际 %q", status)
	}
	// 余额本就没扣到,必须保持释放后状态。
	if bal, held := readBalance(t, ctx, pool, seed.tenantID, seed.userID); !bal.Equal(decimal.RequireFromString("10.00")) || !held.Equal(decimal.Zero) {
		t.Fatalf("追扣未发生时余额不应变 balance/held=%s/%s want 10.00/0", bal, held)
	}
	if events := countBillingEvents(t, ctx, pool, claimID, ""); events != 0 {
		t.Fatalf("hold 已释放后追扣不得新增 billing event,实际 %d", events)
	}
}

// releaseHoldInTx 在独立事务里对 claimID 的预扣 hold 调 billing.Release(模拟任务过期/中止
// 退还客户),使 hold.State 变 released。
func releaseHoldInTx(t *testing.T, ctx context.Context, pool interface {
	Begin(context.Context) (pgx.Tx, error)
}, claimID int64) {
	t.Helper()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := billing.Release(ctx, tx, claimID); err != nil {
		t.Fatalf("billing.Release: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit release: %v", err)
	}
}

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
	claimID := mustClaimID(t, task.HoldRef)
	assertClaimStatusCost(t, ctx, pool, task.HoldRef, "committed", decimal.RequireFromString("1.23"))
	if events := countBillingEvents(t, ctx, pool, claimID, "claim_committed"); events != 1 {
		t.Fatalf("首次追扣后 claim_committed 事件数=%d want 1", events)
	}
	bal1, held1 := readBalance(t, ctx, pool, seed.tenantID, seed.userID)
	if !bal1.Equal(decimal.RequireFromString("8.77")) || !held1.Equal(decimal.Zero) {
		t.Fatalf("首次追扣后 balance/held=%s/%s want 8.77/0", bal1, held1)
	}
	if rows := attemptClaimSweepForTest(t, ctx, pool, claimID); rows != 0 {
		t.Fatalf("追扣已 committed 的 claim 不应再被 sweep 成 aborted,rows=%d", rows)
	}
	assertClaimStatusCost(t, ctx, pool, task.HoldRef, "committed", decimal.RequireFromString("1.23"))

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
	if events := countBillingEvents(t, ctx, pool, claimID, "claim_committed"); events != 1 {
		t.Fatalf("二次追扣后 claim_committed 事件数=%d want 1(不得重复记账)", events)
	}
}

// TestReconcileOrphanBackChargeClaimConflictDoesNotCapture 守 rows==0 防御:若 claim 已非
// reserving,即使 hold 仍 held,也不得先扣余额再发现账本推进失败。
func TestReconcileOrphanBackChargeClaimConflictDoesNotCapture(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	pool := openMediaPool(t, ctx)
	seed := seedMediaUser(t, ctx, pool, "orphan-claim-conflict")
	svc := newIntegrationService(pool)
	store := svc.store.(*PostgresStore)

	task, err := svc.Submit(ctx, seed.tenantID, seed.userID, submitInput("req-orphan-conflict"))
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	claimID := mustClaimID(t, task.HoldRef)
	if _, err := pool.Exec(ctx, `
		UPDATE billing_ledger_claims
		SET status='aborted', aborted_reason='lease_expired', settled_at=NOW()
		WHERE id=$1 AND tenant_id=$2 AND status='reserving'`,
		claimID, seed.tenantID); err != nil {
		t.Fatalf("force claim abort: %v", err)
	}
	if err := store.PersistOrphan(ctx, OrphanRecord{
		TaskID: task.ID, TenantID: seed.tenantID, UserID: seed.userID, Provider: "http",
		ProviderTaskID: "up-orphan-conflict", LeaseOwner: "it-conflict-owner", ObservedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("PersistOrphan: %v", err)
	}
	orphanID := mustOrphanID(t, ctx, pool, task.ID, "up-orphan-conflict")

	res, advanced, err := store.ReconcileOrphan(ctx, orphanID, "reconciled", true, time.Now().UTC(), nil)
	if err != nil {
		t.Fatalf("ReconcileOrphan: %v", err)
	}
	if advanced {
		t.Fatal("claim 已非 reserving 时不得推进孤儿终态")
	}
	if res.CapturedCents != 0 || res.BackChargeOutcome != "claim_swept_conflict" {
		t.Fatalf("conflict 结果 captured/outcome=%d/%q want 0/claim_swept_conflict", res.CapturedCents, res.BackChargeOutcome)
	}
	bal, held := readBalance(t, ctx, pool, seed.tenantID, seed.userID)
	if !bal.Equal(decimal.RequireFromString("10.00")) || !held.Equal(decimal.RequireFromString("1.23")) {
		t.Fatalf("claim 冲突时不得扣余额 balance/held=%s/%s want 10.00/1.23", bal, held)
	}
	if events := countBillingEvents(t, ctx, pool, claimID, "claim_committed"); events != 0 {
		t.Fatalf("claim 冲突时不得写 committed 事件,实际 %d", events)
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

func countBillingEvents(t *testing.T, ctx context.Context, pool interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, claimID int64, eventType string) int {
	t.Helper()
	var n int
	if eventType == "" {
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM billing_events WHERE claim_id=$1`, claimID).Scan(&n); err != nil {
			t.Fatalf("count billing events: %v", err)
		}
		return n
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM billing_events WHERE claim_id=$1 AND event_type=$2`, claimID, eventType).Scan(&n); err != nil {
		t.Fatalf("count billing events: %v", err)
	}
	return n
}

func attemptClaimSweepForTest(t *testing.T, ctx context.Context, pool interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
}, claimID int64) int64 {
	t.Helper()
	tag, err := pool.Exec(ctx, `
		UPDATE billing_ledger_claims
		SET status='aborted', aborted_reason='lease_expired', settled_at=NOW()
		WHERE id=$1 AND status='reserving'`, claimID)
	if err != nil {
		t.Fatalf("attempt claim sweep: %v", err)
	}
	return tag.RowsAffected()
}
