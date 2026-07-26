package mediatask

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/BloomingProsperity/HUAKAI/internal/billing"
	"github.com/BloomingProsperity/HUAKAI/internal/gateway"
)

func (s *PostgresStore) completeSuccessWithUnifiedMoney(ctx context.Context, task Task, owner string, result PollResult, now time.Time) (bool, error) {
	if result.ActualCents < 0 {
		return false, fmt.Errorf("%w: actual_cents", ErrInvalidInput)
	}
	if err := s.persistPendingSuccess(ctx, task, owner, result, now); err != nil {
		return false, err
	}
	claimID, err := claimIDFromHoldRef(task.HoldRef)
	if err != nil {
		return false, err
	}
	status, acquisitionToken, err := s.claimSettlementState(ctx, task.TenantID, claimID)
	if err != nil {
		return false, err
	}
	if status == "committed" {
		return s.markTaskSucceeded(ctx, task.ID, result, now)
	}
	if status == "aborted" {
		return s.finishSuccessAfterSweptClaim(ctx, task, owner, result, now)
	}
	if status != "reserving" || acquisitionToken == uuid.Nil {
		return false, billing.ErrClaimNotReserving
	}

	billedCents := result.ActualCents
	if billedCents <= 0 {
		billedCents = task.EstimatedCents
	}
	if billedCents > task.EstimatedCents {
		billedCents = task.EstimatedCents
	}
	actual := centsToUSD(billedCents)
	endpoint := durableVideoSubmitEndpoint(task)
	_, err = s.settler.Settle(ctx, billing.SettleRequest{
		ClaimID: claimID, TenantID: task.TenantID, APIKeyID: task.APIKeyID, UserID: task.UserID,
		AccountID: task.ProviderAccountID, ProviderAccountID: task.ProviderAccountID,
		AcquisitionToken: acquisitionToken, ActualCost: actual,
		RequestedModel: task.RequestedModel, UpstreamModel: task.ProviderModelID,
		Provider: task.Provider, RequestedAt: task.CreatedAt, Stream: false,
		Fingerprint: payloadHash(task.InputParams), AuditRequestID: task.RequestID,
		AuditRouteID: task.RouteID, AuditPoolGroupID: task.PoolGroupID,
		AuditProviderEndpoint: endpoint,
		Draft: gateway.UsageRecordDraft{
			ActualCost: actual, RoutingReason: append([]byte(nil), result.RoutingReason...),
			EndClass: gateway.StreamEndGraceful, UsageSource: gateway.UsageSourceReported,
		},
	})
	if err != nil {
		if errors.Is(err, billing.ErrClaimNotReserving) {
			status, _, stateErr := s.claimSettlementState(ctx, task.TenantID, claimID)
			if stateErr == nil && status == "committed" {
				return s.markTaskSucceeded(ctx, task.ID, result, now)
			}
		}
		return false, err
	}
	return s.markTaskSucceeded(ctx, task.ID, result, now)
}

func (s *PostgresStore) persistPendingSuccess(ctx context.Context, task Task, owner string, result PollResult, now time.Time) error {
	actualCents := result.ActualCents
	tag, err := s.pool.Exec(ctx, `
UPDATE media_tasks
SET status='settlement_pending', result=$3, actual_cents=$4, progress=99, updated_at=$5
WHERE id=$1 AND lease_owner=$2 AND status IN ('in_progress','settlement_pending')`,
		task.ID, owner, jsonOrNull(result.Result), actualCents, now.UTC())
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrLeaseLost
	}
	return nil
}

func (s *PostgresStore) claimSettlementState(ctx context.Context, tenantID, claimID int64) (string, uuid.UUID, error) {
	var status string
	var token pgtype.UUID
	if err := s.pool.QueryRow(ctx, `
SELECT status, acquisition_token
FROM billing_ledger_claims
WHERE tenant_id=$1 AND id=$2`, tenantID, claimID).Scan(&status, &token); err != nil {
		return "", uuid.Nil, err
	}
	if !token.Valid {
		return status, uuid.Nil, nil
	}
	return status, uuid.UUID(token.Bytes), nil
}

func (s *PostgresStore) markTaskSucceeded(ctx context.Context, taskID int64, result PollResult, now time.Time) (bool, error) {
	tag, err := s.pool.Exec(ctx, `
UPDATE media_tasks
SET status='succeeded', result=$2, actual_cents=$3, progress=100,
    lease_owner=NULL, lease_expires_at=NULL, updated_at=$4, finished_at=$4
WHERE id=$1 AND status IN ('queued','in_progress','settlement_pending')`,
		taskID, jsonOrNull(result.Result), result.ActualCents, now.UTC())
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

func (s *PostgresStore) finishSuccessAfterSweptClaim(ctx context.Context, task Task, owner string, result PollResult, now time.Time) (bool, error) {
	var completed bool
	err := s.withSerializableRetry(ctx, func(tx pgx.Tx) error {
		if err := updateTaskSuccess(ctx, tx, task.ID, result, now); err != nil {
			return err
		}
		if err := persistOrphanTx(ctx, tx, task, owner, now); err != nil {
			return err
		}
		completed = true
		return nil
	})
	return completed, err
}
