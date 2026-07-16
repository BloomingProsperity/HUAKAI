package quota

import (
	"context"
	"errors"
	"expvar"
	"fmt"
	"math/rand/v2"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

const (
	releaseFinalizationAttempts = 6
	releaseRetryBaseBackoff     = 5 * time.Millisecond
	releaseRetryBackoffCap      = 100 * time.Millisecond

	quotaCleanupAttempts    = 3
	quotaCleanupBaseBackoff = 5 * time.Millisecond
	quotaCleanupBackoffCap  = 25 * time.Millisecond
	quotaCleanupTimeout     = time.Second

	quotaMetricsMapName = "quota"
)

const (
	quotaReleaseOutcomeRetrySuccess     = "retry_success"
	quotaReleaseOutcomeExhausted        = "exhausted"
	quotaReleaseOutcomeHandoffQueued    = "handoff_queued"
	quotaReleaseOutcomeHandoffStaleOnly = "handoff_stale_only"
	quotaReleaseOutcomeHandoffFailed    = "handoff_failed"
)

type quotaFinalizationRetryPolicy struct {
	attempts           int
	baseBackoff        time.Duration
	backoffCap         time.Duration
	decorrelatedJitter bool
}

var (
	defaultFinalizationRetryPolicy = quotaFinalizationRetryPolicy{attempts: reserveTxRetryAttempts}
	releaseFinalizationRetryPolicy = quotaFinalizationRetryPolicy{
		attempts:           releaseFinalizationAttempts,
		baseBackoff:        releaseRetryBaseBackoff,
		backoffCap:         releaseRetryBackoffCap,
		decorrelatedJitter: true,
	}
	cleanupRetryPolicy = quotaFinalizationRetryPolicy{
		attempts:           quotaCleanupAttempts,
		baseBackoff:        quotaCleanupBaseBackoff,
		backoffCap:         quotaCleanupBackoffCap,
		decorrelatedJitter: true,
	}
)

type releaseRecoveryStore interface {
	PrepareReleaseRecovery(ctx context.Context, tenantID int64, claimID int64, reservationID int64) (int64, error)
}

var (
	quotaMetricsOnce sync.Once
	quotaMetrics     *expvar.Map
)

func (s *Service) runQuotaFinalizationWithRetry(
	ctx context.Context,
	operation string,
	policy quotaFinalizationRetryPolicy,
	run func(PGStore) error,
) error {
	if s == nil || s.store == nil {
		return ErrStoreNotConfigured
	}
	if policy.attempts <= 0 {
		policy = defaultFinalizationRetryPolicy
	}

	var err error
	var previousDelay time.Duration
	lastSQLState := "none"
	for attempt := 0; attempt < policy.attempts; attempt++ {
		err = s.withStore(ctx, run)
		if !isPgRetryableTxConflict(err) {
			if err == nil && attempt > 0 && operation == "release" {
				observeQuotaReleaseResilience(lastSQLState, quotaReleaseOutcomeRetrySuccess)
			}
			return err
		}
		lastSQLState = quotaRetrySQLState(err)
		if attempt == policy.attempts-1 {
			if operation == "release" {
				observeQuotaReleaseResilience(lastSQLState, quotaReleaseOutcomeExhausted)
			}
			return &RetryableError{Operation: "quota " + operation + " transaction", Cause: err}
		}
		delay := finalizationRetryDelay(policy, attempt, previousDelay)
		previousDelay = delay
		if sleepErr := sleepQuotaRetry(ctx, delay); sleepErr != nil {
			return sleepErr
		}
	}
	return err
}

func finalizationRetryDelay(policy quotaFinalizationRetryPolicy, attempt int, previous time.Duration) time.Duration {
	if !policy.decorrelatedJitter {
		return reserveRetryBackoff(attempt)
	}
	base := policy.baseBackoff
	if base <= 0 {
		return 0
	}
	capDelay := policy.backoffCap
	if capDelay < base {
		capDelay = base
	}
	if previous < base {
		previous = base
	}
	upper := previous * 3
	if upper > capDelay || upper < previous {
		upper = capDelay
	}
	span := upper - base
	if span <= 0 {
		return base
	}
	return base + time.Duration(rand.Int64N(int64(span)+1))
}

func sleepQuotaRetry(ctx context.Context, delay time.Duration) error {
	if delay <= 0 {
		return nil
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (s *Service) handoffReleaseRecovery(
	ctx context.Context,
	tenantID int64,
	claimID int64,
	reservationID int64,
	cause error,
) (bool, error) {
	if s == nil || s.store == nil {
		observeQuotaReleaseResilience(quotaRetrySQLState(cause), quotaReleaseOutcomeHandoffFailed)
		return false, fmt.Errorf("quota release recovery handoff failed: %w", ErrStoreNotConfigured)
	}
	sqlState := quotaRetrySQLState(cause)
	var actualReservationID int64
	prepareErr := s.runQuotaCleanupStepWithRetry(ctx, func(store PGStore) error {
		recoveryStore, ok := store.(releaseRecoveryStore)
		if !ok {
			return fmt.Errorf("quota release recovery store is not configured")
		}
		resolvedID, err := recoveryStore.PrepareReleaseRecovery(ctx, tenantID, claimID, reservationID)
		if err == nil {
			actualReservationID = resolvedID
		}
		return err
	})
	if errors.Is(prepareErr, pgx.ErrNoRows) {
		// 并发终结者已把 reservation 推到终态时无需反向改写，也无需制造空任务。
		return false, nil
	}
	if prepareErr != nil {
		observeQuotaReleaseResilience(sqlState, quotaReleaseOutcomeHandoffFailed)
		return false, fmt.Errorf("quota release recovery prepare failed: %w", prepareErr)
	}

	lastError := quotaReconcileKindReleaseFailed
	if cause != nil {
		lastError += ": " + cause.Error()
	}
	nextRunAt := time.Now().UTC()
	enqueueErr := s.runQuotaCleanupStepWithRetry(ctx, func(store PGStore) error {
		_, err := store.EnqueueReconciliationJob(ctx, ReconciliationEnqueue{
			TenantID:      tenantID,
			ClaimID:       claimID,
			ReservationID: &actualReservationID,
			Kind:          reconciliationDBKind(quotaReconcileKindReleaseFailed),
			LastError:     &lastError,
			NextRunAt:     nextRunAt,
		})
		return err
	})
	if enqueueErr != nil {
		// prepare 已把 lease 提前，任务落盘失败时 stale 段仍有持久恢复资格。
		observeQuotaReleaseResilience(sqlState, quotaReleaseOutcomeHandoffStaleOnly)
		return false, fmt.Errorf("quota release recovery enqueue failed: %w", enqueueErr)
	}
	observeQuotaReleaseResilience(sqlState, quotaReleaseOutcomeHandoffQueued)
	return true, nil
}

func (s *Service) runQuotaCleanupStepWithRetry(ctx context.Context, run func(PGStore) error) error {
	var err error
	var previousDelay time.Duration
	for attempt := 0; attempt < cleanupRetryPolicy.attempts; attempt++ {
		err = s.runQuotaOutsideFailedTx(ctx, run)
		if !isPgRetryableTxConflict(err) || attempt == cleanupRetryPolicy.attempts-1 {
			return err
		}
		delay := finalizationRetryDelay(cleanupRetryPolicy, attempt, previousDelay)
		previousDelay = delay
		if sleepErr := sleepQuotaRetry(ctx, delay); sleepErr != nil {
			return sleepErr
		}
	}
	return err
}

func (s *Service) enqueueFinalizationReconciliation(ctx context.Context, tenantID int64, claimID int64, reservationID int64, requestedKind string, cause error) (bool, error) {
	if s == nil || s.store == nil {
		return false, fmt.Errorf("quota reconciliation enqueue failed: %w", ErrStoreNotConfigured)
	}
	lastError := requestedKind
	if cause != nil {
		lastError += ": " + cause.Error()
	}
	if reservationID != 0 {
		_ = s.runQuotaOutsideFailedTx(ctx, func(store PGStore) error {
			return store.MarkReservationReconciliationNeeded(ctx, tenantID, reservationID, claimID)
		})
	}
	var reservationPtr *int64
	if reservationID != 0 {
		reservationPtr = &reservationID
	}
	err := s.runQuotaOutsideFailedTx(ctx, func(store PGStore) error {
		_, err := store.EnqueueReconciliationJob(ctx, ReconciliationEnqueue{
			TenantID:      tenantID,
			ClaimID:       claimID,
			ReservationID: reservationPtr,
			Kind:          reconciliationDBKind(requestedKind),
			LastError:     &lastError,
			NextRunAt:     time.Now().UTC(),
		})
		return err
	})
	if err != nil {
		return false, fmt.Errorf("quota reconciliation enqueue failed: %w", err)
	}
	return true, nil
}

func (s *Service) runQuotaOutsideFailedTx(ctx context.Context, run func(PGStore) error) error {
	if txStore, ok := s.store.(quotaTxStore); ok {
		return txStore.WithTx(ctx, run)
	}
	return run(s.store)
}

func reconciliationDBKind(requestedKind string) string {
	switch requestedKind {
	case quotaReconcileKindSettleFailed:
		return "settle_after_billing_success"
	case quotaReconcileKindReleaseFailed:
		return "release_after_abort"
	case quotaReconcileKindCacheHitFailed:
		return "release_after_cache_hit"
	default:
		return requestedKind
	}
}

func quotaRetrySQLState(err error) string {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "40001", "40P01":
			return pgErr.Code
		}
	}
	return "none"
}

func observeQuotaReleaseResilience(sqlState string, outcome string) {
	if sqlState != "40001" && sqlState != "40P01" && sqlState != "none" {
		sqlState = "none"
	}
	switch outcome {
	case quotaReleaseOutcomeRetrySuccess,
		quotaReleaseOutcomeExhausted,
		quotaReleaseOutcomeHandoffQueued,
		quotaReleaseOutcomeHandoffStaleOnly,
		quotaReleaseOutcomeHandoffFailed:
	default:
		return
	}
	quotaMetricsOnce.Do(func() {
		if existing := expvar.Get(quotaMetricsMapName); existing != nil {
			quotaMetrics, _ = existing.(*expvar.Map)
			return
		}
		quotaMetrics = expvar.NewMap(quotaMetricsMapName)
	})
	if quotaMetrics == nil {
		return
	}
	quotaMetrics.Add(fmt.Sprintf("operation=release|sqlstate=%s|outcome=%s", sqlState, outcome), 1)
}
