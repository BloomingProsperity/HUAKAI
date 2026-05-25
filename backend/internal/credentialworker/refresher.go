package credentialworker

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Refresher 是 credential worker 依赖的最小刷新接口。
type Refresher interface {
	Refresh(ctx context.Context, accountID int64) error
}

// NoopRefresher 只用于测试或显式 dry-run；生产不应把它当真实刷新实现。
type NoopRefresher struct{}

func (NoopRefresher) Refresh(context.Context, int64) error { return nil }

// ProviderAwareRefresher 可利用 scheduler 已扫出的 provider_id 做分发。
type ProviderAwareRefresher interface {
	RefreshForProvider(ctx context.Context, providerID, accountID int64) error
}

// ErrProviderAdapterMissing 表示对应 provider 的刷新 adapter 尚未接入。
var ErrProviderAdapterMissing = errors.New("credentialworker: provider refresh adapter missing")

// RefreshAccount 是 registry refresher 从存储层读取的最小账号视图。
type RefreshAccount struct {
	AccountID         int64
	TenantID          int64
	ProviderID        int64
	ProviderName      string
	CurrentCredential []byte
	TokenVersion      int32
}

// RefreshAccountStore 隔离 credentialworker 与具体数据库实现，便于单测替换。
type RefreshAccountStore interface {
	LoadRefreshAccount(ctx context.Context, accountID int64) (RefreshAccount, error)
	SaveRefreshCredential(ctx context.Context, account RefreshAccount, newCredential []byte, expiresAt time.Time) error
}

// RegistryRefresher 是真实刷新分发器：先读账号当前 credential，再按 provider name 路由到 adapter。
type RegistryRefresher struct {
	registry *AdapterRegistry
	store    RefreshAccountStore
}

func NewRegistryRefresher(registry *AdapterRegistry, store RefreshAccountStore) *RegistryRefresher {
	if registry == nil {
		registry = NewAdapterRegistry()
	}
	return &RegistryRefresher{registry: registry, store: store}
}

func (r *RegistryRefresher) Refresh(ctx context.Context, accountID int64) error {
	return r.refresh(ctx, 0, accountID)
}

func (r *RegistryRefresher) RefreshForProvider(ctx context.Context, providerID, accountID int64) error {
	return r.refresh(ctx, providerID, accountID)
}

func (r *RegistryRefresher) refresh(ctx context.Context, providerID, accountID int64) error {
	if r == nil || r.registry == nil {
		return ErrProviderAdapterMissing
	}
	if r.store == nil {
		return errors.New("credentialworker: refresh account store missing")
	}
	account, err := r.store.LoadRefreshAccount(ctx, accountID)
	if err != nil {
		return err
	}
	if providerID != 0 && account.ProviderID != 0 && providerID != account.ProviderID {
		return fmt.Errorf("credentialworker: provider_id changed during refresh: scanned=%d loaded=%d account_id=%d", providerID, account.ProviderID, accountID)
	}
	providerName := normalizeProviderName(account.ProviderName)
	adapter, ok := r.registry.Lookup(providerName)
	if !ok || adapter == nil {
		if IsMockOnlyProvider(providerName) {
			return fmt.Errorf("%w: provider=%s account_id=%d", ErrMockOnly, providerName, accountID)
		}
		slog.Warn("credentialworker provider adapter missing",
			"provider", providerName,
			"reason_class", "adapter_missing",
		)
		return fmt.Errorf("%w: provider=%s account_id=%d", ErrProviderAdapterMissing, providerName, accountID)
	}
	newCredential, expiresAt, err := adapter.RefreshForProvider(ctx, accountID, providerName, account.CurrentCredential)
	if err != nil {
		return err
	}
	return r.store.SaveRefreshCredential(ctx, account, newCredential, expiresAt)
}

type pgRefreshDB interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// PostgresRefreshStore 用 CAS 写回刷新后的 credential，不触碰 schema。
type PostgresRefreshStore struct {
	db pgRefreshDB
}

func NewPostgresRefreshStore(db pgRefreshDB) *PostgresRefreshStore {
	return &PostgresRefreshStore{db: db}
}

func (s *PostgresRefreshStore) LoadRefreshAccount(ctx context.Context, accountID int64) (RefreshAccount, error) {
	if s == nil || s.db == nil {
		return RefreshAccount{}, errors.New("credentialworker: postgres refresh db missing")
	}
	const q = `
SELECT pa.id, pa.tenant_id, pa.provider_id, p.code, pa.credentials, pa.token_version
FROM provider_accounts pa
JOIN providers p ON p.id = pa.provider_id AND p.tenant_id = pa.tenant_id AND p.deleted_at IS NULL
WHERE pa.id = $1
  AND pa.deleted_at IS NULL
  AND pa.enabled
`
	var account RefreshAccount
	if err := s.db.QueryRow(ctx, q, accountID).Scan(
		&account.AccountID,
		&account.TenantID,
		&account.ProviderID,
		&account.ProviderName,
		&account.CurrentCredential,
		&account.TokenVersion,
	); err != nil {
		return RefreshAccount{}, err
	}
	return account, nil
}

func (s *PostgresRefreshStore) SaveRefreshCredential(ctx context.Context, account RefreshAccount, newCredential []byte, expiresAt time.Time) error {
	if s == nil || s.db == nil {
		return errors.New("credentialworker: postgres refresh db missing")
	}
	const q = `
UPDATE provider_accounts
SET credentials = $1::jsonb,
    expires_at = $2,
    token_version = token_version + 1,
    refresh_token_fingerprint = $3,
    last_refresh_at = NOW(),
    last_refresh_outcome = $4,
    updated_at = NOW()
WHERE id = $5
  AND tenant_id = $6
  AND token_version = $7
`
	outcome := "refresh_succeeded"
	tag, err := s.db.Exec(ctx, q, newCredential, expiresAt, refreshFingerprint(account.TenantID, newCredential), outcome, account.AccountID, account.TenantID, account.TokenVersion)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return errors.New("credentialworker: refresh credential cas lost")
	}
	return nil
}

func refreshFingerprint(tenantID int64, credential []byte) *string {
	var raw map[string]any
	if err := json.Unmarshal(credential, &raw); err != nil {
		return nil
	}
	refreshToken, _ := raw["refresh_token"].(string)
	refreshToken = strings.TrimSpace(refreshToken)
	if refreshToken == "" {
		return nil
	}
	sum := sha256.Sum256([]byte(fmt.Sprintf("%d:%s", tenantID, refreshToken)))
	fp := hex.EncodeToString(sum[:])
	return &fp
}
