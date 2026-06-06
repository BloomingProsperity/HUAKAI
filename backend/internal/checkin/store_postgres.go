package checkin

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type PostgresStore struct {
	pool *pgxpool.Pool
}

func NewPostgresStore(pool *pgxpool.Pool) *PostgresStore {
	return &PostgresStore{pool: pool}
}

func (s *PostgresStore) CheckedInOn(ctx context.Context, tenantID, userID int64, date time.Time) (bool, error) {
	if s == nil || s.pool == nil {
		return false, ErrStoreNotConfigured
	}
	var exists bool
	if err := s.pool.QueryRow(ctx, `
SELECT EXISTS (
	SELECT 1
	FROM daily_checkin
	WHERE tenant_id=$1 AND user_id=$2 AND checkin_date=$3
)`, tenantID, userID, normalizeDate(date)).Scan(&exists); err != nil {
		return false, fmt.Errorf("checkin: read checked-in day: %w", err)
	}
	return exists, nil
}

func (s *PostgresStore) ListRecords(ctx context.Context, tenantID, userID int64, monthStart time.Time) ([]Record, error) {
	if s == nil || s.pool == nil {
		return nil, ErrStoreNotConfigured
	}
	start := monthStartUTC(monthStart)
	end := start.AddDate(0, 1, 0)
	rows, err := s.pool.Query(ctx, `
SELECT id, checkin_date, reward_cents, currency_code, billing_event_id, created_at
FROM daily_checkin
WHERE tenant_id=$1
  AND user_id=$2
  AND checkin_date >= $3
  AND checkin_date < $4
ORDER BY checkin_date ASC`, tenantID, userID, start, end)
	if err != nil {
		return nil, fmt.Errorf("checkin: list records: %w", err)
	}
	defer rows.Close()

	out := []Record{}
	for rows.Next() {
		rec, err := scanRecord(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, rec)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("checkin: list records rows: %w", err)
	}
	return out, nil
}

func scanRecord(row pgx.Row) (Record, error) {
	var rec Record
	var billing sql.NullInt64
	if err := row.Scan(&rec.ID, &rec.CheckinDate, &rec.RewardCents, &rec.CurrencyCode, &billing, &rec.CreatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Record{}, err
		}
		return Record{}, fmt.Errorf("checkin: scan record: %w", err)
	}
	rec.CheckinDate = normalizeDate(rec.CheckinDate)
	rec.CurrencyCode = strings.TrimSpace(rec.CurrencyCode)
	if billing.Valid {
		rec.BillingEventID = billing.Int64
	}
	rec.CreatedAt = rec.CreatedAt.UTC()
	return rec, nil
}

func monthStartUTC(t time.Time) time.Time {
	y, m, _ := t.UTC().Date()
	return time.Date(y, m, 1, 0, 0, 0, 0, time.UTC)
}

var _ Store = (*PostgresStore)(nil)
