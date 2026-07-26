// HUAKAI · iKun
//go:build integration_pg

// A#4 判别测试: billing LeaseSweeper 抢先 abort 了 claim 之后, 任务完成/失败路径不得
// 回滚整事务 —— 否则 media_tasks 卡非终态, worker 每 ~30s 重试同一失败死循环。
// 正确行为: 强推终态 (+error_class=claim_swept 可追溯) + 落孤儿对账线索, 跳过 billing 写。

package mediatask

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/BloomingProsperity/HUAKAI/internal/billing"
)

// sweepClaimForTest 模拟 billing LeaseSweeper 的动作: 把任务的 claim 置 aborted
// (与 UpdateClaimAbortedWithReason 同形), 使后续 committed/aborted 条件更新命中 0 行。
func sweepClaimForTest(t *testing.T, ctx context.Context, store *PostgresStore, task Task) {
	t.Helper()
	claimID, err := claimIDFromHoldRef(task.HoldRef)
	if err != nil {
		t.Fatalf("parse hold_ref %q: %v", task.HoldRef, err)
	}
	tag, err := store.pool.Exec(ctx, `
UPDATE billing_ledger_claims
SET status = 'aborted', aborted_reason = 'lease_expired', settled_at = NOW()
WHERE id = $1 AND status = 'reserving'`, claimID)
	if err != nil || tag.RowsAffected() != 1 {
		t.Fatalf("sweep claim %d: rows=%d err=%v", claimID, tag.RowsAffected(), err)
	}
}

