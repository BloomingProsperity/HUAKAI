package billing

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

var ErrRateTableNotFound = errors.New("billing: rate table not found")

const PublicScopeTenantID int64 = 0

// RateTable 是公开 pricing endpoint 返回的历史价格表。
type RateTable struct {
	ID            int64           `json:"id"`
	Version       string          `json:"version"`
	PricingData   json.RawMessage `json:"pricing_data"`
	EffectiveFrom time.Time       `json:"effective_from"`
	EffectiveTo   *time.Time      `json:"effective_to,omitempty"`
	CreatedAt     time.Time       `json:"created_at"`
}

// RateTableSnapshot 是历史 version 列表的轻量行。
type RateTableSnapshot struct {
	ID            int64      `json:"id"`
	Version       string     `json:"version"`
	EffectiveFrom time.Time  `json:"effective_from"`
	EffectiveTo   *time.Time `json:"effective_to,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
}

// RateTableSource 读取 F-BILL-001 的 immutable pricing history。
type RateTableSource interface {
	GetRateTable(ctx context.Context, version string) (RateTable, error)
	GetRateTableSnapshot(ctx context.Context, snapshotID int64) (RateTable, error)
	ListRateTableSnapshots(ctx context.Context) ([]RateTableSnapshot, error)
}

type rateTableQueryer interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

// PGXRateTableSource 使用现有 billing_pricing_versions 表提供只读公开查询。
type PGXRateTableSource struct {
	pool rateTableQueryer
}

const getPublicRateTableSQL = `
SELECT id, version, pricing_data, effective_from, effective_to, created_at
FROM billing_pricing_versions
WHERE version = $1
  AND tenant_id = $2
LIMIT 1`

const getPublicRateTableSnapshotSQL = `
SELECT id, version, pricing_data, effective_from, effective_to, created_at
FROM billing_pricing_versions
WHERE id = $1
  AND tenant_id = $2
LIMIT 1`

const listPublicRateTableSnapshotsSQL = `
SELECT DISTINCT ON (version) id, version, effective_from, effective_to, created_at
FROM billing_pricing_versions
WHERE tenant_id = $1
ORDER BY version ASC, effective_from DESC`

func NewPGXRateTableSource(pool *pgxpool.Pool) *PGXRateTableSource {
	return &PGXRateTableSource{pool: pool}
}

func (s *PGXRateTableSource) GetRateTable(ctx context.Context, version string) (RateTable, error) {
	if s == nil || s.pool == nil {
		return RateTable{}, ErrPoolNotConfigured
	}
	var (
		row           RateTable
		effectiveFrom pgtype.Timestamptz
		effectiveTo   pgtype.Timestamptz
		createdAt     pgtype.Timestamptz
	)
	err := s.pool.QueryRow(ctx, getPublicRateTableSQL, version, PublicScopeTenantID).Scan(&row.ID, &row.Version, &row.PricingData, &effectiveFrom, &effectiveTo, &createdAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return RateTable{}, ErrRateTableNotFound
	}
	if err != nil {
		return RateTable{}, fmt.Errorf("billing: get rate table: %w", err)
	}
	row.EffectiveFrom = pgTime(effectiveFrom)
	row.EffectiveTo = pgTimePtr(effectiveTo)
	row.CreatedAt = pgTime(createdAt)
	if len(row.PricingData) == 0 {
		row.PricingData = json.RawMessage(`{}`)
	}
	return row, nil
}

func (s *PGXRateTableSource) GetRateTableSnapshot(ctx context.Context, snapshotID int64) (RateTable, error) {
	if s == nil || s.pool == nil {
		return RateTable{}, ErrPoolNotConfigured
	}
	if snapshotID <= 0 {
		return RateTable{}, ErrRateTableNotFound
	}
	var (
		row           RateTable
		effectiveFrom pgtype.Timestamptz
		effectiveTo   pgtype.Timestamptz
		createdAt     pgtype.Timestamptz
	)
	err := s.pool.QueryRow(ctx, getPublicRateTableSnapshotSQL, snapshotID, PublicScopeTenantID).Scan(&row.ID, &row.Version, &row.PricingData, &effectiveFrom, &effectiveTo, &createdAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return RateTable{}, ErrRateTableNotFound
	}
	if err != nil {
		return RateTable{}, fmt.Errorf("billing: get rate table snapshot: %w", err)
	}
	row.EffectiveFrom = pgTime(effectiveFrom)
	row.EffectiveTo = pgTimePtr(effectiveTo)
	row.CreatedAt = pgTime(createdAt)
	if len(row.PricingData) == 0 {
		row.PricingData = json.RawMessage(`{}`)
	}
	return row, nil
}

func (s *PGXRateTableSource) ListRateTableSnapshots(ctx context.Context) ([]RateTableSnapshot, error) {
	if s == nil || s.pool == nil {
		return nil, ErrPoolNotConfigured
	}
	rows, err := s.pool.Query(ctx, listPublicRateTableSnapshotsSQL, PublicScopeTenantID)
	if err != nil {
		return nil, fmt.Errorf("billing: list rate table snapshots: %w", err)
	}
	defer rows.Close()

	var out []RateTableSnapshot
	for rows.Next() {
		var (
			row           RateTableSnapshot
			effectiveFrom pgtype.Timestamptz
			effectiveTo   pgtype.Timestamptz
			createdAt     pgtype.Timestamptz
		)
		if err := rows.Scan(&row.ID, &row.Version, &effectiveFrom, &effectiveTo, &createdAt); err != nil {
			return nil, fmt.Errorf("billing: scan rate table snapshot: %w", err)
		}
		row.EffectiveFrom = pgTime(effectiveFrom)
		row.EffectiveTo = pgTimePtr(effectiveTo)
		row.CreatedAt = pgTime(createdAt)
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("billing: iterate rate table snapshots: %w", err)
	}
	return out, nil
}

func pgTime(ts pgtype.Timestamptz) time.Time {
	if !ts.Valid {
		return time.Time{}
	}
	return ts.Time.UTC()
}

func pgTimePtr(ts pgtype.Timestamptz) *time.Time {
	if !ts.Valid {
		return nil
	}
	t := ts.Time.UTC()
	return &t
}
