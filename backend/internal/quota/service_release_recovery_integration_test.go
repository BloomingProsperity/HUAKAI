//go:build integration_pg

package quota

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"
)

// AT-CD2-002:审计写点前五次抛出真实 SQLSTATE 40001 时，每笔失败事务必须全回滚，第六笔原子释放。
func TestServiceRelease_ATCD2002_RealPGSixthTransactionCommits(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	pool := openQuotaIntegrationPool(t, ctx)
	f := newQuotaFixture(t, ctx, pool)
	store := NewPostgresStore(pool)
	service := NewService(store)
	seed := seedReleaseRecoveryFixture(t, ctx, f, service, "cd2-real-pg-sixth")
	fault := installReleaseAuditConflictFault(t, ctx, f, 5)

	result, err := service.Release(ctx, ReleaseRequest{
		TenantID: f.tenantID,
		ClaimID:  seed.reserve.Reservation.ClaimID,
		Reason:   "abort",
	})
	if err != nil {
		t.Fatalf("Release: %v", err)
	}
	if result.Reservation.Status != ReservationReleased || fault.attempts() != 6 {
		t.Fatalf("status=%s sequence=%d; want released/6", result.Reservation.Status, fault.attempts())
	}
	assertReleasedQuotaFixture(t, f, seed)
	if got := f.auditSemanticCount("release_aborted"); got != 1 {
		t.Fatalf("release audit count=%d; want 1", got)
	}

	second, err := service.Release(ctx, ReleaseRequest{
		TenantID: f.tenantID,
		ClaimID:  seed.reserve.Reservation.ClaimID,
		Reason:   "abort",
	})
	if err != nil {
		t.Fatalf("second Release: %v", err)
	}
	if !second.IdempotencyHit || fault.attempts() != 6 {
		t.Fatalf("second idempotency=%v sequence=%d; want true/6", second.IdempotencyHit, fault.attempts())
	}
	if got := f.auditSemanticCount("release_aborted"); got != 1 {
		t.Fatalf("release audit count after replay=%d; want 1", got)
	}
}

// TestPrepareQuotaReleaseRecovery_ATCD2008_RevivedClaimIsNoOp 固定旧 Abort
// 的 prepare 到达前 claim 已复活的交错；删掉 billing 终态守卫会污染活预留。
func TestPrepareQuotaReleaseRecovery_ATCD2008_RevivedClaimIsNoOp(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	pool := openQuotaIntegrationPool(t, ctx)
	f := newQuotaFixture(t, ctx, pool)
	store := NewPostgresStore(pool)
	service := NewService(store)
	now := time.Now().UTC().Truncate(time.Second)
	reserve := f.reserveForSettlement(ctx, service, now, "cd2-prepare-attempt-aba", "4", false)
	f.setClaimTerminal(reserve.Reservation.ClaimID, claimStatusAborted, "")
	if got := f.reviveClaimForNewAttempt(reserve.Reservation.ClaimID, now.Add(10*time.Minute)); got != 2 {
		t.Fatalf("复活 attempt_seq=%d，want 2", got)
	}

	reused, err := service.Reserve(ctx, ReserveRequest{
		TenantID:           f.tenantID,
		ClaimID:            reserve.Reservation.ClaimID,
		RequestFingerprint: reserve.Reservation.RequestFingerprint,
		Scopes:             reserve.Reservation.Scopes,
		PredictedCost:      reserve.Reservation.PredictedCost,
		LeaseExpiresAt:     now.Add(10 * time.Minute),
		At:                 now,
	})
	if err != nil || !reused.Allowed || !reused.IdempotencyHit || reused.Reservation.ID != reserve.Reservation.ID {
		t.Fatalf("新 attempt 复用 err/result=%v/%+v，want 同一活预留", err, reused)
	}
	beforeStatus, beforeLease, _ := releaseRecoveryState(t, f, reserve.Reservation.ID)

	gotID, err := store.PrepareReleaseRecovery(ctx, f.tenantID, reserve.Reservation.ClaimID, 0)
	if !errors.Is(err, pgx.ErrNoRows) || gotID != 0 {
		t.Fatalf("PrepareReleaseRecovery id/err=%d/%v，want 0/pgx.ErrNoRows", gotID, err)
	}
	afterStatus, afterLease, _ := releaseRecoveryState(t, f, reserve.Reservation.ID)
	if beforeStatus != ReservationReserved || afterStatus != ReservationReserved || !afterLease.Equal(beforeLease) {
		t.Fatalf("status/lease before=%s/%s after=%s/%s，want 活预留不变", beforeStatus, beforeLease, afterStatus, afterLease)
	}
}

