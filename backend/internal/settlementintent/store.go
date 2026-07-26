// Package settlementintent 持久化 relay 请求的结算意图，并用乐观锁推进状态。
package settlementintent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/shopspring/decimal"

	dbbilling "github.com/BloomingProsperity/HUAKAI/internal/db/billing"
)

var (
	errStoreNotConfigured    = errors.New("结算意图存储未配置")
	errInvalidRecoveryRecord = errors.New("结算恢复证据无效")
)

// CreateParams 是首字节交付前持久化的请求级结算证据。
type CreateParams struct {
	TenantID           int64
	RequestID          string
	LogicalRequestID   string
	AttemptSeq         int32
	ClaimID            int64
	APIKeyID           int64
	RequestFingerprint string
	PredictedCost      decimal.Decimal
}

// StaleSettlementIntent 是后台对账所需的最小意图快照。
type StaleSettlementIntent struct {
	ID                   int64
	TenantID             int64
	ClaimID              int64
	AttemptSeq           int32
	Version              int32
	Status               string
	ActualCost           decimal.Decimal
	RecoveryPayload      json.RawMessage
	RecoveryFailureClass string
}

// Store 定义结算意图所需的最小持久化接口，便于主链路注入故障测试。
type Store interface {
	Insert(context.Context, CreateParams) (int64, error)
	MarkDelivering(context.Context, int64, int32, time.Time) (int32, error)
	MarkSettling(context.Context, int64, int32, decimal.Decimal) (int32, error)
	MarkSettled(context.Context, int64, int32, decimal.Decimal, time.Time) (int32, error)
	MarkAborted(context.Context, int64, int32) (int32, error)
	MarkFailed(context.Context, int64, int32, decimal.Decimal) (int32, error)
	MarkRecoveryPending(context.Context, int64, int32, decimal.Decimal, json.RawMessage, string) (int32, error)
	ListStaleNonTerminalSettlementIntents(context.Context, time.Time, time.Time, int32) ([]StaleSettlementIntent, error)
	MarkSettledIfStale(context.Context, int64, int32, decimal.Decimal, time.Time) (int32, error)
	MarkAbortedIfStale(context.Context, int64, int32) (int32, error)
	MarkSupersededIfStale(context.Context, int64, int32) (int32, error)
	MarkSettlingIfStale(context.Context, int64, int32) (int32, error)
}

// PostgresStore 把状态迁移委托给 sqlc 查询。
type PostgresStore struct {
	queries *dbbilling.Queries
}

func NewPostgresStore(queries *dbbilling.Queries) *PostgresStore {
	return &PostgresStore{queries: queries}
}

func NewConfiguredPostgresStore(queries *dbbilling.Queries, enabled bool) Store {
	if !enabled {
		return nil
	}
	return NewPostgresStore(queries)
}

func (s *PostgresStore) Insert(ctx context.Context, in CreateParams) (int64, error) {
	if s == nil || s.queries == nil {
		return 0, errStoreNotConfigured
	}
	return s.queries.InsertSettlementIntent(ctx, dbbilling.InsertSettlementIntentParams{
		TenantID:           in.TenantID,
		RequestID:          in.RequestID,
		LogicalRequestID:   optionalString(in.LogicalRequestID),
		AttemptSeq:         in.AttemptSeq,
		ClaimID:            in.ClaimID,
		APIKeyID:           optionalInt64(in.APIKeyID),
		RequestFingerprint: in.RequestFingerprint,
		PredictedCost:      in.PredictedCost,
	})
}

func (s *PostgresStore) MarkDelivering(ctx context.Context, id int64, version int32, firstByteAt time.Time) (int32, error) {
	if s == nil || s.queries == nil {
		return 0, errStoreNotConfigured
	}
	return s.queries.MarkSettlementIntentDelivering(ctx, dbbilling.MarkSettlementIntentDeliveringParams{
		ID:          id,
		Version:     version,
		FirstByteAt: pgtype.Timestamptz{Time: firstByteAt.UTC(), Valid: true},
	})
}

func (s *PostgresStore) MarkSettling(ctx context.Context, id int64, version int32, actualCost decimal.Decimal) (int32, error) {
	if s == nil || s.queries == nil {
		return 0, errStoreNotConfigured
	}
	return s.queries.MarkSettlementIntentSettling(ctx, dbbilling.MarkSettlementIntentSettlingParams{
		ActualCost: actualCost,
		ID:         id,
		Version:    version,
	})
}

func (s *PostgresStore) MarkSettled(ctx context.Context, id int64, version int32, actualCost decimal.Decimal, settledAt time.Time) (int32, error) {
	if s == nil || s.queries == nil {
		return 0, errStoreNotConfigured
	}
	return s.queries.MarkSettlementIntentSettled(ctx, dbbilling.MarkSettlementIntentSettledParams{
		ID:         id,
		Version:    version,
		ActualCost: actualCost,
		SettledAt:  pgtype.Timestamptz{Time: settledAt.UTC(), Valid: true},
	})
}

