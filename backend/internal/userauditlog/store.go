// Package userauditlog persists append-only user-facing audit events for
// self-service API key management.
package userauditlog

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	ActionIssueAPIKey  = "issue_api_key"
	ActionRevokeAPIKey = "revoke_api_key"

	OutcomeCommitted = "committed"
	OutcomeDenied    = "denied"
	OutcomeError     = "error"
)

const (
	PageLimitDefault = 50
	PageLimitMax     = 200
)

var (
	ErrInvalidRequest = errors.New("userauditlog: invalid request")
	ErrMisconfigured  = errors.New("userauditlog: store not configured")
	ErrBackend        = errors.New("userauditlog: backend datastore error")
)

// Event is the append-only row written by the userkey service.
//
// It intentionally has no plaintext or key_hash fields. Callers may pass only
// KeyPrefix for API key correlation.
type Event struct {
	TenantID   int64
	UserID     int64
	Action     string
	Outcome    string
	APIKeyID   *int64
	KeyPrefix  string
	Reason     string
	RequestID  string
	OccurredAt time.Time
}

// EventRecord is the persisted read model returned to a session user.
type EventRecord struct {
	ID         int64
	TenantID   int64
	UserID     int64
	Action     string
	Outcome    string
	APIKeyID   *int64
	KeyPrefix  string
	Reason     string
	RequestID  string
	OccurredAt time.Time
}

type ListRequest struct {
	TenantID int64
	UserID   int64
	Limit    int
	Offset   int
}

// UserAuditSink is the write-side dependency injected into userkey.Service.
type UserAuditSink interface {
	Record(ctx context.Context, event Event) error
}

// NoopSink preserves userkey behavior when durable audit storage is not wired.
type NoopSink struct{}

func (NoopSink) Record(context.Context, Event) error { return nil }

type PostgresStore struct {
	pool *pgxpool.Pool
}

func NewPostgresStore(pool *pgxpool.Pool) *PostgresStore {
	return &PostgresStore{pool: pool}
}

func (s *PostgresStore) Record(ctx context.Context, event Event) error {
	if s == nil || s.pool == nil {
		return ErrMisconfigured
	}
	if event.TenantID <= 0 || event.UserID <= 0 || event.Action == "" || event.Outcome == "" {
		return ErrInvalidRequest
	}
	_, err := s.pool.Exec(ctx,
		`INSERT INTO user_audit_events (
		     tenant_id, user_id, action, outcome, api_key_id,
		     key_prefix, reason, request_id
		   )
		   VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		event.TenantID, event.UserID, event.Action, event.Outcome,
		nullableInt64(event.APIKeyID), nullableString(event.KeyPrefix),
		nullableString(event.Reason), nullableString(event.RequestID),
	)
	if err != nil {
		return fmt.Errorf("%w: record: %v", ErrBackend, err)
	}
	return nil
}

func (s *PostgresStore) List(ctx context.Context, req ListRequest) ([]EventRecord, error) {
	if s == nil || s.pool == nil {
		return nil, ErrMisconfigured
	}
	if req.TenantID <= 0 || req.UserID <= 0 {
		return nil, ErrInvalidRequest
	}
	limit := req.Limit
	if limit <= 0 {
		limit = PageLimitDefault
	}
	if limit > PageLimitMax {
		limit = PageLimitMax
	}
	offset := req.Offset
	if offset < 0 {
		offset = 0
	}
	rows, err := s.pool.Query(ctx,
		`SELECT id, tenant_id, user_id, action, outcome, api_key_id,
		        key_prefix, reason, request_id, occurred_at
		   FROM user_audit_events
		  WHERE tenant_id = $1 AND user_id = $2
		  ORDER BY occurred_at ASC, id ASC
		  LIMIT $3 OFFSET $4`,
		req.TenantID, req.UserID, int32(limit), int32(offset),
	)
	if err != nil {
		return nil, fmt.Errorf("%w: list: %v", ErrBackend, err)
	}
	defer rows.Close()
	out := make([]EventRecord, 0)
	for rows.Next() {
		rec, err := scanEventRecord(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, rec)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("%w: rows: %v", ErrBackend, err)
	}
	return out, nil
}

func scanEventRecord(row pgx.Row) (EventRecord, error) {
	var (
		rec       EventRecord
		apiKeyID  pgtype.Int8
		keyPrefix *string
		reason    *string
		requestID *string
	)
	if err := row.Scan(&rec.ID, &rec.TenantID, &rec.UserID, &rec.Action, &rec.Outcome,
		&apiKeyID, &keyPrefix, &reason, &requestID, &rec.OccurredAt); err != nil {
		return EventRecord{}, fmt.Errorf("%w: scan: %v", ErrBackend, err)
	}
	if apiKeyID.Valid {
		v := apiKeyID.Int64
		rec.APIKeyID = &v
	}
	if keyPrefix != nil {
		rec.KeyPrefix = *keyPrefix
	}
	if reason != nil {
		rec.Reason = *reason
	}
	if requestID != nil {
		rec.RequestID = *requestID
	}
	return rec, nil
}

func nullableInt64(v *int64) any {
	if v == nil || *v == 0 {
		return nil
	}
	return *v
}

func nullableString(v string) any {
	if v == "" {
		return nil
	}
	return v
}

var _ UserAuditSink = NoopSink{}
var _ UserAuditSink = (*PostgresStore)(nil)
