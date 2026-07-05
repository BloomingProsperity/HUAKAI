package dlq

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	legacydlq "github.com/BloomingProsperity/HUAKAI/internal/dlq"
	"github.com/jackc/pgx/v5"
)

var ErrReplayConflict = errors.New("obsdlq: replay conflict")

type AdminListFilter struct {
	TenantID  *int64
	EventType *string
	From      *time.Time
	To        *time.Time
	Limit     int
}

type AdminDeadEvent struct {
	ID            string          `json:"id"`
	OutboxEventID string          `json:"outbox_event_id"`
	TenantID      int64           `json:"tenant_id"`
	EventType     string          `json:"event_type"`
	Priority      Priority        `json:"priority"`
	Payload       json.RawMessage `json:"payload"`
	DeadAt        time.Time       `json:"dead_at"`
	DeadReason    string          `json:"dead_reason"`
	AttemptCount  int             `json:"attempt_count"`
	OutboxStatus  Status          `json:"outbox_status"`
	FailureReason string          `json:"failure_reason,omitempty"`
	CreatedAt     time.Time       `json:"created_at"`
	NextRetryAt   time.Time       `json:"next_retry_at"`
}

type AdminReplayResult struct {
	DLQEventID    string `json:"id"`
	OutboxEventID string `json:"outbox_event_id"`
}

type AdminPriorityCount struct {
	Priority Priority
	Count    int64
}

func (p *PostgresOutbox) ListDead(ctx context.Context, filter AdminListFilter) ([]AdminDeadEvent, error) {
	if p == nil || p.pool == nil {
		return nil, ErrOutboxNotConfigured
	}
	limit := filter.Limit
	if limit <= 0 {
		limit = 100
	}
	if limit > 200 {
		limit = 200
	}
	rows, err := p.pool.Query(ctx, `
	SELECT de.id,
	       de.outbox_event_id,
	       de.tenant_id,
	       oe.event_type,
	       oe.priority,
	       de.payload,
	       de.dead_at,
	       de.dead_reason,
	       oe.attempt_count,
	       oe.status,
	       COALESCE(oe.failure_reason, ''),
	       oe.created_at,
	       oe.next_retry_at
	FROM dlq_events de
	JOIN outbox_events oe ON oe.id = de.outbox_event_id
	                  AND oe.tenant_id = de.tenant_id
	WHERE ($1::bigint IS NULL OR de.tenant_id = $1::bigint)
	  AND ($2::text IS NULL OR oe.event_type = $2::text)
	  AND ($3::timestamptz IS NULL OR de.dead_at >= $3::timestamptz)
	  AND ($4::timestamptz IS NULL OR de.dead_at <= $4::timestamptz)
	ORDER BY de.dead_at DESC, de.id DESC
	LIMIT $5`, filter.TenantID, filter.EventType, filter.From, filter.To, limit)
	if err != nil {
		return nil, fmt.Errorf("obsdlq: list dead: %w", err)
	}
	defer rows.Close()

	out := []AdminDeadEvent{}
	for rows.Next() {
		var row AdminDeadEvent
		if err := rows.Scan(
			&row.ID,
			&row.OutboxEventID,
			&row.TenantID,
			&row.EventType,
			&row.Priority,
			&row.Payload,
			&row.DeadAt,
			&row.DeadReason,
			&row.AttemptCount,
			&row.OutboxStatus,
			&row.FailureReason,
			&row.CreatedAt,
			&row.NextRetryAt,
		); err != nil {
			return nil, fmt.Errorf("obsdlq: scan dead: %w", err)
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("obsdlq: iterate dead: %w", err)
	}
	return out, nil
}

func (p *PostgresOutbox) ReplayDead(ctx context.Context, id string) (AdminReplayResult, error) {
	if p == nil || p.pool == nil {
		return AdminReplayResult{}, ErrOutboxNotConfigured
	}
	var result AdminReplayResult
	tag, err := p.pool.Exec(ctx, `
	UPDATE outbox_events
	SET status = 'pending',
	    attempt_count = 0,
	    next_retry_at = now()
	WHERE id = (
	    SELECT outbox_event_id
	    FROM dlq_events
	    WHERE id = $1
	)
	  AND status = 'failed_dead'`, id)
	if err != nil {
		return AdminReplayResult{}, fmt.Errorf("obsdlq: replay dead: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return AdminReplayResult{}, ErrReplayConflict
	}
	if err := p.pool.QueryRow(ctx, `SELECT id, outbox_event_id FROM dlq_events WHERE id = $1`, id).Scan(&result.DLQEventID, &result.OutboxEventID); err != nil {
		if err == pgx.ErrNoRows {
			return AdminReplayResult{}, ErrReplayConflict
		}
		return AdminReplayResult{}, fmt.Errorf("obsdlq: replay dead result: %w", err)
	}
	return result, nil
}

func (p *PostgresOutbox) CountDeadByPriority(ctx context.Context) ([]AdminPriorityCount, error) {
	if p == nil || p.pool == nil {
		return nil, ErrOutboxNotConfigured
	}
	rows, err := p.pool.Query(ctx, `
	SELECT oe.priority, count(*)::bigint
	FROM dlq_events de
	JOIN outbox_events oe ON oe.id = de.outbox_event_id
	WHERE oe.status = 'failed_dead'
	GROUP BY oe.priority`)
	if err != nil {
		return nil, fmt.Errorf("obsdlq: count dead by priority: %w", err)
	}
	defer rows.Close()
	var out []AdminPriorityCount
	for rows.Next() {
		var item AdminPriorityCount
		if err := rows.Scan(&item.Priority, &item.Count); err != nil {
			return nil, fmt.Errorf("obsdlq: scan dead count: %w", err)
		}
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("obsdlq: iterate dead count: %w", err)
	}
	return out, nil
}

func (p *PostgresOutbox) UpdateDLQDepthGauge(ctx context.Context) error {
	counts, err := p.CountDeadByPriority(ctx)
	if err != nil {
		return err
	}
	totals := map[Priority]int64{PriorityCritical: 0, PriorityHigh: 0, PriorityDefault: 0}
	for _, count := range counts {
		totals[count.Priority] += count.Count
	}
	for priority, count := range totals {
		legacydlq.SetDLQDepthGauge("depth_OBS_"+strings.ToUpper(string(priority)), count)
	}
	return nil
}