func (s *PostgresStore) MarkAborted(ctx context.Context, id int64, version int32) (int32, error) {
	if s == nil || s.queries == nil {
		return 0, errStoreNotConfigured
	}
	return s.queries.MarkSettlementIntentAborted(ctx, dbbilling.MarkSettlementIntentAbortedParams{
		ID:      id,
		Version: version,
	})
}

func (s *PostgresStore) MarkFailed(ctx context.Context, id int64, version int32, actualCost decimal.Decimal) (int32, error) {
	if s == nil || s.queries == nil {
		return 0, errStoreNotConfigured
	}
	return s.queries.MarkSettlementIntentFailed(ctx, dbbilling.MarkSettlementIntentFailedParams{
		ActualCost: actualCost,
		ID:         id,
		Version:    version,
	})
}

func (s *PostgresStore) MarkRecoveryPending(
	ctx context.Context,
	id int64,
	version int32,
	actualCost decimal.Decimal,
	payload json.RawMessage,
	failureClass string,
) (int32, error) {
	if s == nil || s.queries == nil {
		return 0, errStoreNotConfigured
	}
	payload = json.RawMessage(bytes.TrimSpace(payload))
	if id <= 0 || version < 0 || len(payload) == 0 || !json.Valid(payload) || payload[0] != '{' {
		return 0, errInvalidRecoveryRecord
	}
	return s.queries.MarkSettlementIntentRecoveryPending(ctx, dbbilling.MarkSettlementIntentRecoveryPendingParams{
		ActualCost:           actualCost,
		RecoveryPayload:      payload,
		RecoveryFailureClass: optionalString(strings.TrimSpace(failureClass)),
		ID:                   id,
		Version:              version,
	})
}

func (s *PostgresStore) ListStaleNonTerminalSettlementIntents(ctx context.Context, staleCutoff, createdBefore time.Time, limit int32) ([]StaleSettlementIntent, error) {
	if s == nil || s.queries == nil {
		return nil, errStoreNotConfigured
	}
	rows, err := s.queries.ListStaleNonTerminalSettlementIntents(ctx, dbbilling.ListStaleNonTerminalSettlementIntentsParams{
		StaleCutoff:   pgtype.Timestamptz{Time: staleCutoff.UTC(), Valid: true},
		CreatedBefore: pgtype.Timestamptz{Time: createdBefore.UTC(), Valid: true},
		Lim:           limit,
	})
	if err != nil {
		return nil, err
	}
	intents := make([]StaleSettlementIntent, 0, len(rows))
	for _, row := range rows {
		intents = append(intents, StaleSettlementIntent{
			ID:         row.ID,
			TenantID:   row.TenantID,
			ClaimID:    row.ClaimID,
			AttemptSeq: row.AttemptSeq,
			Version:    row.Version,
			Status:     row.Status,
			ActualCost: row.ActualCost,
			RecoveryPayload: append(
				json.RawMessage(nil),
				row.RecoveryPayload...,
			),
			RecoveryFailureClass: optionalStringValue(row.RecoveryFailureClass),
		})
	}
	return intents, nil
}

func (s *PostgresStore) MarkSettledIfStale(ctx context.Context, id int64, version int32, actualCost decimal.Decimal, settledAt time.Time) (int32, error) {
	if s == nil || s.queries == nil {
		return 0, errStoreNotConfigured
	}
	return s.queries.MarkSettlementIntentSettledIfStale(ctx, dbbilling.MarkSettlementIntentSettledIfStaleParams{
		ActualCost: actualCost,
		SettledAt:  pgtype.Timestamptz{Time: settledAt.UTC(), Valid: true},
		ID:         id,
		Version:    version,
	})
}

func (s *PostgresStore) MarkAbortedIfStale(ctx context.Context, id int64, version int32) (int32, error) {
	if s == nil || s.queries == nil {
		return 0, errStoreNotConfigured
	}
	return s.queries.MarkSettlementIntentAbortedIfStale(ctx, dbbilling.MarkSettlementIntentAbortedIfStaleParams{
		ID:      id,
		Version: version,
	})
}

func (s *PostgresStore) MarkSupersededIfStale(ctx context.Context, id int64, version int32) (int32, error) {
	if s == nil || s.queries == nil {
		return 0, errStoreNotConfigured
	}
	return s.queries.MarkSettlementIntentSupersededIfStale(ctx, dbbilling.MarkSettlementIntentSupersededIfStaleParams{
		ID:      id,
		Version: version,
	})
}

func (s *PostgresStore) MarkSettlingIfStale(ctx context.Context, id int64, version int32) (int32, error) {
	if s == nil || s.queries == nil {
		return 0, errStoreNotConfigured
	}
	return s.queries.MarkSettlementIntentSettlingIfStale(ctx, dbbilling.MarkSettlementIntentSettlingIfStaleParams{
		ID:      id,
		Version: version,
	})
}

func optionalStringValue(value *string) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(*value)
}

func optionalString(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

func optionalInt64(value int64) *int64 {
	if value == 0 {
		return nil
	}
	return &value
}
