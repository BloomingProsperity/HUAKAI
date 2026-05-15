package dlq

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/jackc/pgx/v5/pgxpool"
)

type BillingEventReplicaPayload struct {
	BillingEventID   int64   `json:"billing_event_id"`
	TenantID         int64   `json:"tenant_id"`
	ClaimID          int64   `json:"claim_id"`
	EventType        string  `json:"event_type"`
	ActualCost       string  `json:"actual_cost"`
	ActualCostSigned string  `json:"actual_cost_signed"`
	EndClass         *string `json:"end_class,omitempty"`
	UsageSource      *string `json:"usage_source,omitempty"`
	Fingerprint      string  `json:"fingerprint"`
	OccurredAt       string  `json:"occurred_at"`
}

func NewPostgresReplicaHandler(pool *pgxpool.Pool) Handler {
	var once sync.Once
	var ensureErr error
	return func(ctx context.Context, rec Record) error {
		if pool == nil {
			return ErrStoreNotConfigured
		}
		once.Do(func() {
			_, ensureErr = pool.Exec(ctx, `
CREATE TABLE IF NOT EXISTS huakai_observability_replica_events (
	id bigserial PRIMARY KEY,
	idempotency_key text NOT NULL UNIQUE,
	tenant_id bigint NOT NULL,
	event_kind text NOT NULL,
	source_table text NOT NULL,
	source_id bigint,
	payload jsonb NOT NULL,
	received_at timestamptz NOT NULL DEFAULT now()
)`)
		})
		if ensureErr != nil {
			return fmt.Errorf("dlq: ensure replica table: %w", ensureErr)
		}
		_, err := pool.Exec(ctx, `
INSERT INTO huakai_observability_replica_events (
	idempotency_key, tenant_id, event_kind, source_table, source_id, payload
) VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (idempotency_key) DO NOTHING`,
			rec.IdempotencyKey, rec.TenantID, string(rec.EventKind), rec.SourceTable, rec.SourceID, []byte(rec.Payload),
		)
		if err != nil {
			return fmt.Errorf("dlq: write replica event: %w", err)
		}
		return nil
	}
}

func NewLazyPostgresReplicaHandler(dsn string) (Handler, func()) {
	dsn = strings.TrimSpace(dsn)
	var once sync.Once
	var pool *pgxpool.Pool
	var inner Handler
	var openErr error
	closeFn := func() {
		if pool != nil {
			pool.Close()
		}
	}
	handler := func(ctx context.Context, rec Record) error {
		if dsn == "" {
			return ErrStoreNotConfigured
		}
		once.Do(func() {
			pool, openErr = pgxpool.New(ctx, dsn)
			if openErr == nil {
				inner = NewPostgresReplicaHandler(pool)
			}
		})
		if openErr != nil {
			return fmt.Errorf("dlq: open replica pool: %w", openErr)
		}
		return inner(ctx, rec)
	}
	return handler, closeFn
}
