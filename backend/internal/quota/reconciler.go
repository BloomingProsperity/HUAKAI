package quota

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"
)

const (
	defaultReconcilerMaxAttempts = 10
	defaultReconcilerBaseBackoff = time.Minute
	defaultReconcilerMaxBackoff  = time.Hour
	defaultReconcilerLimit       = 100
	defaultReconcilerTenantSweep = 200
	terminalReconciliationDelay  = 3650 * 24 * time.Hour

	reconciliationKindSettleAfterBillingSuccess = "settle_after_billing_success"
	reconciliationKindReleaseAfterAbort         = "release_after_abort"
	reconciliationKindReleaseAfterCacheHit      = "release_after_cache_hit"

	// Release 校验已有白名单; reconciler 使用现有合法 reason, 不扩展主路径校验面。
	reconciliationReleaseReason = "upstream_error"

	// billing_ledger_claims.status 终态值(与 0002 迁移 CHECK 对齐)。
	// quota 不 import internal/billing(避免反向依赖), 以字面值对齐。
	claimStatusCommitted = "committed"
	claimStatusAborted   = "aborted"
)

var errReconciliationRecoveryInvalidated = errors.New("quota reconciler: recovery invalidated")

type ReconcilerOptions struct {
	MaxAttempts int
	BaseBackoff time.Duration
	MaxBackoff  time.Duration
	Limit       int
	// TenantSweep 是全局 sweep 单轮扫描的最大租户数(ReconcileAllTenants 用),默认 200。
	TenantSweep int
	Logger      *slog.Logger
}

// Reconciler 消费 quota_reconciliation_jobs, 重放 B2b 已提交业务结果后的 quota 补偿动作。
type Reconciler struct {
	service     *Service
	store       PGStore
	maxAttempts int
	baseBackoff time.Duration
	maxBackoff  time.Duration
	limit       int
	tenantSweep int
	logger      *slog.Logger
}

