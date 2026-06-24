package mediatask

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/shopspring/decimal"

	"github.com/BloomingProsperity/HUAKAI/internal/billing"
	dbbilling "github.com/BloomingProsperity/HUAKAI/internal/db/billing"
)

func (s *PostgresStore) CompleteSuccess(ctx context.Context, task Task, owner string, result PollResult, now time.Time) (bool, error) {
	if s == nil || s.pool == nil {
		return false, ErrStoreNotConfigured
	}
	var settled bool
	err := s.withSerializableRetry(ctx, func(tx pgx.Tx) error {
		locked, ok, err := lockTerminalCandidate(ctx, tx, task.ID, owner)
		if err != nil || !ok {
			return err
		}
		if result.ActualCents < 0 {
			return fmt.Errorf("%w: actual_cents", ErrInvalidInput)
		}
		if result.ActualCents > locked.EstimatedCents {
			return ErrActualExceedsEstimate
		}
		claimID, err := claimIDFromHoldRef(locked.HoldRef)
		if err != nil {
			return err
		}
		actual := centsToUSD(result.ActualCents)
		qtx := dbbilling.New(tx)
		if rows, err := qtx.UpdateClaimCommitted(ctx, dbbilling.UpdateClaimCommittedParams{
			ID: claimID, ActualCost: decimal.NullDecimal{Decimal: actual, Valid: true}, TenantID: locked.TenantID,
		}); err != nil {
			return err
		} else if rows == 0 {
			return billing.ErrClaimNotReserving
		}
		if err := insertBillingEvent(ctx, qtx, locked, claimID, "claim_committed", actual, "stream_end_graceful", billing.StreamStatePartial); err != nil {
			return err
		}
		if _, err := billing.Capture(ctx, tx, claimID, actual); err != nil {
			return err
		}
		if err := updateTaskSuccess(ctx, tx, locked.ID, result, now); err != nil {
			return err
		}
		settled = true
		return nil
	})
	return settled, err
}

func (s *PostgresStore) CompleteFailure(ctx context.Context, task Task, owner, errorClass string, now time.Time) (bool, error) {
	return s.abortTask(ctx, task.ID, owner, StatusFailed, firstNonEmpty(errorClass, "provider_failed"), now)
}

func (s *PostgresStore) ExpireTask(ctx context.Context, task Task, owner string, now time.Time) (bool, error) {
	return s.abortTask(ctx, task.ID, owner, StatusExpired, "timeout", now)
}

func (s *PostgresStore) insertReservedTask(ctx context.Context, tx pgx.Tx, input CreateTaskInput) (Task, error) {
	apiKeyID, err := activeAPIKeyID(ctx, tx, input.TenantID, input.UserID, time.Now().UTC())
	if err != nil {
		return Task{}, err
	}
	version := firstNonEmpty(input.BillingPolicyVersion, s.billingVersion)
	if version == "" {
		version = "mediatask-v1"
	}
	requestClass := firstNonEmpty(input.RequestClass, s.requestClass, "standard")
	fingerprint := payloadHash(input.InputParams)
	qtx := dbbilling.New(tx)
	claim, err := qtx.InsertClaim(ctx, dbbilling.InsertClaimParams{
		TenantID: input.TenantID, IdempotencyKey: mediaClaimKey(input, apiKeyID, version, requestClass),
		RequestFingerprint: fingerprint, APIKeyID: apiKeyID, UserID: input.UserID,
		LogicalRequestID: input.RequestID, EndpointFamily: "media_tasks", RequestedModel: input.TaskType,
		BillingPolicyVersion: version, RequestClass: requestClass, PredictedCost: centsToUSD(input.EstimatedCents),
		// claim 孤儿回收租约必须 > 媒体任务最大生命周期(TaskTimeout)。原硬编码 90s 远
		// 短于 TaskTimeout(默认 15min),会让跑得久的合法任务 claim 被 billing LeaseSweeper
		// 提前 abort、完成时无法计费致亏钱。改用 resolveClaimLeaseWindow(覆盖任务生命周期)。
		CurrencyCode: "USD", LeaseExpiresAt: pgtype.Timestamptz{Time: time.Now().UTC().Add(resolveClaimLeaseWindow(input.ClaimLeaseWindow)), Valid: true},
	})
	if err != nil {
		return Task{}, err
	}
	if _, err := billing.Reserve(ctx, tx, billing.ReserveParams{
		TenantID: input.TenantID, UserID: input.UserID, ClaimID: claim.ID,
		Cost: centsToUSD(input.EstimatedCents), EnforcementMode: s.holdMode(ctx, input.TenantID),
	}); err != nil {
		if errors.Is(err, billing.ErrBalanceHoldInsufficientBalance) {
			return Task{}, billing.ErrInsufficientBalance
		}
		return Task{}, err
	}
	if s.beforeInsertTask != nil {
		if err := s.beforeInsertTask(); err != nil {
			return Task{}, err
		}
	}
	return scanTask(tx.QueryRow(ctx, insertTaskSQL,
		input.TenantID, input.UserID, input.TaskType, StatusQueued, input.Provider,
		input.RequestID, input.InputParams, input.EstimatedCents, holdRef(claim.ID),
	))
}

