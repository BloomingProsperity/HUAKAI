package pricingcatalog

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"

	pricingcatalogdb "github.com/BloomingProsperity/HUAKAI/internal/db/pricingcatalog"
)

type PostgresStore struct {
	queries pricingcatalogdb.Querier
}

func NewPostgresStore(pool *pgxpool.Pool) *PostgresStore {
	if pool == nil {
		return &PostgresStore{}
	}
	return newPostgresStore(pricingcatalogdb.New(pool))
}

func newPostgresStore(q pricingcatalogdb.Querier) *PostgresStore {
	return &PostgresStore{queries: q}
}

func (s *PostgresStore) GetRatio(ctx context.Context, tenantID, poolGroupID int64) (GroupPricingRatio, error) {
	if s == nil || s.queries == nil {
		return GroupPricingRatio{}, fmt.Errorf("%w: store not configured", ErrBackend)
	}
	row, err := s.queries.GetPoolGroupPricingRatio(ctx, pricingcatalogdb.GetPoolGroupPricingRatioParams{
		TenantID:    tenantID,
		PoolGroupID: poolGroupID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return GroupPricingRatio{}, ErrNotFound
	}
	if err != nil {
		return GroupPricingRatio{}, fmt.Errorf("%w: get ratio: %w", ErrBackend, err)
	}
	return ratioFromGetRow(row)
}

func (s *PostgresStore) ListRatios(ctx context.Context, tenantID int64) ([]GroupPricingRatio, error) {
	if s == nil || s.queries == nil {
		return nil, fmt.Errorf("%w: store not configured", ErrBackend)
	}
	rows, err := s.queries.ListPoolGroupPricingRatios(ctx, tenantID)
	if err != nil {
		return nil, fmt.Errorf("%w: list ratios: %w", ErrBackend, err)
	}
	out := make([]GroupPricingRatio, 0, len(rows))
	for _, row := range rows {
		item, err := ratioFromListRow(row)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, nil
}

func (s *PostgresStore) UpsertRatio(ctx context.Context, p UpsertRatioParams) (GroupPricingRatio, error) {
	if s == nil || s.queries == nil {
		return GroupPricingRatio{}, fmt.Errorf("%w: store not configured", ErrBackend)
	}
	if err := validateUpsertParams(p); err != nil {
		return GroupPricingRatio{}, err
	}
	row, err := s.queries.UpsertPoolGroupPricingRatio(ctx, pricingcatalogdb.UpsertPoolGroupPricingRatioParams{
		TenantID:    p.TenantID,
		PoolGroupID: p.PoolGroupID,
		Ratio:       p.Ratio.String(),
		PublicRatio: p.PublicRatio,
		Actor:       strings.TrimSpace(p.Actor),
	})
	if err != nil {
		return GroupPricingRatio{}, fmt.Errorf("%w: upsert ratio: %w", ErrBackend, err)
	}
	return ratioFromUpsertRow(row)
}

func (s *PostgresStore) DeleteRatio(ctx context.Context, tenantID, poolGroupID int64) error {
	if s == nil || s.queries == nil {
		return fmt.Errorf("%w: store not configured", ErrBackend)
	}
	_, err := s.queries.DeletePoolGroupPricingRatio(ctx, pricingcatalogdb.DeletePoolGroupPricingRatioParams{
		TenantID:    tenantID,
		PoolGroupID: poolGroupID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("%w: delete ratio: %w", ErrBackend, err)
	}
	return nil
}

func validateUpsertParams(p UpsertRatioParams) error {
	if p.TenantID <= 0 || p.PoolGroupID <= 0 || strings.TrimSpace(p.Actor) == "" || !p.Ratio.IsPositive() {
		return ErrInvalidInput
	}
	return nil
}

func ratioFromGetRow(row pricingcatalogdb.GetPoolGroupPricingRatioRow) (GroupPricingRatio, error) {
	return buildRatio(row.ID, row.TenantID, row.PoolGroupID, row.Ratio, row.PublicRatio,
		row.CreatedBy, row.UpdatedBy, row.CreatedAt, row.UpdatedAt)
}

func ratioFromListRow(row pricingcatalogdb.ListPoolGroupPricingRatiosRow) (GroupPricingRatio, error) {
	return buildRatio(row.ID, row.TenantID, row.PoolGroupID, row.Ratio, row.PublicRatio,
		row.CreatedBy, row.UpdatedBy, row.CreatedAt, row.UpdatedAt)
}

func ratioFromUpsertRow(row pricingcatalogdb.UpsertPoolGroupPricingRatioRow) (GroupPricingRatio, error) {
	return buildRatio(row.ID, row.TenantID, row.PoolGroupID, row.Ratio, row.PublicRatio,
		row.CreatedBy, row.UpdatedBy, row.CreatedAt, row.UpdatedAt)
}

func buildRatio(id, tenantID, poolGroupID int64, ratioText string, public bool, createdBy, updatedBy string, createdAt, updatedAt pgtype.Timestamptz) (GroupPricingRatio, error) {
	ratio, err := decimal.NewFromString(ratioText)
	if err != nil {
		return GroupPricingRatio{}, fmt.Errorf("%w: invalid ratio from database: %w", ErrBackend, err)
	}
	return GroupPricingRatio{
		ID:          id,
		TenantID:    tenantID,
		PoolGroupID: poolGroupID,
		Ratio:       ratio,
		RatioText:   ratioText,
		PublicRatio: public,
		CreatedBy:   createdBy,
		UpdatedBy:   updatedBy,
		CreatedAt:   pgTime(createdAt),
		UpdatedAt:   pgTime(updatedAt),
	}, nil
}

func pgTime(ts pgtype.Timestamptz) time.Time {
	if !ts.Valid {
		return time.Time{}
	}
	return ts.Time.UTC()
}
