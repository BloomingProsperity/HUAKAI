package billing

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	dbbilling "github.com/BloomingProsperity/HUAKAI/internal/db/billing"
)

// StoredBillingSetting 是 billing_settings 的领域层行表示。
type StoredBillingSetting struct {
	ID        int64
	TenantID  int64
	Key       string
	Value     string
	UpdatedAt time.Time
	UpdatedBy string
}

// PolicyStore 封装 billing_settings 的读、写和列表查询。
type PolicyStore interface {
	Get(ctx context.Context, tenantID int64, key string) (StoredBillingSetting, bool, error)
	UpsertStreamInputOnlyInterruptedPolicy(ctx context.Context, tenantID int64, policy StreamInputOnlyInterruptedPolicy, updatedBy string) (StoredBillingSetting, error)
	List(ctx context.Context, tenantID int64) ([]StoredBillingSetting, error)
}

type billingSettingsQueries interface {
	GetBillingSetting(ctx context.Context, arg dbbilling.GetBillingSettingParams) (dbbilling.BillingSetting, error)
	UpsertBillingSetting(ctx context.Context, arg dbbilling.UpsertBillingSettingParams) (dbbilling.BillingSetting, error)
	ListBillingSettingsByTenant(ctx context.Context, tenantID int64) ([]dbbilling.BillingSetting, error)
}

// SQLPolicyStore 是 PolicyStore 的 PostgreSQL 与 sqlc 实现。
type SQLPolicyStore struct {
	q billingSettingsQueries
}

// NewPolicyStore 构造 PostgreSQL 支持的 PolicyStore; 连接池为空时返回未配置实例。
func NewPolicyStore(pool *pgxpool.Pool) *SQLPolicyStore {
	if pool == nil {
		return &SQLPolicyStore{}
	}
	return &SQLPolicyStore{q: dbbilling.New(pool)}
}

func (s *SQLPolicyStore) Get(ctx context.Context, tenantID int64, key string) (StoredBillingSetting, bool, error) {
	if s == nil || s.q == nil {
		return StoredBillingSetting{}, false, ErrPoolNotConfigured
	}
	key = strings.TrimSpace(key)
	if tenantID <= 0 || key == "" {
		return StoredBillingSetting{}, false, fmt.Errorf("%w: tenant_id/key", ErrBillingSettingInvalid)
	}
	row, err := s.q.GetBillingSetting(ctx, dbbilling.GetBillingSettingParams{
		TenantID:   tenantID,
		SettingKey: key,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return StoredBillingSetting{}, false, nil
	}
	if err != nil {
		return StoredBillingSetting{}, false, err
	}
	return storedBillingSettingFromDB(row), true, nil
}

func (s *SQLPolicyStore) UpsertStreamInputOnlyInterruptedPolicy(ctx context.Context, tenantID int64, policy StreamInputOnlyInterruptedPolicy, updatedBy string) (StoredBillingSetting, error) {
	if s == nil || s.q == nil {
		return StoredBillingSetting{}, ErrPoolNotConfigured
	}
	if tenantID <= 0 {
		return StoredBillingSetting{}, fmt.Errorf("%w: tenant_id", ErrBillingSettingInvalid)
	}
	canonical, err := ParseStreamInputOnlyInterruptedPolicy(policy.String())
	if err != nil {
		return StoredBillingSetting{}, err
	}
	updatedBy = strings.TrimSpace(updatedBy)
	if updatedBy == "" {
		updatedBy = "system"
	}
	row, err := s.q.UpsertBillingSetting(ctx, dbbilling.UpsertBillingSettingParams{
		TenantID:     tenantID,
		SettingKey:   StreamInputOnlyInterruptedPolicyKey,
		SettingValue: canonical.String(),
		UpdatedBy:    updatedBy,
	})
	if err != nil {
		return StoredBillingSetting{}, err
	}
	return storedBillingSettingFromDB(row), nil
}

func (s *SQLPolicyStore) UpsertBalanceEnforcementMode(ctx context.Context, tenantID int64, mode BalanceEnforcementMode, updatedBy string) (StoredBillingSetting, error) {
	if s == nil || s.q == nil {
		return StoredBillingSetting{}, ErrPoolNotConfigured
	}
	if tenantID <= 0 {
		return StoredBillingSetting{}, fmt.Errorf("%w: tenant_id", ErrBillingSettingInvalid)
	}
	canonical, err := ParseBalanceEnforcementMode(mode.String())
	if err != nil {
		return StoredBillingSetting{}, err
	}
	updatedBy = strings.TrimSpace(updatedBy)
	if updatedBy == "" {
		updatedBy = "system"
	}
	row, err := s.q.UpsertBillingSetting(ctx, dbbilling.UpsertBillingSettingParams{
		TenantID:     tenantID,
		SettingKey:   BalanceEnforcementModeKey,
		SettingValue: canonical.String(),
		UpdatedBy:    updatedBy,
	})
	if err != nil {
		return StoredBillingSetting{}, err
	}
	return storedBillingSettingFromDB(row), nil
}

func (s *SQLPolicyStore) List(ctx context.Context, tenantID int64) ([]StoredBillingSetting, error) {
	if s == nil || s.q == nil {
		return nil, ErrPoolNotConfigured
	}
	if tenantID <= 0 {
		return nil, fmt.Errorf("%w: tenant_id", ErrBillingSettingInvalid)
	}
	rows, err := s.q.ListBillingSettingsByTenant(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	out := make([]StoredBillingSetting, 0, len(rows))
	for _, row := range rows {
		out = append(out, storedBillingSettingFromDB(row))
	}
	return out, nil
}

func storedBillingSettingFromDB(row dbbilling.BillingSetting) StoredBillingSetting {
	return StoredBillingSetting{
		ID:        row.ID,
		TenantID:  row.TenantID,
		Key:       row.SettingKey,
		Value:     row.SettingValue,
		UpdatedAt: pgTime(row.UpdatedAt),
		UpdatedBy: row.UpdatedBy,
	}
}

var _ PolicyStore = (*SQLPolicyStore)(nil)
