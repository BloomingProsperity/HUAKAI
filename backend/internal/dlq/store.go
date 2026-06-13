package dlq

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Store struct {
	pool *pgxpool.Pool
}

var ErrStaleLease = errors.New("dlq: stale lease")

type queryer interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

type scanner interface {
	Scan(...any) error
}

type ListFilter struct {
	EventKind EventKind
	Status    Status
	TenantID  *int64
	Limit     int
}

const recordColumnsDLQ = `
	d.id, d.tenant_id, d.claim_id, d.payload, d.failure_reason, d.failure_at,
	d.replay_attempts, d.last_replay_at, d.replayed_at, d.replay_failure_reason,
	d.event_kind, d.lane, d.status, d.next_retry_at, d.lease_owner, d.lease_until,
	d.replica_status, d.replica_target, d.replica_committed_at, d.idempotency_key,
	d.source_table, d.source_id, d.operator_review_at`

func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

func (s *Store) Enqueue(ctx context.Context, e Event) (int64, error) {
	if s == nil || s.pool == nil {
		return 0, ErrStoreNotConfigured
	}
	return enqueue(ctx, s.pool, normalizeEvent(e))
}

func (s *Store) EnqueueTx(ctx context.Context, tx pgx.Tx, e Event) (int64, error) {
	if tx == nil {
		return 0, ErrStoreNotConfigured
	}
	return enqueue(ctx, tx, normalizeEvent(e))
}

func (s *Store) List(ctx context.Context, f ListFilter) ([]Record, error) {
	if s == nil || s.pool == nil {
		return nil, ErrStoreNotConfigured
	}
	limit := f.Limit
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	rows, err := s.pool.Query(ctx, `
SELECT `+recordColumnsDLQ+`
FROM usage_record_dlq d
WHERE ($1::text = '' OR d.event_kind = $1)
  AND ($2::text = '' OR d.status = $2)
  AND ($3::bigint IS NULL OR d.tenant_id = $3)
ORDER BY d.failure_at DESC, d.id DESC
LIMIT $4`,
		string(f.EventKind), string(f.Status), nullableInt64Ptr(f.TenantID), limit,
	)
	if err != nil {
		return nil, fmt.Errorf("dlq: list: %w", err)
	}
	defer rows.Close()

	var out []Record
	for rows.Next() {
		rec, err := scanRecord(rows)
		if err != nil {
			return nil, fmt.Errorf("dlq: scan list: %w", err)
		}
		out = append(out, rec)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("dlq: iterate list: %w", err)
	}
	return out, nil
}

// GetByID reads a single dead-letter record by its id, scoped to tenantID. It is
// READ-ONLY (no claim, no lease, no state change) — a target lookup for the
// mutating dlq_replay preview/confirm path, where matching by id over a bounded
// List window could miss records older than that window. Returns ErrNotFound
// when no row with that id belongs to tenantID, so a wrong-tenant id cannot
// resolve another tenant's record (tenant isolation). The actual replay still
// runs through Claim/Replay; this only resolves the target for the preview.
func (s *Store) GetByID(ctx context.Context, tenantID, id int64) (Record, error) {
	if s == nil || s.pool == nil {
		return Record{}, ErrStoreNotConfigured
	}
	rec, err := scanRecord(s.pool.QueryRow(ctx, `
SELECT `+recordColumnsDLQ+`
FROM usage_record_dlq d
WHERE d.id = $1
  AND d.tenant_id = $2`,
		id, tenantID,
	))
	if err == pgx.ErrNoRows {
		return Record{}, ErrNotFound
	}
	if err != nil {
		return Record{}, fmt.Errorf("dlq: get by id: %w", err)
	}
	return rec, nil
}

func (s *Store) Claim(ctx context.Context, lane Lane, workerID string, leaseTTL time.Duration) (*Record, error) {
	if s == nil || s.pool == nil {
		return nil, ErrStoreNotConfigured
	}
	if lane == "" {
		lane = LaneMed
	}
	if leaseTTL <= 0 {
		leaseTTL = 30 * time.Second
	}
	rec, err := scanRecord(s.pool.QueryRow(ctx, `
WITH candidate AS (
	SELECT q.id
	FROM usage_record_dlq q
	WHERE q.lane = $1
	  AND (
		(q.status = 'pending' AND q.next_retry_at <= now())
		OR (q.status = 'inflight' AND q.lease_until < now())
	  )
	ORDER BY q.next_retry_at ASC, q.failure_at ASC, q.id ASC
	LIMIT 1
	FOR UPDATE SKIP LOCKED
)
UPDATE usage_record_dlq d
SET status = 'inflight',
    lease_owner = $2,
    lease_ttl = $3::interval,
    lease_until = now() + $3::interval,
    last_replay_at = now(),
    updated_at = now()
FROM candidate
WHERE d.id = candidate.id
RETURNING `+recordColumnsDLQ,
		string(lane), workerID, intervalLiteral(leaseTTL),
	))
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("dlq: claim %s: %w", lane, err)
	}
	return &rec, nil
}

