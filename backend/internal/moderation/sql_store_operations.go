package moderation

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	dbmoderation "github.com/BloomingProsperity/HUAKAI/internal/db/moderation"
)

type moderationKeyOperationInput struct {
	action             string
	tenantID           int64
	apiKeyID           int64
	violationEventID   int64
	idempotencyKey     string
	requestFingerprint string
	actorID            string
	actorRole          string
}

func newModerationKeyOperationInput(
	action string,
	tenantID int64,
	apiKeyID int64,
	violationEventID int64,
	idempotencyKey string,
	actorID string,
	actorRole string,
	reason string,
) (moderationKeyOperationInput, error) {
	idempotencyKey = strings.TrimSpace(idempotencyKey)
	actorID = strings.TrimSpace(actorID)
	actorRole = strings.TrimSpace(actorRole)
	if tenantID <= 0 || apiKeyID <= 0 || actorID == "" || actorRole == "" ||
		idempotencyKey == "" || len(idempotencyKey) > 256 {
		return moderationKeyOperationInput{}, ErrInvalidEvent
	}
	if action != "disable" && action != "unban" {
		return moderationKeyOperationInput{}, ErrInvalidEvent
	}
	if (action == "disable" && violationEventID <= 0) ||
		(action == "unban" && violationEventID != 0) {
		return moderationKeyOperationInput{}, ErrInvalidEvent
	}
	canonical := strings.Join([]string{
		"moderation-key-operation-v1",
		action,
		strconv.FormatInt(tenantID, 10),
		strconv.FormatInt(apiKeyID, 10),
		strconv.FormatInt(violationEventID, 10),
		actorID,
		actorRole,
		strings.TrimSpace(reason),
	}, "\x00")
	sum := sha256.Sum256([]byte(canonical))
	return moderationKeyOperationInput{
		action:             action,
		tenantID:           tenantID,
		apiKeyID:           apiKeyID,
		violationEventID:   violationEventID,
		idempotencyKey:     idempotencyKey,
		requestFingerprint: hex.EncodeToString(sum[:]),
		actorID:            actorID,
		actorRole:          actorRole,
	}, nil
}

func loadModerationKeyOperation(
	ctx context.Context,
	q *dbmoderation.Queries,
	input moderationKeyOperationInput,
) (dbmoderation.ModerationKeyOperation, bool, error) {
	lockKey := strings.Join([]string{
		"moderation-key-operation",
		strconv.FormatInt(input.tenantID, 10),
		input.idempotencyKey,
	}, ":")
	if err := q.AcquireModerationKeyOperationLock(ctx, lockKey); err != nil {
		return dbmoderation.ModerationKeyOperation{}, false, err
	}
	row, err := q.GetModerationKeyOperation(ctx, dbmoderation.GetModerationKeyOperationParams{
		TenantID: input.tenantID, IdempotencyKey: input.idempotencyKey,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return dbmoderation.ModerationKeyOperation{}, false, nil
	}
	if err != nil {
		return dbmoderation.ModerationKeyOperation{}, false, err
	}
	if row.RequestFingerprint != input.requestFingerprint {
		return dbmoderation.ModerationKeyOperation{}, false, ErrStateConflict
	}
	return row, true, nil
}

func insertModerationKeyOperation(
	ctx context.Context,
	q *dbmoderation.Queries,
	input moderationKeyOperationInput,
	logID int64,
	status string,
	generation int64,
	updatedAt time.Time,
) error {
	var violationEventID *int64
	if input.violationEventID > 0 {
		violationEventID = &input.violationEventID
	}
	_, err := q.InsertModerationKeyOperation(ctx, dbmoderation.InsertModerationKeyOperationParams{
		TenantID: input.tenantID, APIKeyID: input.apiKeyID,
		IdempotencyKey: input.idempotencyKey, RequestFingerprint: input.requestFingerprint,
		Action: input.action, ViolationEventID: violationEventID,
		ActorID: input.actorID, ActorRole: input.actorRole,
		ResultStatus: status, ResultLogID: logID, ResultGeneration: generation,
		ResultUpdatedAt: pgtype.Timestamptz{Time: updatedAt, Valid: true},
	})
	return err
}

func unbanResultFromOperation(row dbmoderation.ModerationKeyOperation) UnbanAPIKeyResult {
	return UnbanAPIKeyResult{
		APIKeyID: row.APIKeyID, TenantID: row.TenantID,
		Status: row.ResultStatus, AuditLogID: row.ResultLogID,
		UpdatedAt: timeFromPG(row.ResultUpdatedAt),
	}
}

func disableResultFromOperation(row dbmoderation.ModerationKeyOperation) DisableAPIKeyResult {
	return DisableAPIKeyResult{
		APIKeyID: row.APIKeyID, TenantID: row.TenantID,
		Status: row.ResultStatus, LogID: row.ResultLogID,
		UpdatedAt: timeFromPG(row.ResultUpdatedAt),
	}
}