// AT-CD2-004:恢复准备成功但入队失败时，提前到期的 reservation 必须在至多两轮 worker 内由 stale 段释放。
func TestServiceRelease_ATCD2004_QueueFailureFallsBackToStaleSweep(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	pool := openQuotaIntegrationPool(t, ctx)
	f := newQuotaFixture(t, ctx, pool)
	store := NewPostgresStore(pool)
	service := NewService(store)
	drainReconcilerBacklog(t, ctx, service, store)
	seed := seedReleaseRecoveryFixture(t, ctx, f, service, "cd2-stale-only")
	f.setClaimTerminal(seed.reserve.Reservation.ClaimID, "aborted", "")
	releaseFault := installReleaseAuditConflictFault(t, ctx, f, 100)
	queueFaultDrop := installReleaseQueueFailureFault(t, ctx, f)

	result, err := service.Release(ctx, ReleaseRequest{
		TenantID:      f.tenantID,
		ClaimID:       seed.reserve.Reservation.ClaimID,
		ReservationID: 0,
		Reason:        "abort",
	})
	if err == nil || !IsRetryable(err) {
		t.Fatalf("Release err=%v; want retryable primary failure", err)
	}
	if result.ReconciliationQueued {
		t.Fatal("ReconciliationQueued=true; want false when enqueue is forced to fail")
	}
	status, lease, dbNow := releaseRecoveryState(t, f, seed.reserve.Reservation.ID)
	if status != ReservationReconciliationNeeded || lease.After(dbNow) {
		t.Fatalf("prepared status=%s lease=%s DB now=%s; want reconciliation_needed and lease<=now", status, lease, dbNow)
	}
	if got := releaseJobCountForClaim(t, f, seed.reserve.Reservation.ClaimID); got != 0 {
		t.Fatalf("job count=%d; want 0 after forced enqueue rollback", got)
	}

	releaseFault.drop()
	queueFaultDrop()
	worker := NewReconciliationWorker(NewReconciler(service, store, ReconcilerOptions{Limit: 10}), time.Minute)
	processed := 0
	for round := 0; round < 2 && f.reservationStatus(seed.reserve.Reservation.ID) != ReservationReleased; round++ {
		n, runErr := worker.RunOnce(ctx, time.Now().UTC().Add(time.Duration(round+1)*time.Second))
		if runErr != nil {
			t.Fatalf("RunOnce round %d: %v", round+1, runErr)
		}
		processed += n
	}
	if processed != 1 || f.reservationStatus(seed.reserve.Reservation.ID) != ReservationReleased {
		t.Fatalf("processed=%d status=%s; want one recovery and released within two rounds", processed, f.reservationStatus(seed.reserve.Reservation.ID))
	}
	assertReleasedQuotaFixture(t, f, seed)
	if got := f.auditSemanticCount("release_aborted"); got != 1 {
		t.Fatalf("release audit count=%d; want 1", got)
	}
}

