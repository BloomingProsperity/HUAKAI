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
//(进程死于 billing Tx2 commit 与 quota settle 之间时, job 表里什么都没有, 预留会永久卡
// reserved 冻结窗口 headroom)。按 claim 终态定向补偿: committed→Settle(优先用 claim 的
// actual_cost, NULL 时退回 predicted_cost 保守代理), aborted→Release。两个动作与并发中的
// 真实结算相撞时都是幂等命中(见 service_settle.go 终态 switch), 无竞态窗口; claim 仍
// reserving 的行不取(billing lease sweeper 先终结, 下一轮再接)。返回本轮补偿成功的预留数。
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
		var actErr error
		switch row.ClaimStatus {
		case claimStatusCommitted:
			actual := row.PredictedCost
			if row.ClaimActualCostSet {
				actual = row.ClaimActualCost
			}
			_, actErr = r.service.Settle(ctx, SettleRequest{
				TenantID:      row.TenantID,
				ClaimID:       row.ClaimID,
				ReservationID: row.ReservationID,
				ActualCost:    actual,
				SettledAt:     now,
			})
		case claimStatusAborted:
			_, actErr = r.service.Release(ctx, ReleaseRequest{
				TenantID:      row.TenantID,
				ClaimID:       row.ClaimID,
				ReservationID: row.ReservationID,
				Reason:        reconciliationReleaseReason,
				ReleasedAt:    now,
			})
		default:
			continue
		}
		if actErr != nil {
			// 单行失败不阻断其它行(下一轮重扫仍会接住本行)。
			errs = append(errs, fmt.Errorf("tenant %d reservation %d claim %d (%s): %w",
				row.TenantID, row.ReservationID, row.ClaimID, row.ClaimStatus, actErr))
			continue
		}
		processed++
	}
	if processed > 0 {
		r.logger.InfoContext(ctx, "quota reconciler swept stale reservations",
			"processed", processed, "scanned", len(stale))
	}
	return processed, errors.Join(errs...)
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
		// job 表没有真实 actual_cost; billing ledger 是金额权威。这里用 reserve 阶段
		// predicted_cost 作为 quota 视图的保守代理, 把 hold 从 reserved 转到 settled,
		// 不释放用量, 避免 billing 已成功但 quota 漏算。
		_, err := r.service.Settle(ctx, SettleRequest{
			TenantID:      tenantID,
			ClaimID:       job.ClaimID,
			ReservationID: reservationID,
			ActualCost:    reservation.PredictedCost,
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
	if r.maxAttempts > 0 && job.AttemptCount+1 >= r.maxAttempts {
		nextRunAt = now.Add(terminalReconciliationDelay)
		r.logger.WarnContext(ctx, "quota reconciliation job reached max attempts",
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
