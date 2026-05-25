package dlq

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type PostgresOutbox struct {
	pool *pgxpool.Pool
}

func NewPostgresOutbox(pool *pgxpool.Pool) *PostgresOutbox {
	return &PostgresOutbox{pool: pool}
}

func (p *PostgresOutbox) Enqueue(ctx context.Context, e OutboxEvent) (OutboxEvent, error) {
	if p == nil || p.pool == nil {
		return OutboxEvent{}, ErrOutboxNotConfigured
	}
	e, err := normalizeEvent(e, time.Now().UTC())
	if err != nil {
		return OutboxEvent{}, err
	}
	err = p.pool.QueryRow(ctx, `
INSERT INTO outbox_events (
    id, tenant_id, event_type, priority, payload, created_at,
    attempt_count, next_retry_at, status, failure_reason
) VALUES (
    $1, $2, $3, $4, $5::jsonb, $6,
    $7, $8, $9, $10
)
RETURNING id, tenant_id, event_type, priority, payload, created_at,
          attempt_count, next_retry_at, status, failure_reason`,
		e.ID, e.TenantID, e.EventType, string(e.Priority), []byte(e.Payload), e.CreatedAt,
		e.AttemptCount, e.NextRetryAt, string(e.Status), e.FailureReason,
	).Scan(&e.ID, &e.TenantID, &e.EventType, &e.Priority, &e.Payload, &e.CreatedAt,
		&e.AttemptCount, &e.NextRetryAt, &e.Status, &e.FailureReason)
	if err != nil {
		return OutboxEvent{}, fmt.Errorf("obsdlq: enqueue: %w", err)
	}
	return e, nil
}

func (p *PostgresOutbox) Dequeue(ctx context.Context, opts DequeueOptions) (OutboxEvent, bool, error) {
	if p == nil || p.pool == nil {
		return OutboxEvent{}, false, ErrOutboxNotConfigured
	}
	tx, err := p.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return OutboxEvent{}, false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	now := opts.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}
	visibility := opts.VisibilityTimeout
	if visibility <= 0 {
		visibility = 15 * time.Minute
	}
	workerID := opts.WorkerID
	if workerID == "" {
		workerID = "obsdlq-worker"
	}
	var ev OutboxEvent
	err = tx.QueryRow(ctx, `
WITH ranked AS (
    SELECT q.id
    FROM outbox_events q
    WHERE q.status IN ('pending', 'failed_retry', 'processing')
      AND q.next_retry_at <= $2
      AND ($1::text = '' OR q.priority = $1)
      AND pg_try_advisory_xact_lock(hashtext(q.tenant_id::text),
          CASE q.priority WHEN 'critical' THEN 3 WHEN 'high' THEN 2 ELSE 1 END)
    ORDER BY CASE q.priority WHEN 'critical' THEN 3 WHEN 'high' THEN 2 ELSE 1 END DESC,
             q.next_retry_at ASC, q.created_at ASC, q.id ASC
    LIMIT 1
    FOR UPDATE SKIP LOCKED
)
UPDATE outbox_events q
SET status = 'processing',
    failure_reason = NULLIF($3, ''),
    next_retry_at = $4
FROM ranked
WHERE q.id = ranked.id
RETURNING q.id, q.tenant_id, q.event_type, q.priority, q.payload, q.created_at,
          q.attempt_count, q.next_retry_at, q.status, COALESCE(q.failure_reason, '')`,
		string(opts.Priority), now.UTC(), "processing:"+workerID, now.Add(visibility).UTC(),
	).Scan(&ev.ID, &ev.TenantID, &ev.EventType, &ev.Priority, &ev.Payload, &ev.CreatedAt,
		&ev.AttemptCount, &ev.NextRetryAt, &ev.Status, &ev.FailureReason)
	if err == pgx.ErrNoRows {
		return OutboxEvent{}, false, nil
	}
	if err != nil {
		return OutboxEvent{}, false, fmt.Errorf("obsdlq: dequeue: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return OutboxEvent{}, false, err
	}
	return ev, true, nil
}

func (p *PostgresOutbox) MarkCompleted(ctx context.Context, id string) error {
	if p == nil || p.pool == nil {
		return ErrOutboxNotConfigured
	}
	tag, err := p.pool.Exec(ctx, `
UPDATE outbox_events
SET status = 'completed', failure_reason = NULL
WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("obsdlq: mark completed: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrEventNotFound
	}
	return nil
}

func (p *PostgresOutbox) MarkFailedRetry(ctx context.Context, id, reason string, next time.Time) error {
	if p == nil || p.pool == nil {
		return ErrOutboxNotConfigured
	}
	tag, err := p.pool.Exec(ctx, `
UPDATE outbox_events
SET status = 'failed_retry',
    attempt_count = attempt_count + 1,
    next_retry_at = $2,
    failure_reason = $3
WHERE id = $1`, id, next.UTC(), RedactString(reason))
	if err != nil {
		return fmt.Errorf("obsdlq: mark failed retry: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrEventNotFound
	}
	return nil
}

func (p *PostgresOutbox) MarkFailedDead(ctx context.Context, id, reason string) error {
	if p == nil || p.pool == nil {
		return ErrOutboxNotConfigured
	}
	tx, err := p.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	reason = RedactString(reason)
	var ev OutboxEvent
	err = tx.QueryRow(ctx, `
UPDATE outbox_events
SET status = 'failed_dead',
    attempt_count = attempt_count + 1,
    failure_reason = $2
WHERE id = $1
RETURNING id, tenant_id, event_type, priority, payload, created_at,
          attempt_count, next_retry_at, status, COALESCE(failure_reason, '')`,
		id, reason,
	).Scan(&ev.ID, &ev.TenantID, &ev.EventType, &ev.Priority, &ev.Payload, &ev.CreatedAt,
		&ev.AttemptCount, &ev.NextRetryAt, &ev.Status, &ev.FailureReason)
	if err == pgx.ErrNoRows {
		return ErrEventNotFound
	}
	if err != nil {
		return fmt.Errorf("obsdlq: mark failed dead: %w", err)
	}
	deadID := newEventID()
	if _, err := tx.Exec(ctx, `
INSERT INTO dlq_events (id, outbox_event_id, tenant_id, payload, dead_at, dead_reason)
VALUES ($1, $2, $3, $4::jsonb, now(), $5)`,
		deadID, ev.ID, ev.TenantID, json.RawMessage(ev.Payload), reason,
	); err != nil {
		return fmt.Errorf("obsdlq: insert dlq event: %w", err)
	}
	return tx.Commit(ctx)
}
