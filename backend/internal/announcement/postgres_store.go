package announcement

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

func (s *PostgresStore) Create(ctx context.Context, ann Announcement) (Announcement, error) {
	if s == nil || s.pool == nil {
		return Announcement{}, ErrStoreNotConfigured
	}
	return scanAnnouncement(s.pool.QueryRow(ctx, `
INSERT INTO announcements (
    tenant_id, title, body, severity, active, published_at, expires_at, created_by_admin
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
RETURNING id, tenant_id, title, body, severity, active, published_at, expires_at, created_by_admin, created_at, updated_at`,
		ann.TenantID, ann.Title, ann.Body, string(ann.Severity), ann.Active, ann.PublishedAt, ann.ExpiresAt, ann.CreatedByAdmin,
	))
}

func (s *PostgresStore) Update(ctx context.Context, ann Announcement) (Announcement, error) {
	if s == nil || s.pool == nil {
		return Announcement{}, ErrStoreNotConfigured
	}
	out, err := scanAnnouncement(s.pool.QueryRow(ctx, `
UPDATE announcements
SET title=$3,
    body=$4,
    severity=$5,
    active=$6,
    published_at=$7,
    expires_at=$8,
    updated_at=now()
WHERE tenant_id=$1 AND id=$2
RETURNING id, tenant_id, title, body, severity, active, published_at, expires_at, created_by_admin, created_at, updated_at`,
		ann.TenantID, ann.ID, ann.Title, ann.Body, string(ann.Severity), ann.Active, ann.PublishedAt, ann.ExpiresAt,
	))
	if err == pgx.ErrNoRows {
		return Announcement{}, ErrNotFound
	}
	return out, err
}

func (s *PostgresStore) Delete(ctx context.Context, tenantID, id int64) error {
	if s == nil || s.pool == nil {
		return ErrStoreNotConfigured
	}
	tag, err := s.pool.Exec(ctx, `DELETE FROM announcements WHERE tenant_id=$1 AND id=$2`, tenantID, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *PostgresStore) Get(ctx context.Context, tenantID, id int64) (Announcement, error) {
	if s == nil || s.pool == nil {
		return Announcement{}, ErrStoreNotConfigured
	}
	ann, err := scanAnnouncement(s.pool.QueryRow(ctx, `
SELECT id, tenant_id, title, body, severity, active, published_at, expires_at, created_by_admin, created_at, updated_at
FROM announcements
WHERE tenant_id=$1 AND id=$2`, tenantID, id))
	if err == pgx.ErrNoRows {
		return Announcement{}, ErrNotFound
	}
	return ann, err
}

func (s *PostgresStore) ListActive(ctx context.Context, in ListActiveInput) ([]Announcement, error) {
	if s == nil || s.pool == nil {
		return nil, ErrStoreNotConfigured
	}
	rows, err := s.pool.Query(ctx, `
SELECT id, tenant_id, title, body, severity, active, published_at, expires_at, created_by_admin, created_at, updated_at
FROM announcements
WHERE tenant_id=$1
  AND active = true
  AND published_at <= $2
  AND (expires_at IS NULL OR expires_at > $2)
ORDER BY published_at DESC, id DESC
LIMIT $3 OFFSET $4`, in.TenantID, in.Now, int32(in.Limit), int32(in.Offset))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanAnnouncementRows(rows)
}

func (s *PostgresStore) ListAllAdmin(ctx context.Context, in ListAdminInput) ([]Announcement, error) {
	if s == nil || s.pool == nil {
		return nil, ErrStoreNotConfigured
	}
	rows, err := s.pool.Query(ctx, `
SELECT id, tenant_id, title, body, severity, active, published_at, expires_at, created_by_admin, created_at, updated_at
FROM announcements
WHERE tenant_id=$1
ORDER BY published_at DESC, id DESC
LIMIT $2 OFFSET $3`, in.TenantID, int32(in.Limit), int32(in.Offset))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanAnnouncementRows(rows)
}

type announcementScanner interface {
	Scan(...any) error
}

func scanAnnouncement(row announcementScanner) (Announcement, error) {
	var ann Announcement
	var severity string
	var publishedAt, expiresAt, createdAt, updatedAt pgtype.Timestamptz
	var createdBy pgtype.Int8
	if err := row.Scan(
		&ann.ID,
		&ann.TenantID,
		&ann.Title,
		&ann.Body,
		&severity,
		&ann.Active,
		&publishedAt,
		&expiresAt,
		&createdBy,
		&createdAt,
		&updatedAt,
	); err != nil {
		return Announcement{}, err
	}
	ann.Severity = Severity(severity)
	ann.PublishedAt = pgTime(publishedAt)
	ann.ExpiresAt = pgTimePtr(expiresAt)
	ann.CreatedByAdmin = pgInt64Ptr(createdBy)
	ann.CreatedAt = pgTime(createdAt)
	ann.UpdatedAt = pgTime(updatedAt)
	return ann, nil
}

func scanAnnouncementRows(rows pgx.Rows) ([]Announcement, error) {
	items := []Announcement{}
	for rows.Next() {
		ann, err := scanAnnouncement(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, ann)
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