func (s *PostgresStore) abortTask(ctx context.Context, taskID int64, owner string, status Status, errorClass string, now time.Time) (bool, error) {
	if s == nil || s.pool == nil {
		return false, ErrStoreNotConfigured
	}
	var completed bool
	err := s.withSerializableRetry(ctx, func(tx pgx.Tx) error {
		locked, ok, err := lockTerminalCandidate(ctx, tx, taskID, owner)
		if err != nil || !ok {
			return err
		}
		claimID, err := claimIDFromHoldRef(locked.HoldRef)
		if err != nil {
			return err
		}
		qtx := dbbilling.New(tx)
		reason := errorClass
		if rows, err := qtx.UpdateClaimAbortedWithReason(ctx, dbbilling.UpdateClaimAbortedWithReasonParams{
			ID: claimID, TenantID: locked.TenantID, AbortedReason: &reason,
		}); err != nil {
			return err
		} else if rows == 0 {
			return billing.ErrClaimNotReserving
		}
		if err := insertBillingEvent(ctx, qtx, locked, claimID, "claim_aborted", decimal.Zero, reason, billing.StreamStateFailed); err != nil {
			return err
		}
		if _, err := billing.Release(ctx, tx, claimID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
	UPDATE media_tasks
	SET status=$2, error_class=$3, lease_owner=NULL, lease_expires_at=NULL,
	    updated_at=$4, finished_at=$4
	WHERE id=$1 AND status IN ('queued','in_progress')`,
			locked.ID, status, errorClass, now.UTC(),
		); err != nil {
			return err
		}
		completed = true
		return nil
	})
	return completed, err
}

func (s *PostgresStore) holdMode(ctx context.Context, tenantID int64) billing.EnforcementMode {
	mode := billing.DefaultBalanceEnforcementMode
	if s.balanceResolver != nil {
		mode = s.balanceResolver.ResolveBalanceEnforcementMode(ctx, tenantID)
	}
	if mode == billing.BalanceEnforcementModeOptIn {
		return billing.EnforcementModeOptIn
	}
	return billing.EnforcementModeMandatory
}

func activeAPIKeyID(ctx context.Context, tx pgx.Tx, tenantID, userID int64, now time.Time) (int64, error) {
	var id int64
	err := tx.QueryRow(ctx, `
	SELECT id
	FROM api_keys
	WHERE tenant_id=$1 AND user_id=$2 AND status='active' AND deleted_at IS NULL
	  AND (expires_at IS NULL OR expires_at > $3)
	ORDER BY id ASC
	LIMIT 1
	FOR KEY SHARE`, tenantID, userID, now.UTC()).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, ErrNoActiveAPIKey
	}
	return id, err
}

func lockTerminalCandidate(ctx context.Context, tx pgx.Tx, id int64, owner string) (Task, bool, error) {
	task, err := scanTask(tx.QueryRow(ctx, selectTaskSQL+` WHERE id=$1 FOR UPDATE`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return Task{}, false, ErrNotFound
	}
	if err != nil {
		return Task{}, false, err
	}
	if task.LeaseOwner != owner || IsTerminal(task.Status) {
		return task, false, nil
	}
	return task, true, nil
}

func updateTaskSuccess(ctx context.Context, tx pgx.Tx, id int64, result PollResult, now time.Time) error {
	_, err := tx.Exec(ctx, `
	UPDATE media_tasks
	SET status='succeeded', result=$2, actual_cents=$3, progress=100,
	    lease_owner=NULL, lease_expires_at=NULL, updated_at=$4, finished_at=$4
	WHERE id=$1 AND status IN ('queued','in_progress')`,
		id, jsonOrNull(result.Result), result.ActualCents, now.UTC(),
	)
	return err
}

func insertBillingEvent(ctx context.Context, q *dbbilling.Queries, task Task, claimID int64, eventType string, cost decimal.Decimal, endClass string, streamState billing.StreamState) error {
	_, err := q.InsertBillingEvent(ctx, dbbilling.InsertBillingEventParams{
		TenantID: task.TenantID, ClaimID: &claimID, EventType: eventType,
		ActualCost: cost, ActualCostSigned: cost,
		EndClass: &endClass, UsageSource: stringPtr("reported"), StreamState: streamState.DBValue(),
		DeliveredTokenCount: 0, Fingerprint: payloadHash(task.InputParams), AuditRequestID: &task.RequestID,
	})
	return err
}

func mediaClaimKey(input CreateTaskInput, apiKeyID int64, version, requestClass string) string {
	return billing.ComputeIdempotencyFingerprint(billing.ReserveRequest{
		TenantID: input.TenantID, APIKeyID: apiKeyID, UserID: input.UserID,
		LogicalRequestID: input.RequestID, EndpointFamily: "media_tasks",
		NormalizedPayloadHash: payloadHash(input.InputParams), RequestedModel: input.TaskType,
		BillingPolicyVersion: version, RequestClass: requestClass,
	})
}

func payloadHash(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func holdRef(claimID int64) string {
	return "claim:" + strconv.FormatInt(claimID, 10)
}

func claimIDFromHoldRef(ref string) (int64, error) {
	raw := strings.TrimPrefix(strings.TrimSpace(ref), "claim:")
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 {
		return 0, fmt.Errorf("%w: hold_ref", ErrInvalidInput)
	}
	return id, nil
}

func stringPtr(v string) *string {
	return &v
}