// AT-CD2-005:恢复任务成功入队后，下一轮 job 段必须完成释放，stale 段不得重复施加业务效果。
func TestServiceRelease_ATCD2005_QueuedHandoffReplaysExactlyOnce(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	pool := openQuotaIntegrationPool(t, ctx)
	f := newQuotaFixture(t, ctx, pool)
	store := NewPostgresStore(pool)
	service := NewService(store)
	drainReconcilerBacklog(t, ctx, service, store)
	seed := seedReleaseRecoveryFixture(t, ctx, f, service, "cd2-job-replay")
	f.setClaimTerminal(seed.reserve.Reservation.ClaimID, "aborted", "")
	releaseFault := installReleaseAuditConflictFault(t, ctx, f, 100)

	result, err := service.Release(ctx, ReleaseRequest{
		TenantID:      f.tenantID,
		ClaimID:       seed.reserve.Reservation.ClaimID,
		ReservationID: 0,
		Reason:        "abort",
	})
	if err == nil || !IsRetryable(err) {
		t.Fatalf("Release err=%v; want retryable primary failure", err)
	}
	if !result.ReconciliationQueued {
		t.Fatal("ReconciliationQueued=false; want true")
	}
	job := releaseJobForClaim(t, f, seed.reserve.Reservation.ClaimID)
	if job.reservationID == nil || *job.reservationID != seed.reserve.Reservation.ID || job.status != "queued" {
		t.Fatalf("job=%+v; want queued with real reservation id %d", job, seed.reserve.Reservation.ID)
	}
	if job.nextRunAt.After(job.dbNow) {
		t.Fatalf("job next_run_at=%s DB now=%s; want due immediately", job.nextRunAt, job.dbNow)
	}

	releaseFault.drop()
	worker := NewReconciliationWorker(NewReconciler(service, store, ReconcilerOptions{Limit: 10}), time.Minute)
	processed, runErr := worker.RunOnce(ctx, time.Now().UTC().Add(time.Second))
	if runErr != nil {
		t.Fatalf("RunOnce: %v", runErr)
	}
	if processed != 1 {
		t.Fatalf("processed=%d; want exactly one job replay", processed)
	}
	if state := f.reconcilerJobState(job.id); state.status != "succeeded" {
		t.Fatalf("job status=%s; want succeeded", state.status)
	}
	assertReleasedQuotaFixture(t, f, seed)
	if got := f.auditSemanticCount("release_aborted"); got != 1 {
		t.Fatalf("release audit count=%d; want exactly 1 after job plus stale segments", got)
	}
}

// AT-CD2-007:提前 lease 不得绕过未决 post-delivery 保护；恢复交付完成后才允许 stale 段释放。
func TestServiceRelease_ATCD2007_EarlyLeaseHonorsPendingPostDelivery(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	pool := openQuotaIntegrationPool(t, ctx)
	f := newQuotaFixture(t, ctx, pool)
	store := NewPostgresStore(pool)
	service := NewService(store)
	drainReconcilerBacklog(t, ctx, service, store)
	seed := seedReleaseRecoveryFixture(t, ctx, f, service, "cd2-post-delivery")
	f.setClaimTerminal(seed.reserve.Reservation.ClaimID, "aborted", "")
	f.seedPendingPostDeliverySettlement(seed.reserve.Reservation.ClaimID)

	realID, err := store.PrepareReleaseRecovery(ctx, f.tenantID, seed.reserve.Reservation.ClaimID, 0)
	if err != nil {
		t.Fatalf("PrepareReleaseRecovery: %v", err)
	}
	if realID != seed.reserve.Reservation.ID {
		t.Fatalf("real reservation id=%d; want %d", realID, seed.reserve.Reservation.ID)
	}
	worker := NewReconciliationWorker(NewReconciler(service, store, ReconcilerOptions{Limit: 10}), time.Minute)
	processed, err := worker.RunOnce(ctx, time.Now().UTC().Add(time.Second))
	if err != nil {
		t.Fatalf("RunOnce while pending: %v", err)
	}
	if processed != 0 || f.reservationStatus(seed.reserve.Reservation.ID) != ReservationReconciliationNeeded {
		t.Fatalf("pending processed=%d status=%s; want 0/reconciliation_needed", processed, f.reservationStatus(seed.reserve.Reservation.ID))
	}
	assertHeldQuotaFixture(t, f, seed)

	if _, err := f.pool.Exec(ctx,
		`UPDATE usage_record_dlq SET status='delivered' WHERE tenant_id=$1 AND claim_id=$2 AND event_kind='post_delivery_settlement'`,
		f.tenantID, seed.reserve.Reservation.ClaimID,
	); err != nil {
		t.Fatalf("mark post-delivery recovery delivered: %v", err)
	}
	processed, err = worker.RunOnce(ctx, time.Now().UTC().Add(2*time.Second))
	if err != nil {
		t.Fatalf("RunOnce after delivered: %v", err)
	}
	if processed != 1 || f.reservationStatus(seed.reserve.Reservation.ID) != ReservationReleased {
		t.Fatalf("delivered processed=%d status=%s; want 1/released", processed, f.reservationStatus(seed.reserve.Reservation.ID))
	}
	assertReleasedQuotaFixture(t, f, seed)
}

