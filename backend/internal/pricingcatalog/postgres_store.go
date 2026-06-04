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
	"github.com/BloomingProsperity/HUAKAI/internal/sign"
)

type PostgresStore struct {
	db      pricingcatalogdb.DBTX
	queries pricingcatalogdb.Querier
	beginTx func(context.Context, pgx.TxOptions) (pgx.Tx, error)
	signer  *sign.Signer
	now     func() time.Time
}

func NewPostgresStore(pool *pgxpool.Pool) *PostgresStore {
	if pool == nil {
		return &PostgresStore{}
	}
	return newPostgresStoreWithAuditDB(pool, nil, time.Now)
}

func NewPostgresStoreWithAuditSigner(pool *pgxpool.Pool, signer *sign.Signer) *PostgresStore {
	if pool == nil {
		return &PostgresStore{signer: signer, now: time.Now}
	}
	return newPostgresStoreWithAuditDB(pool, signer, time.Now)
}

func newPostgresStore(q pricingcatalogdb.Querier) *PostgresStore {
	return &PostgresStore{queries: q, now: time.Now}
}

func newPostgresStoreWithAuditDB(db pricingcatalogdb.DBTX, signer *sign.Signer, now func() time.Time) *PostgresStore {
	store := &PostgresStore{
		db:      db,
		queries: pricingcatalogdb.New(db),
		signer:  signer,
		now:     now,
	}
	if store.now == nil {
		store.now = time.Now
	}
	if beginner, ok := db.(interface {
		BeginTx(context.Context, pgx.TxOptions) (pgx.Tx, error)
	}); ok {
		store.beginTx = beginner.BeginTx
	}
	return store
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
	if s.signer == nil {
		return GroupPricingRatio{}, ErrAuditSignerMissing
	}
	if s.beginTx == nil {
		return GroupPricingRatio{}, ErrAuditTxMissing
	}
	tx, err := s.beginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return GroupPricingRatio{}, fmt.Errorf("%w: begin audited upsert ratio: %w", ErrBackend, err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	qtx := pricingcatalogdb.New(tx)
	oldRatio, hasOld, err := getRatioInTx(ctx, qtx, p.TenantID, p.PoolGroupID)
	if err != nil {
		return GroupPricingRatio{}, err
	}
	row, err := qtx.UpsertPoolGroupPricingRatio(ctx, pricingcatalogdb.UpsertPoolGroupPricingRatioParams{
		TenantID:    p.TenantID,
		PoolGroupID: p.PoolGroupID,
		Ratio:       p.Ratio.String(),
		PublicRatio: p.PublicRatio,
		Actor:       strings.TrimSpace(p.Actor),
	})
	if err != nil {
		return GroupPricingRatio{}, fmt.Errorf("%w: upsert ratio: %w", ErrBackend, err)
	}
	out, err := ratioFromUpsertRow(row)
	if err != nil {
		return GroupPricingRatio{}, err
	}
	newRatioText := out.RatioString()
	event := pricingRatioAuditEvent{
		OccurredAt:  s.now().UTC(),
		ActorID:     strings.TrimSpace(p.Actor),
		ActorRole:   strings.TrimSpace(p.ActorRole),
		TenantID:    p.TenantID,
		PoolGroupID: p.PoolGroupID,
		Action:      RatioAuditActionUpsert,
		NewRatio:    &newRatioText,
	}
	if hasOld {
		oldRatioText := oldRatio.RatioString()
		event.OldRatio = &oldRatioText
	}
	if _, err := appendPricingRatioAuditInTx(ctx, tx, s.signer, event); err != nil {
		return GroupPricingRatio{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return GroupPricingRatio{}, fmt.Errorf("%w: commit audited upsert ratio: %w", ErrBackend, err)
	}
	return out, nil
}

func (s *PostgresStore) DeleteRatio(ctx context.Context, p DeleteRatioParams) error {
	if s == nil || s.queries == nil {
		return fmt.Errorf("%w: store not configured", ErrBackend)
	}
	if err := validateDeleteParams(p); err != nil {
		return err
	}
	if s.signer == nil {
		return ErrAuditSignerMissing
	}
	if s.beginTx == nil {
		return ErrAuditTxMissing
	}
	tx, err := s.beginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return fmt.Errorf("%w: begin audited delete ratio: %w", ErrBackend, err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	qtx := pricingcatalogdb.New(tx)
	oldRatio, hasOld, err := getRatioInTx(ctx, qtx, p.TenantID, p.PoolGroupID)
	if err != nil {
		return err
	}
	if !hasOld {
		return ErrNotFound
	}
	_, err = qtx.DeletePoolGroupPricingRatio(ctx, pricingcatalogdb.DeletePoolGroupPricingRatioParams{
		TenantID:    p.TenantID,
		PoolGroupID: p.PoolGroupID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("%w: delete ratio: %w", ErrBackend, err)
	}
	oldRatioText := oldRatio.RatioString()
	if _, err := appendPricingRatioAuditInTx(ctx, tx, s.signer, pricingRatioAuditEvent{
		OccurredAt:  s.now().UTC(),
		ActorID:     strings.TrimSpace(p.Actor),
		ActorRole:   strings.TrimSpace(p.ActorRole),
		TenantID:    p.TenantID,
		PoolGroupID: p.PoolGroupID,
		Action:      RatioAuditActionDelete,
		OldRatio:    &oldRatioText,
	}); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("%w: commit audited delete ratio: %w", ErrBackend, err)
	}
	return nil
}

func validateUpsertParams(p UpsertRatioParams) error {
	if p.TenantID <= 0 ||
		p.PoolGroupID <= 0 ||
		strings.TrimSpace(p.Actor) == "" ||
		strings.TrimSpace(p.ActorRole) == "" ||
		!p.Ratio.IsPositive() {
		return ErrInvalidInput
	}
	return nil
}

func validateDeleteParams(p DeleteRatioParams) error {
	if p.TenantID <= 0 ||
		p.PoolGroupID <= 0 ||
		strings.TrimSpace(p.Actor) == "" ||
		strings.TrimSpace(p.ActorRole) == "" {
		return ErrInvalidInput
	}
	return nil
}

func getRatioInTx(ctx context.Context, q pricingcatalogdb.Querier, tenantID, poolGroupID int64) (GroupPricingRatio, bool, error) {
	row, err := q.GetPoolGroupPricingRatio(ctx, pricingcatalogdb.GetPoolGroupPricingRatioParams{
		TenantID:    tenantID,
		PoolGroupID: poolGroupID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return GroupPricingRatio{}, false, nil
	}
	if err != nil {
		return GroupPricingRatio{}, false, fmt.Errorf("%w: get ratio: %w", ErrBackend, err)
	}
	out, err := ratioFromGetRow(row)
	if err != nil {
		return GroupPricingRatio{}, false, err
	}
	return out, true, nil
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
