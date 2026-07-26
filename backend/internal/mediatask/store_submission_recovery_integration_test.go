//go:build integration_pg

package mediatask

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/billing"
	admindb "github.com/BloomingProsperity/HUAKAI/internal/db/admin"
)

func allowSubmissionRecoveryForTest(context.Context, SubmissionRecoveryResult) error {
	return nil
}

func TestUnknownSubmissionPersistsRecoveryFactAndProtectsExpiredClaim(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	pg := openMediaPool(t, ctx)
	seed := seedMediaUser(t, ctx, pg, "submission-unknown-protection")
	store := newIntegrationService(pg).store.(*PostgresStore)

	task, orphanID := createUnknownSubmissionForTest(
		t, ctx, store, seed, "req-submission-unknown-protection",
	)
	var kind, idempotencyKey, errorClass, reconcileStatus string
	if err := pg.QueryRow(ctx, `
SELECT orphan_kind, idempotency_key, error_class, reconcile_status
FROM media_task_orphans
WHERE id=$1`, orphanID).Scan(&kind, &idempotencyKey, &errorClass, &reconcileStatus); err != nil {
		t.Fatalf("读取提交未知恢复事实: %v", err)
	}
	if kind != "submission_unknown" ||
		idempotencyKey != DeriveIdempotencyKey(task.ID, task.RequestID) ||
		errorClass != "provider_submit_outcome_unknown" ||
		reconcileStatus != "pending" {
		t.Fatalf("恢复事实不完整: kind=%q key=%q class=%q status=%q",
			kind, idempotencyKey, errorClass, reconcileStatus)
	}

	if _, err := pg.Exec(ctx, `
UPDATE billing_ledger_claims
SET lease_expires_at=NOW()-interval '100 years'
WHERE tenant_id=$1 AND id=$2`, seed.tenantID, mustClaimID(t, task.HoldRef)); err != nil {
		t.Fatalf("令 claim 过期: %v", err)
	}
	sweeper := billing.NewLeaseSweeper(pg, billing.NewSettler(pg), 1000)
	processed, err := sweeper.SweepOnce(ctx)
	if err != nil {
		t.Fatalf("清扫未知提交保护场景: %v", err)
	}
	if processed != 0 {
		t.Fatalf("未知提交保护场景清扫了 %d 条记录，期望 0", processed)
	}

	assertClaimStatusCost(t, ctx, pg, task.HoldRef, "reserving", decimal.Zero)
	balance, held := readBalance(t, ctx, pg, seed.tenantID, seed.userID)
	if !balance.Equal(decimal.RequireFromString("10.00")) ||
		!held.Equal(decimal.RequireFromString("1.23")) {
		t.Fatalf("未知结果的预扣被错误释放: balance=%s held=%s", balance, held)
	}
	assertTaskStatus(t, ctx, pg, task.ID, StatusSubmissionUnknown)
}