// drainReconcilerBacklog 在动手前把共享测试库里历史遗留的补偿 job 与过期孤儿预留排干:
// RunOnce 的 processed 是全局计数,其他测试/会话(如重并发 E2E 的容忍性悬挂)留下的残渣
// 会让本文件「processed 恰 N」的精确断言假红。排干到 0 后精确计数才有判别意义。
func drainReconcilerBacklog(t *testing.T, ctx context.Context, service *Service, store *PostgresStore) {
	t.Helper()
	worker := NewReconciliationWorker(NewReconciler(service, store, ReconcilerOptions{Limit: 100}), time.Minute)
	for i := 0; i < 10; i++ {
		n, err := worker.RunOnce(ctx, time.Now().UTC().Add(5*time.Second))
		if err != nil {
			t.Fatalf("drain backlog round %d: %v", i+1, err)
		}
		if n == 0 {
			return
		}
	}
	t.Fatal("drain backlog: residual work after 10 rounds; shared test DB unhealthy")
}

type releaseRecoveryFixture struct {
	reserve       ReserveResult
	requestPolicy int64
	costPolicy    int64
	userScopeID   string
	at            time.Time
}

func (f *quotaFixture) reviveClaimForNewAttempt(claimID int64, leaseExpiresAt time.Time) int32 {
	f.t.Helper()
	var attemptSeq int32
	if err := f.pool.QueryRow(f.ctx,
		`UPDATE billing_ledger_claims
		 SET status='reserving', aborted_reason=NULL, settled_at=NULL,
		     attempt_seq=attempt_seq+1, lease_expires_at=$3, reserved_at=clock_timestamp()
		 WHERE tenant_id=$1 AND id=$2 AND status='aborted'
		 RETURNING attempt_seq`,
		f.tenantID, claimID, leaseExpiresAt,
	).Scan(&attemptSeq); err != nil {
		f.t.Fatalf("复活 billing claim：%v", err)
	}
	return attemptSeq
}

func seedReleaseRecoveryFixture(t *testing.T, ctx context.Context, f *quotaFixture, service *Service, label string) releaseRecoveryFixture {
	t.Helper()
	now := time.Now().UTC().Truncate(time.Second)
	userScopeID := fmt.Sprint(f.userID)
	requestPolicy := f.seedPolicyWithMode(now, ScopeUser, userScopeID, MetricRequests, WindowFixed, 3600, "100", ModeEnforce)
	costPolicy := f.seedPolicyWithMode(now, ScopeUser, userScopeID, MetricCostUSD, WindowFixed, 3600, "100", ModeEnforce)
	f.seedPolicyWithMode(now, ScopeUser, userScopeID, MetricConcurrency, WindowNone, 0, "1", ModeEnforce)
	reserve := f.reserveForSettlement(ctx, service, now, label, "4", true)
	return releaseRecoveryFixture{
		reserve:       reserve,
		requestPolicy: requestPolicy,
		costPolicy:    costPolicy,
		userScopeID:   userScopeID,
		at:            now,
	}
}

func assertHeldQuotaFixture(t *testing.T, f *quotaFixture, seed releaseRecoveryFixture) {
	t.Helper()
	requestValues := f.windowValues(seed.requestPolicy, seed.at)
	costValues := f.windowValues(seed.costPolicy, seed.at)
	if !requestValues.reserved.Equal(decimal.NewFromInt(1)) || !costValues.reserved.Equal(decimal.NewFromInt(4)) {
		t.Fatalf("held request/cost reserved=%s/%s; want 1/4", requestValues.reserved, costValues.reserved)
	}
	if got := f.activeSlotCount(ScopeUser, seed.userScopeID); got != 1 {
		t.Fatalf("active slots=%d; want 1 while recovery is protected", got)
	}
}

