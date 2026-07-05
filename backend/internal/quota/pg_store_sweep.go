package quota

import (
	"context"
	"math"
	"time"

	dbquota "github.com/BloomingProsperity/HUAKAI/internal/db/quota"
)

func (s *PostgresStore) ListDueReconciliationJobs(ctx context.Context, tenantID int64, at time.Time, limit int) ([]ReconciliationJob, error) {
	q, err := s.queries()
	if err != nil {
		return nil, err
	}
	if limit <= 0 {
		return nil, nil
	}
	if limit > math.MaxInt32 {
		limit = math.MaxInt32
	}
	rows, err := q.ListDueQuotaReconciliationJobs(ctx, dbquota.ListDueQuotaReconciliationJobsParams{
		TenantID: tenantID,
		AtTime:   pgTimestamptz(at),
		JobLimit: int32(limit),
	})
	if err != nil {
		return nil, err
	}
	jobs := make([]ReconciliationJob, 0, len(rows))
	for _, row := range rows {
		jobs = append(jobs, ReconciliationJob{
			TenantID:      row.TenantID,
			ID:            row.ID,
			ClaimID:       row.ClaimID,
			ReservationID: row.ReservationID,
			Kind:          row.JobKind,
			Status:        row.Status,
			AttemptCount:  int(row.AttemptCount),
			LastError:     row.LastError,
			NextRunAt:     pgTime(row.NextRunAt),
		})
	}
	return jobs, nil
}

// ListTenantsWithDueReconciliationJobs 返回有到期 job 的 distinct 租户(全局 sweep 入口)。
// tenantLimit<=0 时不查(返回空),防止无界扫描。
func (s *PostgresStore) ListTenantsWithDueReconciliationJobs(ctx context.Context, at time.Time, tenantLimit int) ([]int64, error) {
	q, err := s.queries()
	if err != nil {
		return nil, err
	}
	if tenantLimit <= 0 {
		return nil, nil
	}
	if tenantLimit > math.MaxInt32 {
		tenantLimit = math.MaxInt32
	}
	return q.ListTenantsWithDueQuotaReconciliationJobs(ctx, dbquota.ListTenantsWithDueQuotaReconciliationJobsParams{
		AtTime:      pgTimestamptz(at),
		TenantLimit: int32(tenantLimit),
	})
}

// ListStaleReservedReservations 返回 lease 已过期仍未终态、且 billing claim 已终态的预留。
// limit<=0 时不查(返回空),防止无界扫描。
func (s *PostgresStore) ListStaleReservedReservations(ctx context.Context, at time.Time, limit int) ([]StaleReservation, error) {
	q, err := s.queries()
	if err != nil {
		return nil, err
	}
	if limit <= 0 {
		return nil, nil
	}
	if limit > math.MaxInt32 {
		limit = math.MaxInt32
	}
	rows, err := q.ListStaleReservedQuotaReservations(ctx, dbquota.ListStaleReservedQuotaReservationsParams{
		AtTime:   pgTimestamptz(at),
		RowLimit: int32(limit),
	})
	if err != nil {
		return nil, err
	}
	stale := make([]StaleReservation, 0, len(rows))
	for _, row := range rows {
		stale = append(stale, StaleReservation{
			TenantID:           row.TenantID,
			ReservationID:      row.ReservationID,
			ClaimID:            row.ClaimID,
			PredictedCost:      decimalFromPG(row.PredictedCost),
			ClaimStatus:        row.ClaimStatus,
			ClaimActualCost:    row.ClaimActualCost.Decimal,
			ClaimActualCostSet: row.ClaimActualCost.Valid,
		})
	}
	return stale, nil
}

// GetClaimTerminalState 点查 billing claim 现状(status + actual_cost)。
func (s *PostgresStore) GetClaimTerminalState(ctx context.Context, tenantID, claimID int64) (ClaimTerminalState, error) {
	q, err := s.queries()
	if err != nil {
		return ClaimTerminalState{}, err
	}
	row, err := q.GetBillingClaimTerminalState(ctx, dbquota.GetBillingClaimTerminalStateParams{
		TenantID: tenantID,
		ClaimID:  claimID,
	})
	if err != nil {
		return ClaimTerminalState{}, err
	}
	return ClaimTerminalState{
		Status:        row.Status,
		ActualCost:    row.ActualCost.Decimal,
		ActualCostSet: row.ActualCost.Valid,
	}, nil
}

func (s *PostgresStore) MarkReconciliationJobRunning(ctx context.Context, tenantID int64, jobID int64) error {
	q, err := s.queries()
	if err != nil {
		return err
	}
	rows, err := q.MarkQuotaReconciliationJobRunning(ctx, dbquota.MarkQuotaReconciliationJobRunningParams{
		TenantID: tenantID,
		JobID:    jobID,
	})
	return requireAffected(rows, err)
}

func (s *PostgresStore) CompleteReconciliationJob(ctx context.Context, tenantID int64, jobID int64) error {
	q, err := s.queries()
	if err != nil {
		return err
	}
	rows, err := q.CompleteQuotaReconciliationJob(ctx, dbquota.CompleteQuotaReconciliationJobParams{
		TenantID: tenantID,
		JobID:    jobID,
	})
	return requireAffected(rows, err)
}

func (s *PostgresStore) FailReconciliationJob(ctx context.Context, input ReconciliationFailure) error {
	q, err := s.queries()
	if err != nil {
		return err
	}
	rows, err := q.FailQuotaReconciliationJob(ctx, dbquota.FailQuotaReconciliationJobParams{
		LastError: input.LastError,
		NextRunAt: pgTimestamptz(input.NextRunAt),
		TenantID:  input.TenantID,
		JobID:     input.JobID,
	})
	return requireAffected(rows, err)
}
