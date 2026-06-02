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
	terminalReconciliationDelay  = 3650 * 24 * time.Hour

	reconciliationKindSettleAfterBillingSuccess = "settle_after_billing_success"
	reconciliationKindReleaseAfterAbort         = "release_after_abort"
	reconciliationKindReleaseAfterCacheHit      = "release_after_cache_hit"

	// Release 校验已有白名单; reconciler 使用现有合法 reason, 不扩展主路径校验面。
	reconciliationReleaseReason = "upstream_error"
)

type ReconcilerOptions struct {
	MaxAttempts int
	BaseBackoff time.Duration
	MaxBackoff  time.Duration
	Limit       int
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
		logger:      opts.Logger,
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