func assertReleasedQuotaFixture(t *testing.T, f *quotaFixture, seed releaseRecoveryFixture) {
	t.Helper()
	if status := f.reservationStatus(seed.reserve.Reservation.ID); status != ReservationReleased {
		t.Fatalf("reservation status=%s; want released", status)
	}
	requestValues := f.windowValues(seed.requestPolicy, seed.at)
	costValues := f.windowValues(seed.costPolicy, seed.at)
	if !requestValues.reserved.Equal(decimal.Zero) || !costValues.reserved.Equal(decimal.Zero) {
		t.Fatalf("released request/cost reserved=%s/%s; want 0/0", requestValues.reserved, costValues.reserved)
	}
	if got := f.activeSlotCount(ScopeUser, seed.userScopeID); got != 0 {
		t.Fatalf("active slots=%d; want 0", got)
	}
}

type releaseAuditConflictFault struct {
	t            *testing.T
	f            *quotaFixture
	sequenceName string
	functionName string
	triggerName  string
}

func installReleaseAuditConflictFault(t *testing.T, ctx context.Context, f *quotaFixture, failThrough int64) *releaseAuditConflictFault {
	t.Helper()
	suffix := strings.ReplaceAll(f.testSQLIdentifier("cd2_release"), "-", "_")
	fault := &releaseAuditConflictFault{
		t:            t,
		f:            f,
		sequenceName: suffix + "_seq",
		functionName: suffix + "_fn",
		triggerName:  suffix + "_trg",
	}
	quotedSequence := pgQuoteIdentifier(fault.sequenceName)
	quotedFunction := pgQuoteIdentifier(fault.functionName)
	quotedTrigger := pgQuoteIdentifier(fault.triggerName)
	if _, err := f.pool.Exec(ctx, fmt.Sprintf(`CREATE SEQUENCE %s START WITH 1`, quotedSequence)); err != nil {
		t.Fatalf("create release conflict sequence: %v", err)
	}
	t.Cleanup(fault.drop)
	functionSQL := fmt.Sprintf(`
CREATE FUNCTION %s() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
	current_attempt bigint;
BEGIN
	IF NEW.tenant_id = %d AND COALESCE(NEW.payload ->> 'operation', '') = 'release_aborted' THEN
		current_attempt := nextval('%s');
		IF current_attempt <= %d THEN
			RAISE EXCEPTION USING ERRCODE = '40001', MESSAGE = 'deterministic quota release serialization failure';
		END IF;
	END IF;
	RETURN NEW;
END
$$;`, quotedFunction, f.tenantID, strings.ReplaceAll(quotedSequence, "'", "''"), failThrough)
	if _, err := f.pool.Exec(ctx, functionSQL); err != nil {
		t.Fatalf("create release conflict function: %v", err)
	}
	if _, err := f.pool.Exec(ctx, fmt.Sprintf(`CREATE TRIGGER %s BEFORE INSERT ON quota_audit_events FOR EACH ROW EXECUTE FUNCTION %s()`, quotedTrigger, quotedFunction)); err != nil {
		t.Fatalf("create release conflict trigger: %v", err)
	}
	return fault
}

func (f *releaseAuditConflictFault) attempts() int64 {
	f.t.Helper()
	var attempts int64
	if err := f.f.pool.QueryRow(f.f.ctx, fmt.Sprintf(`SELECT last_value FROM %s`, pgQuoteIdentifier(f.sequenceName))).Scan(&attempts); err != nil {
		f.t.Fatalf("read release conflict sequence: %v", err)
	}
	return attempts
}

