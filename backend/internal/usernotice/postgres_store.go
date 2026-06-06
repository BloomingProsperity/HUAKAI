package usernotice

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

type PostgresStore struct {
	pool *pgxpool.Pool
}

func NewPostgresStore(pool *pgxpool.Pool) *PostgresStore {
	return &PostgresStore{pool: pool}
}

func (s *PostgresStore) BroadcastInsert(ctx context.Context, notice Notification, maxRecipients int) (broadcastStoreResult, error) {
	if s == nil || s.pool == nil {
		return broadcastStoreResult{}, ErrStoreNotConfigured
	}
	if maxRecipients <= 0 {
		maxRecipients = defaultBroadcastRecipientLimit
	}
	var inserted int64
	var capped bool
	err := s.pool.QueryRow(ctx, `
WITH recipients AS MATERIALIZED (
    SELECT id
    FROM users
    WHERE tenant_id = $1
      AND status = 'active'
      AND deleted_at IS NULL
    ORDER BY id
    LIMIT $7
),
recipient_guard AS (
    SELECT count(*)::bigint AS recipient_count FROM recipients
),
inserted AS (
    INSERT INTO user_notifications (
        tenant_id, user_id, title, body, severity, created_by_admin, created_at
    )
    SELECT $1, id, $2, $3, $4, $5, $6
    FROM recipients
    WHERE (SELECT recipient_count FROM recipient_guard) <= $8
    RETURNING 1
)
SELECT
    (SELECT count(*)::bigint FROM inserted),
    (SELECT recipient_count FROM recipient_guard) > $8`,
		notice.TenantID,
		notice.Title,
		notice.Body,
		string(notice.Severity),
		notice.CreatedByAdmin,
		notice.CreatedAt,
		maxRecipients+1,
		maxRecipients,
	).Scan(&inserted, &capped)
	if err != nil {
		return broadcastStoreResult{}, err
	}
	return broadcastStoreResult{Inserted: inserted, Capped: capped}, nil
}

func (s *PostgresStore) ListForUser(ctx context.Context, in ListInput) ([]Notification, error) {
	if s == nil || s.pool == nil {
		return nil, ErrStoreNotConfigured
	}
	rows, err := s.pool.Query(ctx, `
SELECT id, tenant_id, user_id, title, body, severity, read_at, created_by_admin, created_at
FROM user_notifications
WHERE tenant_id = $1
  AND user_id = $2
  AND ($3 = false OR read_at IS NULL)
ORDER BY created_at DESC, id DESC
LIMIT $4 OFFSET $5`,
		in.TenantID, in.UserID, in.UnreadOnly, int32(in.Limit), int32(in.Offset))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanNotificationRows(rows)
}

func (s *PostgresStore) MarkRead(ctx context.Context, tenantID, userID, id int64, readAt time.Time) (Notification, error) {
	if s == nil || s.pool == nil {
		return Notification{}, ErrStoreNotConfigured
	}
	notice, err := scanNotification(s.pool.QueryRow(ctx, `
UPDATE user_notifications
SET read_at = COALESCE(read_at, $4)
WHERE tenant_id = $1
  AND user_id = $2
  AND id = $3
RETURNING id, tenant_id, user_id, title, body, severity, read_at, created_by_admin, created_at`,
		tenantID, userID, id, readAt.UTC()))
	if err == pgx.ErrNoRows {
		return Notification{}, ErrNotFound
	}
	return notice, err
}

func (s *PostgresStore) UnreadCount(ctx context.Context, tenantID, userID int64) (int64, error) {
	if s == nil || s.pool == nil {
		return 0, ErrStoreNotConfigured
	}
	var count int64
	if err := s.pool.QueryRow(ctx, `
SELECT count(*)::bigint
FROM user_notifications
WHERE tenant_id = $1
  AND user_id = $2
  AND read_at IS NULL`, tenantID, userID).Scan(&count); err != nil {
		return 0, err
	}
	return count, nil
}

type notificationScanner interface {
	Scan(...any) error
}

func scanNotification(row notificationScanner) (Notification, error) {
	var notice Notification
	var severity string
	var readAt, createdAt pgtype.Timestamptz
	var createdBy pgtype.Int8
	if err := row.Scan(
		&notice.ID,
		&notice.TenantID,
		&notice.UserID,
		&notice.Title,
		&notice.Body,
		&severity,
		&readAt,
		&createdBy,
		&createdAt,
	); err != nil {
		return Notification{}, err
	}
	notice.Severity = Severity(severity)
	notice.ReadAt = pgTimePtr(readAt)
	notice.CreatedByAdmin = pgInt64Ptr(createdBy)
	notice.CreatedAt = pgTime(createdAt)
	return notice, nil
}

func scanNotificationRows(rows pgx.Rows) ([]Notification, error) {
	items := []Notification{}
	for rows.Next() {
		notice, err := scanNotification(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, notice)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return items, nil
}

func pgTime(in pgtype.Timestamptz) time.Time {
	if !in.Valid {
		return time.Time{}
	}
	return in.Time.UTC()
}

func pgTimePtr(in pgtype.Timestamptz) *time.Time {
	if !in.Valid {
		return nil
	}
	t := in.Time.UTC()
	return &t
}

func pgInt64Ptr(in pgtype.Int8) *int64 {
	if !in.Valid {
		return nil
	}
	v := in.Int64
	return &v
}