func commitClaimForTest(t *testing.T, ctx context.Context, store *PostgresStore, task Task) {
	t.Helper()
	claimID, err := claimIDFromHoldRef(task.HoldRef)
	if err != nil {
		t.Fatalf("解析 claim: %v", err)
	}
	tx, err := store.pool.Begin(ctx)
	if err != nil {
		t.Fatalf("开启预结算事务: %v", err)
	}
	if _, err := billing.Capture(ctx, tx, claimID, decimal.RequireFromString("1.23")); err != nil {
		_ = tx.Rollback(ctx)
		t.Fatalf("模拟 claim 扣款: %v", err)
	}
	tag, err := tx.Exec(ctx, `
UPDATE billing_ledger_claims
SET status='committed', actual_cost=1.23, settled_at=NOW()
WHERE tenant_id=$1 AND id=$2 AND status='reserving'`, task.TenantID, claimID)
	if err != nil || tag.RowsAffected() != 1 {
		_ = tx.Rollback(ctx)
		t.Fatalf("模拟 claim 已结算: rows=%d err=%v", tag.RowsAffected(), err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("提交预结算事务: %v", err)
	}
}

func countSweptOrphans(t *testing.T, ctx context.Context, store *PostgresStore, taskID int64) int64 {
	t.Helper()
	var n int64
	if err := store.pool.QueryRow(ctx,
		`SELECT count(*) FROM media_task_orphans WHERE task_id=$1`, taskID).Scan(&n); err != nil {
		t.Fatalf("count orphans: %v", err)
	}
	return n
}

// TestPG_MediaTaskClaimSweptSuccessForcesTerminalAndOrphan 守成功路:
// 任务上游真跑成功但 claim 已被 sweeper 抢走 → 强推 succeeded (用户拿到产物) +
// 孤儿线索 (admin Manual-First 追扣/核销), 不再卡 in_progress 死循环。
// mutation: CompleteSuccess 的 rows==0 分支退回 `return billing.ErrClaimNotReserving`
// → settled=false + 任务仍 in_progress → 三断言全红。
func TestPG_MediaTaskClaimSweptSuccessForcesTerminalAndOrphan(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	pool := openMediaPool(t, ctx)
	seed := seedMediaUser(t, ctx, pool, "swept-ok")
	svc := newIntegrationService(pool)
	store := NewPostgresStore(pool, PostgresStoreConfig{BillingPolicyVersion: "test-policy", RequestClass: "standard"})

	task := submitAndMarkSubmitted(t, ctx, svc, store, seed, "req-swept-ok")
	// submit 阶段结束会释放租约 (markSubmittedSQL), poll 阶段重新取租。
	task = leaseTaskForTest(t, ctx, store.pool, task.ID, "poll-worker")
	sweepClaimForTest(t, ctx, store, task)

	settled, err := store.CompleteSuccess(ctx, task, "poll-worker",
		PollResult{Status: StatusSucceeded, Progress: 100, Result: json.RawMessage(`{"url":"x"}`), ActualCents: 100},
		time.Now().UTC())
	if err != nil || !settled {
		t.Fatalf("claim 被扫后 CompleteSuccess settled=%v err=%v, want true/nil (回滚=卡死循环)", settled, err)
	}
	got, err := store.GetTask(ctx, task.TenantID, task.UserID, task.ID)
	if err != nil || got.Status != StatusSucceeded {
		t.Fatalf("task status=%v err=%v, want succeeded (卡非终态=每 30s 重试死循环)", got.Status, err)
	}
	if n := countSweptOrphans(t, ctx, store, task.ID); n != 1 {
		t.Fatalf("orphan rows=%d, want 1 (无对账线索=白吃上游成本且不可追)", n)
	}
}

// TestPG_MediaTaskClaimSweptFailureForcesTerminal 守失败/超时路:
// claim 被扫后 CompleteFailure 强推 failed + error_class=claim_swept + 孤儿线索。
// mutation: abortTask 的 rows==0 分支退回 `return billing.ErrClaimNotReserving` → 红。
func TestPG_MediaTaskClaimSweptFailureForcesTerminal(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	pool := openMediaPool(t, ctx)
	seed := seedMediaUser(t, ctx, pool, "swept-fail")
	svc := newIntegrationService(pool)
	store := NewPostgresStore(pool, PostgresStoreConfig{BillingPolicyVersion: "test-policy", RequestClass: "standard"})

	task := submitAndMarkSubmitted(t, ctx, svc, store, seed, "req-swept-fail")
	task = leaseTaskForTest(t, ctx, store.pool, task.ID, "poll-worker")
	sweepClaimForTest(t, ctx, store, task)

	completed, err := store.CompleteFailure(ctx, task, "poll-worker", "provider_failed", time.Now().UTC())
	if err != nil || !completed {
		t.Fatalf("claim 被扫后 CompleteFailure completed=%v err=%v, want true/nil", completed, err)
	}
	got, err := store.GetTask(ctx, task.TenantID, task.UserID, task.ID)
	if err != nil || got.Status != StatusFailed || got.ErrorClass != "claim_swept" {
		t.Fatalf("task status=%v error_class=%q err=%v, want failed/claim_swept", got.Status, got.ErrorClass, err)
	}
	if n := countSweptOrphans(t, ctx, store, task.ID); n != 1 {
		t.Fatalf("orphan rows=%d, want 1", n)
	}
}

// TestPG_MediaTaskCommittedClaimCannotMasqueradeAsRefund 守住失败路的反向合同：
// claim 已完成扣款时，条件 abort 同样会返回 0 行，但不得把它冒充成“清扫器已退款”。
// 变异：删掉 abortTask 对 claim 真实状态的复核，本测试会看到任务错误进入 failed /
// claim_swept，向运营面谎报已退款。
func TestPG_MediaTaskCommittedClaimCannotMasqueradeAsRefund(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	pool := openMediaPool(t, ctx)
	seed := seedMediaUser(t, ctx, pool, "committed-failure")
	svc := newIntegrationService(pool)
	store := NewPostgresStore(pool, PostgresStoreConfig{BillingPolicyVersion: "test-policy", RequestClass: "standard"})

	task := submitAndMarkSubmitted(t, ctx, svc, store, seed, "req-committed-failure")
	task = leaseTaskForTest(t, ctx, store.pool, task.ID, "poll-worker")
	commitClaimForTest(t, ctx, store, task)

	completed, err := store.CompleteFailure(ctx, task, "poll-worker", "provider_failed", time.Now().UTC())
	if !errors.Is(err, billing.ErrClaimNotReserving) || completed {
		t.Fatalf("已结算 claim 的失败处理 completed=%v err=%v，期望 false/ErrClaimNotReserving", completed, err)
	}
	got, err := store.GetTask(ctx, task.TenantID, task.UserID, task.ID)
	if err != nil {
		t.Fatalf("读取任务: %v", err)
	}
	if got.Status != StatusInProgress || got.ErrorClass != "" {
		t.Fatalf("任务 status/error_class=%s/%q，期望保持 in_progress/空", got.Status, got.ErrorClass)
	}
	assertClaimStatusCost(t, ctx, pool, task.HoldRef, "committed", decimal.RequireFromString("1.23"))
	balance, held := readBalance(t, ctx, pool, task.TenantID, task.UserID)
	if !balance.Equal(decimal.RequireFromString("8.77")) || !held.Equal(decimal.Zero) {
		t.Fatalf("余额/预扣=%s/%s，期望保持 8.77/0", balance, held)
	}
	if n := countSweptOrphans(t, ctx, store, task.ID); n != 0 {
		t.Fatalf("已结算冲突不得伪造退款孤儿记录，实际=%d", n)
	}
}

// TestPG_MediaTaskCommittedClaimCannotMasqueradeAsSweptSuccess 守住成功路的同根分支：
// 原子成功结算若看到 claim 已 committed，说明状态合同发生冲突，不得伪装成“预扣已清扫”
// 并创建一条假的待追账记录。
func TestPG_MediaTaskCommittedClaimCannotMasqueradeAsSweptSuccess(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	pool := openMediaPool(t, ctx)
	seed := seedMediaUser(t, ctx, pool, "committed-success")
	svc := newIntegrationService(pool)
	store := NewPostgresStore(pool, PostgresStoreConfig{BillingPolicyVersion: "test-policy", RequestClass: "standard"})

	task := submitAndMarkSubmitted(t, ctx, svc, store, seed, "req-committed-success")
	task = leaseTaskForTest(t, ctx, store.pool, task.ID, "poll-worker")
	commitClaimForTest(t, ctx, store, task)

	settled, err := store.CompleteSuccess(ctx, task, "poll-worker", PollResult{
		Status: StatusSucceeded, Progress: 100, ActualCents: 123, Result: json.RawMessage(`{"url":"x"}`),
	}, time.Now().UTC())
	if !errors.Is(err, billing.ErrClaimNotReserving) || settled {
		t.Fatalf("已结算 claim 的成功处理 settled=%v err=%v，期望 false/ErrClaimNotReserving", settled, err)
	}
	got, err := store.GetTask(ctx, task.TenantID, task.UserID, task.ID)
	if err != nil {
		t.Fatalf("读取任务: %v", err)
	}
	if got.Status != StatusInProgress || len(got.Result) != 0 {
		t.Fatalf("任务 status/result=%s/%s，期望保持 in_progress/空", got.Status, got.Result)
	}
	assertClaimStatusCost(t, ctx, pool, task.HoldRef, "committed", decimal.RequireFromString("1.23"))
	if n := countSweptOrphans(t, ctx, store, task.ID); n != 0 {
		t.Fatalf("已结算冲突不得伪造待追账记录，实际=%d", n)
	}
}
