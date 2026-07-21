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
	"github.com/shopspring/decimal"

	"github.com/BloomingProsperity/HUAKAI/internal/billing"
	dbbilling "github.com/BloomingProsperity/HUAKAI/internal/db/billing"
)

// persistOrphanTx 在调用方事务内幂等落一条孤儿对账线索；没有上游任务 ID 时无需记录。
func persistOrphanTx(ctx context.Context, tx pgx.Tx, task Task, owner string, now time.Time) error {
	providerTaskID := strings.TrimSpace(task.ProviderTaskID)
	if providerTaskID == "" {
		return nil
	}
	_, err := tx.Exec(ctx, insertOrphanSQL,
		task.ID, task.TenantID, task.UserID, task.Provider,
		providerTaskID, owner, now.UTC())
	return err
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
		NormalizedPayloadHash: payloadHash(input.InputParams), RequestedModel: firstNonEmpty(input.RequestedModel, input.TaskType),
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
