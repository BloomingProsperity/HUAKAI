package accountquota

import (
	"context"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrStoreNotConfigured = errors.New("额度事实存储未配置")

type Store interface {
	ReplaceSnapshot(context.Context, Snapshot) error
	RecordFailure(context.Context, Snapshot, string) error
}

// RecordFailure 只更新探测状态，不删除仍在有效期内的上一份额度事实。
func (s *PostgresStore) RecordFailure(ctx context.Context, snapshot Snapshot, errorClass string) error {
	if s == nil || s.pool == nil {
		return ErrStoreNotConfigured
	}
	snapshot.Facts = []Fact{{MetricKey: "probe_status", State: StateError, ErrorClass: strings.TrimSpace(errorClass)}}
	if err := snapshot.Validate(); err != nil {
		return err
	}
	fact := snapshot.Facts[0]
	_, err := s.pool.Exec(ctx, `
INSERT INTO provider_account_quota_facts (
    tenant_id, provider_account_id, vendor, metric_key, model_key, state,
    observed_at, source, error_class
) VALUES ($1, $2, $3, $4, '', $5, $6, $7, $8)
ON CONFLICT (tenant_id, provider_account_id, metric_key, model_key) DO UPDATE SET
    vendor = EXCLUDED.vendor,
    state = EXCLUDED.state,
    observed_at = EXCLUDED.observed_at,
    source = EXCLUDED.source,
    error_class = EXCLUDED.error_class,
    updated_at = now()
WHERE provider_account_quota_facts.observed_at <= EXCLUDED.observed_at`,
		snapshot.TenantID, snapshot.ProviderAccountID, strings.TrimSpace(snapshot.Vendor),
		fact.MetricKey, string(fact.State), snapshot.ObservedAt.UTC(), string(snapshot.Source), fact.ErrorClass,
	)
	return err
}

type PostgresStore struct {
	pool *pgxpool.Pool
}

func NewPostgresStore(pool *pgxpool.Pool) *PostgresStore {
	return &PostgresStore{pool: pool}
}

// ReplaceSnapshot 在同一事务内替换同一来源的本轮事实，避免已下架模型长期残留为新鲜额度。
func (s *PostgresStore) ReplaceSnapshot(ctx context.Context, snapshot Snapshot) error {
	if s == nil || s.pool == nil {
		return ErrStoreNotConfigured
	}
	if err := snapshot.Validate(); err != nil {
		return err
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	for _, fact := range snapshot.Facts {
		_, err = tx.Exec(ctx, `
INSERT INTO provider_account_quota_facts (
    tenant_id, provider_account_id, vendor, metric_key, model_key, state,
    used_value, limit_value, remaining_value, unit,
    utilization_percent, remaining_percent, resets_at, observed_at,
    valid_until, source, error_class
) VALUES (
    $1, $2, $3, $4, $5, $6,
    $7, $8, $9, NULLIF($10, ''),
    $11, $12, $13, $14,
    $15, $16, NULLIF($17, '')
)
ON CONFLICT (tenant_id, provider_account_id, metric_key, model_key) DO UPDATE SET
    vendor = EXCLUDED.vendor,
    state = EXCLUDED.state,
    used_value = EXCLUDED.used_value,
    limit_value = EXCLUDED.limit_value,
    remaining_value = EXCLUDED.remaining_value,
    unit = EXCLUDED.unit,
    utilization_percent = EXCLUDED.utilization_percent,
    remaining_percent = EXCLUDED.remaining_percent,
    resets_at = EXCLUDED.resets_at,
    observed_at = EXCLUDED.observed_at,
    valid_until = EXCLUDED.valid_until,
    source = EXCLUDED.source,
    error_class = EXCLUDED.error_class,
    updated_at = now()
WHERE provider_account_quota_facts.observed_at <= EXCLUDED.observed_at`,
			snapshot.TenantID, snapshot.ProviderAccountID, strings.TrimSpace(snapshot.Vendor),
			strings.TrimSpace(fact.MetricKey), strings.TrimSpace(fact.ModelKey), string(fact.State),
			fact.UsedValue, fact.LimitValue, fact.RemainingValue, strings.TrimSpace(fact.Unit),
			fact.UtilizationPercent, fact.RemainingPercent, fact.ResetsAt, snapshot.ObservedAt.UTC(),
			fact.ValidUntil, string(snapshot.Source), strings.TrimSpace(fact.ErrorClass),
		)
		if err != nil {
			return err
		}
	}
	if snapshot.Complete {
		_, err = tx.Exec(ctx, `
DELETE FROM provider_account_quota_facts
WHERE tenant_id = $1
  AND provider_account_id = $2
  AND source = $3
  AND observed_at < $4`,
			snapshot.TenantID, snapshot.ProviderAccountID, string(snapshot.Source), snapshot.ObservedAt.UTC(),
		)
		if err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

var _ Store = (*PostgresStore)(nil)