func TestAttachUnknownSubmissionIsAtomicIdempotentAndRejectsConflictingTaskID(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	pg := openMediaPool(t, ctx)
	seed := seedMediaUser(t, ctx, pg, "submission-attach")
	store := newIntegrationService(pg).store.(*PostgresStore)
	task, orphanID := createUnknownSubmissionForTest(t, ctx, store, seed, "req-submission-attach")

	auditFailure := errors.New("日志写入失败")
	_, _, err := store.AttachUnknownSubmission(
		ctx, orphanID, "provider-task-attach-1", time.Now().UTC(),
		allowSubmissionRecoveryForTest,
		func(context.Context, pgx.Tx, SubmissionRecoveryResult) error {
			return auditFailure
		},
	)
	if !errors.Is(err, auditFailure) {
		t.Fatalf("日志失败应回滚整笔绑定: %v", err)
	}
	assertTaskStatus(t, ctx, pg, task.ID, StatusSubmissionUnknown)
	var status string
	if err := pg.QueryRow(ctx, `SELECT reconcile_status FROM media_task_orphans WHERE id=$1`, orphanID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "pending" {
		t.Fatalf("日志失败后孤儿状态=%q want pending", status)
	}

	result, advanced, err := store.AttachUnknownSubmission(
		ctx, orphanID, "provider-task-attach-1", time.Now().UTC(),
		allowSubmissionRecoveryForTest,
		submissionAuditForTest("orphan_provider_task_attached"),
	)
	if err != nil || !advanced {
		t.Fatalf("首次补录 result=%+v advanced=%v err=%v", result, advanced, err)
	}
	if result.TaskStatus != StatusInProgress || result.OrphanStatus != "reconciled" {
		t.Fatalf("首次补录状态错误: %+v", result)
	}
	assertAdminRecoveryActionCount(t, ctx, pg, orphanID, "orphan_provider_task_attached", 1)
	accessDenied := errors.New("恢复作用域拒绝")
	_, advanced, err = store.AttachUnknownSubmission(
		ctx, orphanID, "provider-task-attach-1", time.Now().UTC(),
		func(context.Context, SubmissionRecoveryResult) error { return accessDenied }, nil,
	)
	if !errors.Is(err, accessDenied) || advanced {
		t.Fatalf("幂等补录必须先鉴权: advanced=%v err=%v", advanced, err)
	}
	result, advanced, err = store.AttachUnknownSubmission(
		ctx, orphanID, "provider-task-attach-1", time.Now().UTC(),
		allowSubmissionRecoveryForTest, nil,
	)
	if err != nil || advanced {
		t.Fatalf("同一补录重放应幂等: result=%+v advanced=%v err=%v", result, advanced, err)
	}

	second, secondOrphanID := createUnknownSubmissionForTest(
		t, ctx, store, seed, "req-submission-attach-conflict",
	)
	_, advanced, err = store.AttachUnknownSubmission(
		ctx, secondOrphanID, "provider-task-attach-1", time.Now().UTC(),
		allowSubmissionRecoveryForTest, nil,
	)
	if !errors.Is(err, ErrProviderTaskIDConflict) || advanced {
		t.Fatalf("另一任务复用同一上游 ID 应显式冲突: advanced=%v err=%v", advanced, err)
	}
	assertTaskStatus(t, ctx, pg, second.ID, StatusSubmissionUnknown)
	assertClaimStatusCost(t, ctx, pg, task.HoldRef, "reserving", decimal.Zero)
	assertClaimStatusCost(t, ctx, pg, second.HoldRef, "reserving", decimal.Zero)
}

func TestSubmissionRecoveryAuthorizesBeforeEveryStateBranch(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	pg := openMediaPool(t, ctx)
	seed := seedMediaUser(t, ctx, pg, "submission-access-order")
	store := newIntegrationService(pg).store.(*PostgresStore)
	accessDenied := errors.New("恢复作用域拒绝")
	denyForeignTenant := func(_ context.Context, result SubmissionRecoveryResult) error {
		if result.TenantID != seed.tenantID {
			t.Fatalf("鉴权收到租户=%d want %d", result.TenantID, seed.tenantID)
		}
		return accessDenied
	}
	assertDenied := func(advanced bool, err error) {
		t.Helper()
		if !errors.Is(err, accessDenied) || advanced {
			t.Fatalf("跨租户恢复必须先拒绝: advanced=%v err=%v", advanced, err)
		}
	}

	t.Run("错误孤儿类型", func(t *testing.T) {
		_, orphanID := createUnknownSubmissionForTest(
			t, ctx, store, seed, "req-submission-access-wrong-kind",
		)
		if _, err := pg.Exec(ctx, `
UPDATE media_task_orphans
SET orphan_kind='provider_task_orphan', provider_task_id='provider-wrong-kind'
WHERE id=$1`, orphanID); err != nil {
			t.Fatalf("构造错误孤儿类型: %v", err)
		}
		_, advanced, err := store.AttachUnknownSubmission(
			ctx, orphanID, "provider-new", time.Now().UTC(), denyForeignTenant, nil,
		)
		assertDenied(advanced, err)
	})

	t.Run("上游任务号冲突", func(t *testing.T) {
		_, ownerOrphanID := createUnknownSubmissionForTest(
			t, ctx, store, seed, "req-submission-access-conflict-owner",
		)
		if _, advanced, err := store.AttachUnknownSubmission(
			ctx, ownerOrphanID, "provider-conflict", time.Now().UTC(),
			allowSubmissionRecoveryForTest, nil,
		); err != nil || !advanced {
			t.Fatalf("构造上游任务号占用: advanced=%v err=%v", advanced, err)
		}
		_, targetOrphanID := createUnknownSubmissionForTest(
			t, ctx, store, seed, "req-submission-access-conflict-target",
		)
		_, advanced, err := store.AttachUnknownSubmission(
			ctx, targetOrphanID, "provider-conflict", time.Now().UTC(),
			denyForeignTenant, nil,
		)
		assertDenied(advanced, err)
	})

	t.Run("非待处理状态", func(t *testing.T) {
		_, orphanID := createUnknownSubmissionForTest(
			t, ctx, store, seed, "req-submission-access-non-pending",
		)
		if _, err := pg.Exec(ctx, `
UPDATE media_task_orphans
SET reconcile_status='ignored'
WHERE id=$1`, orphanID); err != nil {
			t.Fatalf("构造非待处理状态: %v", err)
		}
		_, advanced, err := store.RequestUnknownSubmissionRelease(
			ctx, orphanID, time.Now().UTC(), denyForeignTenant, nil,
		)
		assertDenied(advanced, err)
	})

	t.Run("幂等终态", func(t *testing.T) {
		_, orphanID := createUnknownSubmissionForTest(
			t, ctx, store, seed, "req-submission-access-terminal",
		)
		if _, advanced, err := store.AttachUnknownSubmission(
			ctx, orphanID, "provider-terminal", time.Now().UTC(),
			allowSubmissionRecoveryForTest, nil,
		); err != nil || !advanced {
			t.Fatalf("构造幂等终态: advanced=%v err=%v", advanced, err)
		}
		_, advanced, err := store.AttachUnknownSubmission(
			ctx, orphanID, "provider-terminal", time.Now().UTC(),
			denyForeignTenant, nil,
		)
		assertDenied(advanced, err)
	})
}

func TestKnownProviderEvidenceMustConvergeOrBlockRelease(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	pg := openMediaPool(t, ctx)
	seed := seedMediaUser(t, ctx, pg, "submission-known-evidence")
	store := newIntegrationService(pg).store.(*PostgresStore)

	matching, matchingUnknownID := createUnknownSubmissionForTest(
		t, ctx, store, seed, "req-submission-known-matching",
	)
	if err := store.PersistOrphan(ctx, OrphanRecord{
		TaskID: matching.ID, TenantID: matching.TenantID, UserID: matching.UserID,
		Provider: matching.Provider, ProviderTaskID: "provider-known-matching",
		LeaseOwner: "lost-worker", ObservedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("写入同任务已知上游证据: %v", err)
	}
	_, advanced, err := store.AttachUnknownSubmission(
		ctx, matchingUnknownID, "provider-known-matching", time.Now().UTC(),
		allowSubmissionRecoveryForTest, nil,
	)
	if err != nil || !advanced {
		t.Fatalf("相同上游 ID 应收敛全部线索: advanced=%v err=%v", advanced, err)
	}
	var pending int
	if err := pg.QueryRow(ctx, `
SELECT count(*)
FROM media_task_orphans
WHERE task_id=$1 AND reconcile_status='pending'`, matching.ID).Scan(&pending); err != nil {
		t.Fatalf("统计同任务残留线索: %v", err)
	}
	if pending != 0 {
		t.Fatalf("相同上游 ID 补录后仍有 %d 条 pending 线索", pending)
	}

	conflicting, conflictingUnknownID := createUnknownSubmissionForTest(
		t, ctx, store, seed, "req-submission-known-conflict",
	)
	if err := store.PersistOrphan(ctx, OrphanRecord{
		TaskID: conflicting.ID, TenantID: conflicting.TenantID, UserID: conflicting.UserID,
		Provider: conflicting.Provider, ProviderTaskID: "provider-known-a",
		LeaseOwner: "lost-worker", ObservedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("写入冲突上游证据: %v", err)
	}
	_, advanced, err = store.AttachUnknownSubmission(
		ctx, conflictingUnknownID, "provider-known-b", time.Now().UTC(),
		allowSubmissionRecoveryForTest, nil,
	)
	if !errors.Is(err, ErrProviderTaskIDConflict) || advanced {
		t.Fatalf("同任务不同上游 ID 必须显式冲突: advanced=%v err=%v", advanced, err)
	}
	assertTaskStatus(t, ctx, pg, conflicting.ID, StatusSubmissionUnknown)

	releasing, releasingUnknownID := createUnknownSubmissionForTest(
		t, ctx, store, seed, "req-submission-known-release",
	)
	if err := store.PersistOrphan(ctx, OrphanRecord{
		TaskID: releasing.ID, TenantID: releasing.TenantID, UserID: releasing.UserID,
		Provider: releasing.Provider, ProviderTaskID: "provider-known-release",
		LeaseOwner: "lost-worker", ObservedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("写入退款前上游证据: %v", err)
	}
	_, advanced, err = store.RequestUnknownSubmissionRelease(
		ctx, releasingUnknownID, time.Now().UTC(),
		allowSubmissionRecoveryForTest, nil,
	)
	if !errors.Is(err, ErrProviderTaskIDConflict) || advanced {
		t.Fatalf("已有上游任务号时不得确认未受理退款: advanced=%v err=%v", advanced, err)
	}
	assertTaskStatus(t, ctx, pg, releasing.ID, StatusSubmissionUnknown)
	assertClaimStatusCost(t, ctx, pg, releasing.HoldRef, "reserving", decimal.Zero)
}

func TestConfirmedNotAcceptedReleasesThroughWorkerExactlyOnce(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	pg := openMediaPool(t, ctx)
	seed := seedMediaUser(t, ctx, pg, "submission-release")
	store := newIntegrationService(pg).store.(*PostgresStore)
	task, orphanID := createUnknownSubmissionForTest(t, ctx, store, seed, "req-submission-release")

	result, advanced, err := store.RequestUnknownSubmissionRelease(
		ctx, orphanID, time.Now().UTC(),
		allowSubmissionRecoveryForTest,
		submissionAuditForTest("orphan_release_requested"),
	)
	if err != nil || !advanced ||
		result.TaskStatus != StatusSubmissionReleasing ||
		result.OrphanStatus != "release_requested" {
		t.Fatalf("请求释放 result=%+v advanced=%v err=%v", result, advanced, err)
	}
	assertAdminRecoveryActionCount(t, ctx, pg, orphanID, "orphan_release_requested", 1)
	accessDenied := errors.New("恢复作用域拒绝")
	_, advanced, err = store.RequestUnknownSubmissionRelease(
		ctx, orphanID, time.Now().UTC(),
		func(context.Context, SubmissionRecoveryResult) error { return accessDenied }, nil,
	)
	if !errors.Is(err, accessDenied) || advanced {
		t.Fatalf("幂等释放必须先鉴权: advanced=%v err=%v", advanced, err)
	}
	_, advanced, err = store.RequestUnknownSubmissionRelease(
		ctx, orphanID, time.Now().UTC(),
		allowSubmissionRecoveryForTest, nil,
	)
	if err != nil || advanced {
		t.Fatalf("重复释放请求应幂等: advanced=%v err=%v", advanced, err)
	}
	assertClaimStatusCost(t, ctx, pg, task.HoldRef, "reserving", decimal.Zero)
	_, held := readBalance(t, ctx, pg, seed.tenantID, seed.userID)
	if !held.Equal(decimal.RequireFromString("1.23")) {
		t.Fatalf("worker 处理前预扣不应提前释放: held=%s", held)
	}

	worker := NewWorker(
		store,
		StaticConfigSource{Config: integrationConfig()},
		StaticProviderRegistry{"http": NewNoopProvider()},
		WorkerOptions{Owner: "submission-release-worker"},
	)
	processed, err := worker.RunOnce(ctx)
	if err != nil || !processed {
		t.Fatalf("释放 worker processed=%v err=%v", processed, err)
	}
	assertTaskStatus(t, ctx, pg, task.ID, StatusFailed)
	assertClaimStatusCost(t, ctx, pg, task.HoldRef, "aborted", decimal.Zero)
	balance, held := readBalance(t, ctx, pg, seed.tenantID, seed.userID)
	if !balance.Equal(decimal.RequireFromString("10.00")) || !held.IsZero() {
		t.Fatalf("确认未受理后的余额未收敛: balance=%s held=%s", balance, held)
	}
	var orphanStatus string
	if err := pg.QueryRow(ctx,
		`SELECT reconcile_status FROM media_task_orphans WHERE id=$1`,
		orphanID,
	).Scan(&orphanStatus); err != nil {
		t.Fatal(err)
	}
	if orphanStatus != "cancelled" {
		t.Fatalf("退款完成后孤儿状态=%q want cancelled", orphanStatus)
	}

	processed, err = worker.RunOnce(ctx)
	if err != nil || processed {
		t.Fatalf("终态第二轮不应重复释放: processed=%v err=%v", processed, err)
	}
	var abortedEvents int
	if err := pg.QueryRow(ctx, `
SELECT count(*)
FROM billing_events
WHERE tenant_id=$1 AND claim_id=$2 AND event_type='claim_aborted'`,
		seed.tenantID, mustClaimID(t, task.HoldRef),
	).Scan(&abortedEvents); err != nil {
		t.Fatal(err)
	}
	if abortedEvents != 1 {
		t.Fatalf("claim_aborted 事件=%d want 1", abortedEvents)
	}
}

func createUnknownSubmissionForTest(
	t *testing.T,
	ctx context.Context,
	store *PostgresStore,
	seed mediaSeed,
	requestID string,
) (Task, int64) {
	t.Helper()
	svc := NewService(
		store,
		StaticConfigSource{Config: integrationConfig()},
		StaticProviderRegistry{"http": NewNoopProvider()},
	)
	task, err := svc.Submit(ctx, seed.tenantID, seed.userID, submitInput(requestID))
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	leased := leaseTaskForTest(t, ctx, store.pool, task.ID, "unknown-submit-worker-"+requestID)
	leased, err = store.MarkSubmitting(
		ctx, leased, "unknown-submit-worker-"+requestID, time.Now().UTC(),
	)
	if err != nil {
		t.Fatalf("MarkSubmitting: %v", err)
	}
	task, err = store.MarkSubmissionUnknown(
		ctx,
		leased,
		"unknown-submit-worker-"+requestID,
		"provider_submit_outcome_unknown",
		DeriveIdempotencyKey(task.ID, task.RequestID),
		time.Now().UTC(),
	)
	if err != nil {
		t.Fatalf("MarkSubmissionUnknown: %v", err)
	}
	var orphanID int64
	if err := store.pool.QueryRow(ctx, `
SELECT id
FROM media_task_orphans
WHERE task_id=$1 AND orphan_kind='submission_unknown'`, task.ID).Scan(&orphanID); err != nil {
		t.Fatalf("读取未知提交孤儿: %v", err)
	}
	return task, orphanID
}

func submissionAuditForTest(action string) SubmissionRecoveryAuditHook {
	return func(ctx context.Context, tx pgx.Tx, result SubmissionRecoveryResult) error {
		tenantID := result.TenantID
		targetID := result.OrphanID
		_, err := admindb.New(tx).InsertAdminAuditEvent(ctx, admindb.InsertAdminAuditEventParams{
			TenantID: &tenantID, ActorID: "integration-test",
			ActorRole: admin.RoleTenantOperator, Action: action,
			TargetType: "media_task_orphan", TargetID: &targetID,
			Payload: []byte(`{}`),
		})
		return err
	}
}

func assertAdminRecoveryActionCount(
	t *testing.T,
	ctx context.Context,
	pg *pgxpool.Pool,
	orphanID int64,
	action string,
	want int,
) {
	t.Helper()
	var count int
	if err := pg.QueryRow(ctx, `
SELECT count(*)
FROM admin_audit_events
WHERE action=$1 AND target_type='media_task_orphan' AND target_id=$2`,
		action, orphanID,
	).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != want {
		t.Fatalf("管理员恢复日志 action=%q count=%d want %d", action, count, want)
	}
}