func (f *releaseAuditConflictFault) drop() {
	ctx := context.Background()
	_, _ = f.f.pool.Exec(ctx, fmt.Sprintf(`DROP TRIGGER IF EXISTS %s ON quota_audit_events`, pgQuoteIdentifier(f.triggerName)))
	_, _ = f.f.pool.Exec(ctx, fmt.Sprintf(`DROP FUNCTION IF EXISTS %s()`, pgQuoteIdentifier(f.functionName)))
	_, _ = f.f.pool.Exec(ctx, fmt.Sprintf(`DROP SEQUENCE IF EXISTS %s`, pgQuoteIdentifier(f.sequenceName)))
}

func installReleaseQueueFailureFault(t *testing.T, ctx context.Context, f *quotaFixture) func() {
	t.Helper()
	name := f.testSQLIdentifier("cd2_queue_failure")
	functionName := name + "_fn"
	triggerName := name + "_trg"
	quotedFunction := pgQuoteIdentifier(functionName)
	quotedTrigger := pgQuoteIdentifier(triggerName)
	drop := func() {
		cleanupCtx := context.Background()
		_, _ = f.pool.Exec(cleanupCtx, fmt.Sprintf(`DROP TRIGGER IF EXISTS %s ON quota_reconciliation_jobs`, quotedTrigger))
		_, _ = f.pool.Exec(cleanupCtx, fmt.Sprintf(`DROP FUNCTION IF EXISTS %s()`, quotedFunction))
	}
	t.Cleanup(drop)
	functionSQL := fmt.Sprintf(`
CREATE FUNCTION %s() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
	IF NEW.tenant_id = %d THEN
		RAISE EXCEPTION 'deterministic quota handoff enqueue failure';
	END IF;
	RETURN NEW;
END
$$;`, quotedFunction, f.tenantID)
	if _, err := f.pool.Exec(ctx, functionSQL); err != nil {
		t.Fatalf("create queue failure function: %v", err)
	}
	if _, err := f.pool.Exec(ctx, fmt.Sprintf(`CREATE TRIGGER %s BEFORE INSERT ON quota_reconciliation_jobs FOR EACH ROW EXECUTE FUNCTION %s()`, quotedTrigger, quotedFunction)); err != nil {
		t.Fatalf("create queue failure trigger: %v", err)
	}
	return drop
}

func releaseRecoveryState(t *testing.T, f *quotaFixture, reservationID int64) (ReservationStatus, time.Time, time.Time) {
	t.Helper()
	var status ReservationStatus
	var lease time.Time
	var dbNow time.Time
	if err := f.pool.QueryRow(f.ctx,
		`SELECT status, lease_expires_at, NOW() FROM quota_reservations WHERE tenant_id=$1 AND id=$2`,
		f.tenantID, reservationID,
	).Scan(&status, &lease, &dbNow); err != nil {
		t.Fatalf("read release recovery state: %v", err)
	}
	return status, lease, dbNow
}

func releaseJobCountForClaim(t *testing.T, f *quotaFixture, claimID int64) int64 {
	t.Helper()
	var count int64
	if err := f.pool.QueryRow(f.ctx,
		`SELECT COUNT(*) FROM quota_reconciliation_jobs WHERE tenant_id=$1 AND claim_id=$2`,
		f.tenantID, claimID,
	).Scan(&count); err != nil {
		t.Fatalf("count release jobs: %v", err)
	}
	return count
}

type releaseJobSnapshot struct {
	id            int64
	reservationID *int64
	status        string
	nextRunAt     time.Time
	dbNow         time.Time
}

func releaseJobForClaim(t *testing.T, f *quotaFixture, claimID int64) releaseJobSnapshot {
	t.Helper()
	var job releaseJobSnapshot
	if err := f.pool.QueryRow(f.ctx,
		`SELECT id, reservation_id, status, next_run_at, NOW()
		 FROM quota_reconciliation_jobs
		 WHERE tenant_id=$1 AND claim_id=$2 AND job_kind='release_after_abort'`,
		f.tenantID, claimID,
	).Scan(&job.id, &job.reservationID, &job.status, &job.nextRunAt, &job.dbNow); err != nil {
		t.Fatalf("read release job: %v", err)
	}
	return job
}