func NewReconciler(service *Service, store PGStore, opts ReconcilerOptions) *Reconciler {
	if store == nil && service != nil {
		store = service.store
	}
	if service == nil && store != nil {
		service = NewService(store)
	}
	if opts.MaxAttempts <= 0 {
		opts.MaxAttempts = defaultReconcilerMaxAttempts
	}
	if opts.BaseBackoff <= 0 {
		opts.BaseBackoff = defaultReconcilerBaseBackoff
	}
	if opts.MaxBackoff <= 0 {
		opts.MaxBackoff = defaultReconcilerMaxBackoff
	}
	if opts.Limit <= 0 {
		opts.Limit = defaultReconcilerLimit
	}
	if opts.TenantSweep <= 0 {
		opts.TenantSweep = defaultReconcilerTenantSweep
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	return &Reconciler{
		service:     service,
		store:       store,
		maxAttempts: opts.MaxAttempts,
		baseBackoff: opts.BaseBackoff,
		maxBackoff:  opts.MaxBackoff,
		limit:       opts.Limit,
		tenantSweep: opts.TenantSweep,
		logger:      opts.Logger,
	}
}

// ReconcileAllTenants 是跨租户全局 sweep:列出有到期 job 的 distinct 租户,对每个租户走
// 现有单租户 ReconcileDueJobs(每租户各自 limit,天然公平不饿死)。一个租户失败不阻断其它
// 租户,错误汇总返回;返回本轮成功处理的 job 总数。这是让 quota 补偿器从「建了没接线的死代码」
// 变成真跑的入口——reservation 结算/释放失败入队后由本 sweep 重放,不再永久卡 reserved。
func (r *Reconciler) ReconcileAllTenants(ctx context.Context, now time.Time) (int, error) {
	if r == nil || r.service == nil || r.store == nil {
		return 0, fmt.Errorf("quota reconciler: service and store are required")
	}
	if now.IsZero() {
		return 0, fmt.Errorf("quota reconciler: now is required")
	}
	now = now.UTC()
	tenants, err := r.store.ListTenantsWithDueReconciliationJobs(ctx, now, r.tenantSweep)
	if err != nil {
		return 0, fmt.Errorf("quota reconciler: list tenants with due jobs: %w", err)
	}
	total := 0
	var errs []error
	for _, tenantID := range tenants {
		if err := ctx.Err(); err != nil {
			errs = append(errs, err)
			break
		}
		processed, err := r.ReconcileDueJobs(ctx, tenantID, now, r.limit)
		total += processed
		if err != nil {
			// 单租户失败不阻断其它租户的补偿(隔离故障域)。
			errs = append(errs, fmt.Errorf("tenant %d: %w", tenantID, err))
		}
	}
	if len(tenants) == r.tenantSweep {
		// 命中扫描上限=可能还有更多租户待处理,记 info 供运营判断是否调大 TenantSweep/缩短 interval。
		r.logger.InfoContext(ctx, "quota reconciler tenant sweep hit limit; more tenants may be pending",
			"tenant_sweep_limit", r.tenantSweep, "processed_jobs", total)
	}
	return total, errors.Join(errs...)
}

// SweepStaleReservations 兜住「billing claim 已终态但 quota 补偿 job 从未入队」的崩溃窗口
// (进程死于 billing Tx2 commit 与 quota settle 之间时, job 表里什么都没有, 预留会永久卡
// reserved 冻结窗口 headroom)。只取无补偿 job 史的孤儿行——有 job 史的行归 job 重放段,
// 其退避与终态停靠不可被本段每轮重试击穿(本段动作失败时 Settle 内部会入队 job,
// 于是该行下一轮起自动改走 job 段, 天然获得退避)。
// 竞态口径: list 是时点快照, 每行动作前经 sweepStaleRow 复核现状(预留仍未终态、lease 仍
// 过期、claim 仍终态), 把 aborted→复活链的竞态收窄到复核与动作事务之间的毫秒级; 动作本身
// 行锁 + 幂等(见 service_settle.go 终态 switch), 与并发真实结算相撞时后到方幂等命中,
// 残余最坏情形是一次错误返回入 errs 由后续轮次/job 段接手。返回本轮真实补偿的预留数
// (幂等命中不计)。
func (r *Reconciler) SweepStaleReservations(ctx context.Context, now time.Time, limit int) (int, error) {
	if r == nil || r.service == nil || r.store == nil {
		return 0, fmt.Errorf("quota reconciler: service and store are required")
	}
	if now.IsZero() {
		return 0, fmt.Errorf("quota reconciler: now is required")
	}
	now = now.UTC()
	if limit <= 0 {
		limit = r.limit
	}
	stale, err := r.store.ListStaleReservedReservations(ctx, now, limit)
	if err != nil {
		return 0, fmt.Errorf("quota reconciler: list stale reservations: %w", err)
	}
	processed := 0
	var errs []error
	for _, row := range stale {
		if err := ctx.Err(); err != nil {
			errs = append(errs, err)
			break
		}
		acted, actErr := r.sweepStaleRow(ctx, now, row)
		if actErr != nil {
			errs = append(errs, fmt.Errorf("tenant %d reservation %d claim %d (%s): %w",
				row.TenantID, row.ReservationID, row.ClaimID, row.ClaimStatus, actErr))
			continue
		}
		if acted {
			processed++
		}
	}
	if processed > 0 {
		r.logger.InfoContext(ctx, "quota reconciler swept stale reservations",
			"processed", processed, "scanned", len(stale))
	}
	return processed, errors.Join(errs...)
}

// sweepStaleRow 对单个候选行复核现状后定向补偿。返回 (是否真实补偿, 错误);
// 复核不通过(claim 复活/预留已终态/lease 已被 reactivate 续期)与幂等命中都算未补偿、非错误。
func (r *Reconciler) sweepStaleRow(ctx context.Context, now time.Time, row StaleReservation) (bool, error) {
	rec, err := r.store.GetReservationByClaimForUpdate(ctx, row.TenantID, row.ClaimID)
	if err != nil {
		return false, fmt.Errorf("recheck reservation: %w", err)
	}
	if rec.ID != row.ReservationID ||
		(rec.Status != ReservationReserved && rec.Status != ReservationReconciliationNeeded) ||
		rec.LeaseExpiresAt.After(now) {
		return false, nil
	}
	claim, err := r.store.GetClaimTerminalState(ctx, row.TenantID, row.ClaimID)
	if err != nil {
		return false, fmt.Errorf("recheck claim: %w", err)
	}
	switch claim.Status {
	case claimStatusCommitted:
		// billing 金额权威取 claim 实结额; actual_cost 仅在 commit 时写入
		//(billing_settle.sql, WHERE status='reserving'), NULL 时退 predicted 保守代理。
		actual := rec.PredictedCost
		if claim.ActualCostSet {
			actual = claim.ActualCost
		}
		result, err := r.service.Settle(ctx, SettleRequest{
			TenantID:      row.TenantID,
			ClaimID:       row.ClaimID,
			ReservationID: row.ReservationID,
			ActualCost:    actual,
			SettledAt:     now,
		})
		if err != nil {
			return false, err
		}
		return !result.IdempotencyHit, nil
	case claimStatusAborted:
		result, err := r.service.Release(ctx, ReleaseRequest{
			TenantID:      row.TenantID,
			ClaimID:       row.ClaimID,
			ReservationID: row.ReservationID,
			Reason:        reconciliationReleaseReason,
			ReleasedAt:    now,
		})
		if err != nil {
			return false, err
		}
		return !result.IdempotencyHit, nil
	default:
		return false, nil
	}
}

func (r *Reconciler) ReconcileDueJobs(ctx context.Context, tenantID int64, now time.Time, limit int) (int, error) {
	if r == nil || r.service == nil || r.store == nil {
		return 0, fmt.Errorf("quota reconciler: service and store are required")
	}
	if tenantID <= 0 {
		return 0, fmt.Errorf("quota reconciler: tenant_id is required")
	}
	if now.IsZero() {
		return 0, fmt.Errorf("quota reconciler: now is required")
	}
	now = now.UTC()
	if limit <= 0 {
		limit = r.limit
	}
	jobs, err := r.store.ListDueReconciliationJobs(ctx, tenantID, now, limit)
	if err != nil {
		return 0, err
	}

	processed := 0
	var errs []error
	for _, job := range jobs {
		if err := r.store.MarkReconciliationJobRunning(ctx, tenantID, job.ID); err != nil {
			errs = append(errs, fmt.Errorf("quota reconciler: mark job %d running: %w", job.ID, err))
			continue
		}
		if err := r.replayJob(ctx, tenantID, now, job); err != nil {
			if failErr := r.failRunningJob(ctx, tenantID, now, job, err); failErr != nil {
				errs = append(errs, errors.Join(err, failErr))
			} else {
				errs = append(errs, err)
			}
			continue
		}
		if err := r.store.CompleteReconciliationJob(ctx, tenantID, job.ID); err != nil {
			completeErr := fmt.Errorf("quota reconciler: complete job %d: %w", job.ID, err)
			if failErr := r.failRunningJob(ctx, tenantID, now, job, completeErr); failErr != nil {
				errs = append(errs, errors.Join(completeErr, failErr))
			} else {
				errs = append(errs, completeErr)
			}
			continue
		}
		processed++
	}
	return processed, errors.Join(errs...)
}

func (r *Reconciler) replayJob(ctx context.Context, tenantID int64, now time.Time, job ReconciliationJob) error {
	reservation, err := r.store.GetReservationByClaimForUpdate(ctx, tenantID, job.ClaimID)
	if err != nil {
		return fmt.Errorf("quota reconciler: load reservation for claim %d: %w", job.ClaimID, err)
	}
	reservationID := reservation.ID
	if job.ReservationID != nil {
		reservationID = *job.ReservationID
	}

	switch job.Kind {
	case reconciliationKindSettleAfterBillingSuccess:
		// billing ledger 是金额权威: claim 已写实结额时用它, 未写(NULL)退回 reserve 阶段
		// predicted_cost 保守代理, 与清扫段同一取值口径, 避免同类孤儿两段结出不同金额。
		actual := reservation.PredictedCost
		if claim, err := r.store.GetClaimTerminalState(ctx, tenantID, job.ClaimID); err == nil && claim.ActualCostSet {
			actual = claim.ActualCost
		}
		_, err := r.service.Settle(ctx, SettleRequest{
			TenantID:      tenantID,
			ClaimID:       job.ClaimID,
			ReservationID: reservationID,
			ActualCost:    actual,
			SettledAt:     now,
		})
		return err
	case reconciliationKindReleaseAfterAbort:
		_, err := r.service.Release(ctx, ReleaseRequest{
			TenantID:      tenantID,
			ClaimID:       job.ClaimID,
			ReservationID: reservationID,
			Reason:        reconciliationReleaseReason,
			ReleasedAt:    now,
		})
		if errors.Is(err, ErrReleaseInvalidatedByRevival) {
			return fmt.Errorf("%w: %v", errReconciliationRecoveryInvalidated, err)
		}
		return err
	case reconciliationKindReleaseAfterCacheHit:
		_, err := r.service.CommitCacheHit(ctx, CacheHitRequest{
			TenantID:      tenantID,
			ClaimID:       job.ClaimID,
			ReservationID: reservationID,
			CommittedAt:   now,
			CacheSource:   "quota_reconciler",
		})
		return err
	default:
		return fmt.Errorf("quota reconciler: unsupported job kind %q", job.Kind)
	}
}

func (r *Reconciler) failRunningJob(ctx context.Context, tenantID int64, now time.Time, job ReconciliationJob, cause error) error {
	nextRunAt := r.nextRunAt(now, job.AttemptCount)
	recoveryInvalidated := errors.Is(cause, errReconciliationRecoveryInvalidated)
	if recoveryInvalidated || (r.maxAttempts > 0 && job.AttemptCount+1 >= r.maxAttempts) {
		nextRunAt = now.Add(terminalReconciliationDelay)
		message := "quota reconciliation job reached max attempts"
		if recoveryInvalidated {
			message = "quota reconciliation job invalidated by current claim state"
		}
		r.logger.WarnContext(ctx, message,
			"tenant_id", tenantID,
			"job_id", job.ID,
			"job_kind", job.Kind,
			"attempt_count", job.AttemptCount+1,
			"next_run_at", nextRunAt,
			"error", cause,
		)
	}
	if err := r.store.FailReconciliationJob(ctx, ReconciliationFailure{
		TenantID:  tenantID,
		JobID:     job.ID,
		LastError: cause.Error(),
		NextRunAt: nextRunAt,
	}); err != nil {
		return fmt.Errorf("quota reconciler: fail job %d: %w", job.ID, err)
	}
	return nil
}

func (r *Reconciler) nextRunAt(now time.Time, attemptCount int) time.Time {
	backoff := r.baseBackoff
	for i := 0; i < attemptCount; i++ {
		if backoff >= r.maxBackoff/2 {
			backoff = r.maxBackoff
			break
		}
		backoff *= 2
	}
	if backoff > r.maxBackoff {
		backoff = r.maxBackoff
	}
	return now.Add(backoff)
}