func (s *Store) ClaimByID(ctx context.Context, id int64, actorID string, leaseTTL time.Duration) (*Record, error) {
	if s == nil || s.pool == nil {
		return nil, ErrStoreNotConfigured
	}
	if leaseTTL <= 0 {
		leaseTTL = 30 * time.Second
	}
	rec, err := scanRecord(s.pool.QueryRow(ctx, `
UPDATE usage_record_dlq d
SET status = 'inflight',
    lane = 'HIGH',
    lease_owner = $2,
    lease_ttl = $3::interval,
    lease_until = now() + $3::interval,
    last_replay_at = now(),
    updated_at = now()
WHERE d.id = $1
  AND d.status <> 'delivered'
  AND (d.status <> 'inflight' OR d.lease_until < now())
RETURNING `+recordColumnsDLQ,
		id, "manual:"+actorID, intervalLiteral(leaseTTL),
	))
	if err == pgx.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("dlq: claim by id: %w", err)
	}
	return &rec, nil
}

func (s *Store) MarkDelivered(ctx context.Context, rec Record) error {
	if s == nil || s.pool == nil {
		return ErrStoreNotConfigured
	}
	leaseOwner, leaseUntil, err := requiredLeaseFence(rec)
	if err != nil {
		return err
	}
	tag, err := s.pool.Exec(ctx, `
	UPDATE usage_record_dlq
	SET status = 'delivered',
	    replayed_at = now(),
    replay_failure_reason = NULL,
    lease_owner = NULL,
    lease_until = NULL,
    replica_status = CASE
        WHEN event_kind IN ('billing_event_replica', 'audit_event_replica') THEN 'delivered'
        ELSE replica_status
    END,
    replica_committed_at = CASE
        WHEN event_kind IN ('billing_event_replica', 'audit_event_replica') THEN now()
        ELSE replica_committed_at
	    END,
	    updated_at = now()
	WHERE id = $1
	  AND status = 'inflight'
	  AND lease_owner = $2
	  AND lease_until = $3`, rec.ID, leaseOwner, leaseUntil)
	if err != nil {
		return fmt.Errorf("dlq: mark delivered: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return fmt.Errorf("%w: mark delivered id=%d", ErrStaleLease, rec.ID)
	}
	return nil
}

func (s *Store) MarkFailed(ctx context.Context, rec Record, reason string, decision RetryDecision) error {
	if s == nil || s.pool == nil {
		return ErrStoreNotConfigured
	}
	leaseOwner, leaseUntil, err := requiredLeaseFence(rec)
	if err != nil {
		return err
	}
	var next any
	if decision.Status == StatusPending {
		next = decision.NextRetryAt.UTC()
	}
	tag, err := s.pool.Exec(ctx, `
	UPDATE usage_record_dlq
	SET status = $2,
	    replay_attempts = $3,
    next_retry_at = COALESCE($4::timestamptz, next_retry_at),
    replay_failure_reason = $5,
    replica_status = CASE
        WHEN event_kind IN ('billing_event_replica', 'audit_event_replica') THEN 'failed'
        ELSE replica_status
    END,
    operator_review_at = CASE
        WHEN $2 IN ('operator_review', 'dlq') THEN now()
        ELSE operator_review_at
    END,
	    lease_owner = NULL,
	    lease_until = NULL,
	    updated_at = now()
	WHERE id = $1
	  AND status = 'inflight'
	  AND lease_owner = $6
	  AND lease_until = $7`, rec.ID, string(decision.Status), decision.Attempts, next, reason, leaseOwner, leaseUntil)
	if err != nil {
		return fmt.Errorf("dlq: mark failed: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return fmt.Errorf("%w: mark failed id=%d", ErrStaleLease, rec.ID)
	}
	return nil
}

func requiredLeaseFence(rec Record) (string, pgtype.Timestamptz, error) {
	if rec.LeaseOwner == nil || *rec.LeaseOwner == "" {
		return "", pgtype.Timestamptz{}, fmt.Errorf("%w: record id=%d has no lease owner", ErrStaleLease, rec.ID)
	}
	if !rec.LeaseUntil.Valid {
		return "", pgtype.Timestamptz{}, fmt.Errorf("%w: record id=%d has no lease deadline", ErrStaleLease, rec.ID)
	}
	return *rec.LeaseOwner, rec.LeaseUntil, nil
}

func normalizeEvent(e Event) Event {
	if e.EventKind == "" {
		e.EventKind = EventKindUsageRecord
	}
	if e.Lane == "" {
		e.Lane = LaneForKind(e.EventKind)
	}
	if len(e.Payload) == 0 {
		e.Payload = json.RawMessage(`{}`)
	}
	if e.FailureReason == "" {
		e.FailureReason = "queued"
	}
	if e.ReplicaTarget == "" {
		e.ReplicaTarget = "primary"
	}
	if e.ReplicaStatus == "" {
		e.ReplicaStatus = ReplicaStatusForKind(e.EventKind)
	}
	if e.SourceTable == "" {
		e.SourceTable = "usage_records"
	}
	if e.NextRetryAt.IsZero() {
		e.NextRetryAt = time.Now().UTC()
	}
	if e.LeaseTTL <= 0 {
		e.LeaseTTL = 30 * time.Second
	}
	return e
}

func enqueue(ctx context.Context, q queryer, e Event) (int64, error) {
	if e.TenantID <= 0 || e.IdempotencyKey == "" || !json.Valid(e.Payload) {
		return 0, ErrInvalidEvent
	}
	var id int64
	err := q.QueryRow(ctx, `
INSERT INTO usage_record_dlq (
	tenant_id, claim_id, payload, failure_reason, event_kind, lane, status,
	next_retry_at, lease_ttl, replica_status, replica_target, idempotency_key,
	source_table, source_id
) VALUES (
	$1, $2, $3, $4, $5, $6, 'pending',
	$7, $8::interval, $9, $10, $11,
	$12, $13
)
ON CONFLICT (tenant_id, event_kind, idempotency_key, replica_target)
DO UPDATE SET
	replay_failure_reason = EXCLUDED.failure_reason,
	next_retry_at = LEAST(usage_record_dlq.next_retry_at, EXCLUDED.next_retry_at),
	updated_at = now()
RETURNING id`,
		e.TenantID,
		nullableInt64(e.ClaimID),
		[]byte(e.Payload),
		e.FailureReason,
		string(e.EventKind),
		string(e.Lane),
		e.NextRetryAt.UTC(),
		intervalLiteral(e.LeaseTTL),
		e.ReplicaStatus,
		e.ReplicaTarget,
		e.IdempotencyKey,
		e.SourceTable,
		nullableInt64(e.SourceID),
	).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("dlq: enqueue: %w", err)
	}
	return id, nil
}

func scanRecord(s scanner) (Record, error) {
	var rec Record
	var kind, lane, status string
	err := s.Scan(
		&rec.ID,
		&rec.TenantID,
		&rec.ClaimID,
		&rec.Payload,
		&rec.FailureReason,
		&rec.FailureAt,
		&rec.ReplayAttempts,
		&rec.LastReplayAt,
		&rec.ReplayedAt,
		&rec.ReplayFailureReason,
		&kind,
		&lane,
		&status,
		&rec.NextRetryAt,
		&rec.LeaseOwner,
		&rec.LeaseUntil,
		&rec.ReplicaStatus,
		&rec.ReplicaTarget,
		&rec.ReplicaCommittedAt,
		&rec.IdempotencyKey,
		&rec.SourceTable,
		&rec.SourceID,
		&rec.OperatorReviewAt,
	)
	rec.EventKind = EventKind(kind)
	rec.Lane = Lane(lane)
	rec.Status = Status(status)
	return rec, err
}

func nullableInt64(v int64) any {
	if v == 0 {
		return nil
	}
	return v
}

func nullableInt64Ptr(v *int64) any {
	if v == nil || *v == 0 {
		return nil
	}
	return *v
}

func intervalLiteral(d time.Duration) string {
	seconds := d.Seconds()
	if seconds <= 0 {
		seconds = 30
	}
	return fmt.Sprintf("%.9f seconds", seconds)
}
