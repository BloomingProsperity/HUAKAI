package userkeycontrols

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"

	dbuserkeycontrols "github.com/BloomingProsperity/HUAKAI/internal/db/userkeycontrols"
	"github.com/BloomingProsperity/HUAKAI/internal/quota"
)

type controlsStore interface {
	WithTx(context.Context, func(context.Context, controlsStore) error) error
	UpsertKeyQuotaPolicy(context.Context, quotaPolicyWrite) (quotaPolicyRow, error)
	GetAPIKeyQuotaPolicy(context.Context, int64, int64, int64) (quotaPolicyRow, error)
	SetAPIKeyQuotaPolicyID(context.Context, quotaPolicyLink) (int64, error)
	ValidateGroupBelongsToTenant(context.Context, int64, int64) (groupRow, error)
	SetAPIKeyGroupID(context.Context, groupAssignment) (int64, error)
	GetAPIKeyGroup(context.Context, int64, int64, int64) (keyGroupRow, error)
	SetAPIKeyIPAllowlist(context.Context, ipAllowlistAssignment) (int64, error)
	GetAPIKeyIPAllowlist(context.Context, int64, int64, int64) (keyIPAllowlistRow, error)
	SetAPIKeyModelAllowlist(context.Context, modelAllowlistAssignment) (int64, error)
	GetAPIKeyModelAllowlist(context.Context, int64, int64, int64) (keyModelAllowlistRow, error)
	// KEY-016:IP blacklist(与 allowlist 对称)
	SetAPIKeyIPBlacklist(context.Context, ipBlacklistAssignment) (int64, error)
	GetAPIKeyIPBlacklist(context.Context, int64, int64, int64) (keyIPBlacklistRow, error)
}

type quotaPolicyWrite struct {
	TenantID      int64
	UserID        int64
	APIKeyID      int64
	ScopeID       string
	Metric        quota.Metric
	WindowKind    quota.WindowKind
	WindowSeconds int32
	LimitUSD      decimal.Decimal
	Mode          quota.Mode
	ValidFrom     time.Time
	Actor         string
}

type quotaPolicyLink struct {
	TenantID int64
	UserID   int64
	APIKeyID int64
	PolicyID int64
}

type quotaPolicyRow struct {
	APIKeyID      int64
	TenantID      int64
	ID            int64
	ScopeKind     quota.ScopeKind
	ScopeID       string
	Metric        quota.Metric
	WindowKind    quota.WindowKind
	WindowSeconds int32
	LimitUSD      decimal.Decimal
	Mode          quota.Mode
	Priority      int32
	Enabled       bool
	ValidFrom     time.Time
	ValidUntil    *time.Time
}

type groupAssignment struct {
	TenantID int64
	UserID   int64
	APIKeyID int64
	GroupID  *int64
}

type groupRow struct {
	ID          int64
	Name        string
	Description string
	Enabled     bool
}

type keyGroupRow struct {
	APIKeyID         int64
	GroupID          *int64
	GroupName        string
	GroupDescription string
	GroupEnabled     *bool
}

type ipAllowlistAssignment struct {
	TenantID    int64
	UserID      int64
	APIKeyID    int64
	IPAllowlist *string
}

type keyIPAllowlistRow struct {
	APIKeyID    int64
	IPAllowlist *string
}

type modelAllowlistAssignment struct {
	TenantID      int64
	UserID        int64
	APIKeyID      int64
	AllowedModels *string
}

type keyModelAllowlistRow struct {
	APIKeyID      int64
	AllowedModels *string
}

// KEY-016:IP blacklist 的 store 类型(与 allowlist 对称)
type ipBlacklistAssignment struct {
	TenantID    int64
	UserID      int64
	APIKeyID    int64
	IPBlacklist *string
}

type keyIPBlacklistRow struct {
	APIKeyID    int64
	IPBlacklist *string
}

type PostgresStore struct {
	pool *pgxpool.Pool
	q    dbuserkeycontrols.Querier
	db   dbuserkeycontrols.DBTX // 供手写查询(blacklist)使用的原始 DBTX
}

func newPGControlsStore(pool *pgxpool.Pool) *PostgresStore {
	if pool == nil {
		return &PostgresStore{}
	}
	return &PostgresStore{pool: pool, q: dbuserkeycontrols.New(pool), db: pool}
}

func NewPostgresStore(pool *pgxpool.Pool) *PostgresStore {
	return newPGControlsStore(pool)
}

func (s *PostgresStore) WithTx(ctx context.Context, fn func(context.Context, controlsStore) error) error {
	if s == nil || s.pool == nil {
		return fmt.Errorf("%w: pool unset", ErrServiceMisconfig)
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("%w: begin: %v", ErrBackend, err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	txStore := &PostgresStore{q: dbuserkeycontrols.New(tx), db: tx}
	if err := fn(ctx, txStore); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("%w: commit: %v", ErrBackend, err)
	}
	return nil
}

func (s *PostgresStore) UpsertKeyQuotaPolicy(ctx context.Context, arg quotaPolicyWrite) (quotaPolicyRow, error) {
	if s == nil || s.q == nil {
		return quotaPolicyRow{}, fmt.Errorf("%w: queries unset", ErrServiceMisconfig)
	}
	limit, err := encodeNumeric(arg.LimitUSD)
	if err != nil {
		return quotaPolicyRow{}, err
	}
	row, err := s.q.UpsertAPIKeyQuotaPolicy(ctx, dbuserkeycontrols.UpsertAPIKeyQuotaPolicyParams{
		TenantID:      arg.TenantID,
		ScopeID:       arg.ScopeID,
		Metric:        string(arg.Metric),
		WindowKind:    string(arg.WindowKind),
		WindowSeconds: arg.WindowSeconds,
		LimitValue:    limit,
		Mode:          string(arg.Mode),
		ValidFrom:     timestamptz(arg.ValidFrom),
		Actor:         arg.Actor,
		APIKeyID:      arg.APIKeyID,
		UserID:        arg.UserID,
	})
	if err != nil {
		if isNoRows(err) {
			return quotaPolicyRow{}, ErrKeyNotFound
		}
		return quotaPolicyRow{}, fmt.Errorf("%w: upsert quota policy: %v", ErrBackend, err)
	}
	return quotaPolicyFromUpsert(row)
}

func (s *PostgresStore) GetAPIKeyQuotaPolicy(ctx context.Context, tenantID, userID, apiKeyID int64) (quotaPolicyRow, error) {
	if s == nil || s.q == nil {
		return quotaPolicyRow{}, fmt.Errorf("%w: queries unset", ErrServiceMisconfig)
	}
	row, err := s.q.GetAPIKeyQuotaPolicy(ctx, dbuserkeycontrols.GetAPIKeyQuotaPolicyParams{
		APIKeyID: apiKeyID,
		TenantID: tenantID,
		UserID:   userID,
	})
	if err != nil {
		return quotaPolicyRow{}, err
	}
	return quotaPolicyFromGet(row)
}

func (s *PostgresStore) SetAPIKeyQuotaPolicyID(ctx context.Context, arg quotaPolicyLink) (int64, error) {
	if s == nil || s.q == nil {
		return 0, fmt.Errorf("%w: queries unset", ErrServiceMisconfig)
	}
	return s.q.SetAPIKeyQuotaPolicyID(ctx, dbuserkeycontrols.SetAPIKeyQuotaPolicyIDParams{
		QuotaPolicyID: arg.PolicyID,
		APIKeyID:      arg.APIKeyID,
		TenantID:      arg.TenantID,
		UserID:        arg.UserID,
	})
}

func (s *PostgresStore) ValidateGroupBelongsToTenant(ctx context.Context, tenantID, groupID int64) (groupRow, error) {
	if s == nil || s.q == nil {
		return groupRow{}, fmt.Errorf("%w: queries unset", ErrServiceMisconfig)
	}
	row, err := s.q.ValidateGroupBelongsToTenant(ctx, dbuserkeycontrols.ValidateGroupBelongsToTenantParams{
		TenantID: tenantID,
		GroupID:  groupID,
	})
	if err != nil {
		return groupRow{}, err
	}
	return groupRow{ID: row.ID, Name: row.Name, Description: row.Description, Enabled: row.Enabled}, nil
}

func (s *PostgresStore) SetAPIKeyGroupID(ctx context.Context, arg groupAssignment) (int64, error) {
	if s == nil || s.q == nil {
		return 0, fmt.Errorf("%w: queries unset", ErrServiceMisconfig)
	}
	return s.q.SetAPIKeyGroupID(ctx, dbuserkeycontrols.SetAPIKeyGroupIDParams{
		KeyGroupID: arg.GroupID,
		APIKeyID:   arg.APIKeyID,
		TenantID:   arg.TenantID,
		UserID:     arg.UserID,
	})
}

func (s *PostgresStore) GetAPIKeyGroup(ctx context.Context, tenantID, userID, apiKeyID int64) (keyGroupRow, error) {
	if s == nil || s.q == nil {
		return keyGroupRow{}, fmt.Errorf("%w: queries unset", ErrServiceMisconfig)
	}
	row, err := s.q.GetAPIKeyGroup(ctx, dbuserkeycontrols.GetAPIKeyGroupParams{
		APIKeyID: apiKeyID,
		TenantID: tenantID,
		UserID:   userID,
	})
	if err != nil {
		return keyGroupRow{}, err
	}
	out := keyGroupRow{APIKeyID: row.APIKeyID, GroupID: row.KeyGroupID}
	if row.GroupName != nil {
		out.GroupName = *row.GroupName
	}
	if row.GroupDescription != nil {
		out.GroupDescription = *row.GroupDescription
	}
	out.GroupEnabled = row.GroupEnabled
	return out, nil
}

func (s *PostgresStore) SetAPIKeyIPAllowlist(ctx context.Context, arg ipAllowlistAssignment) (int64, error) {
	if s == nil || s.q == nil {
		return 0, fmt.Errorf("%w: queries unset", ErrServiceMisconfig)
	}
	return s.q.SetAPIKeyIPAllowlist(ctx, dbuserkeycontrols.SetAPIKeyIPAllowlistParams{
		IpAllowlist: arg.IPAllowlist,
		APIKeyID:    arg.APIKeyID,
		TenantID:    arg.TenantID,
		UserID:      arg.UserID,
	})
}

func (s *PostgresStore) GetAPIKeyIPAllowlist(ctx context.Context, tenantID, userID, apiKeyID int64) (keyIPAllowlistRow, error) {
	if s == nil || s.q == nil {
		return keyIPAllowlistRow{}, fmt.Errorf("%w: queries unset", ErrServiceMisconfig)
	}
	row, err := s.q.GetAPIKeyIPAllowlist(ctx, dbuserkeycontrols.GetAPIKeyIPAllowlistParams{
		APIKeyID: apiKeyID,
		TenantID: tenantID,
		UserID:   userID,
	})
	if err != nil {
		return keyIPAllowlistRow{}, err
	}
	return keyIPAllowlistRow{APIKeyID: row.APIKeyID, IPAllowlist: row.IpAllowlist}, nil
}

func (s *PostgresStore) SetAPIKeyModelAllowlist(ctx context.Context, arg modelAllowlistAssignment) (int64, error) {
	if s == nil || s.q == nil {
		return 0, fmt.Errorf("%w: queries unset", ErrServiceMisconfig)
	}
	return s.q.SetAPIKeyModelAllowlist(ctx, dbuserkeycontrols.SetAPIKeyModelAllowlistParams{
		AllowedModels: arg.AllowedModels,
		APIKeyID:      arg.APIKeyID,
		TenantID:      arg.TenantID,
		UserID:        arg.UserID,
	})
}

func (s *PostgresStore) GetAPIKeyModelAllowlist(ctx context.Context, tenantID, userID, apiKeyID int64) (keyModelAllowlistRow, error) {
	if s == nil || s.q == nil {
		return keyModelAllowlistRow{}, fmt.Errorf("%w: queries unset", ErrServiceMisconfig)
	}
	row, err := s.q.GetAPIKeyModelAllowlist(ctx, dbuserkeycontrols.GetAPIKeyModelAllowlistParams{
		APIKeyID: apiKeyID,
		TenantID: tenantID,
		UserID:   userID,
	})
	if err != nil {
		return keyModelAllowlistRow{}, err
	}
	return keyModelAllowlistRow{APIKeyID: row.APIKeyID, AllowedModels: row.AllowedModels}, nil
}

func quotaPolicyFromUpsert(row dbuserkeycontrols.UpsertAPIKeyQuotaPolicyRow) (quotaPolicyRow, error) {
	limit, err := decodeNumeric(row.LimitValue)
	if err != nil {
		return quotaPolicyRow{}, err
	}
	return quotaPolicyRow{
		APIKeyID:      row.APIKeyID,
		TenantID:      row.TenantID,
		ID:            row.ID,
		ScopeKind:     quota.ScopeKind(row.ScopeKind),
		ScopeID:       row.ScopeID,
		Metric:        quota.Metric(row.Metric),
		WindowKind:    quota.WindowKind(row.WindowKind),
		WindowSeconds: row.WindowSeconds,
		LimitUSD:      limit,
		Mode:          quota.Mode(row.Mode),
		Priority:      row.Priority,
		Enabled:       row.Enabled,
		ValidFrom:     row.ValidFrom.Time,
		ValidUntil:    timePtr(row.ValidUntil),
	}, nil
}

func quotaPolicyFromGet(row dbuserkeycontrols.GetAPIKeyQuotaPolicyRow) (quotaPolicyRow, error) {
	policy, err := quotaPolicyFromUpsert(dbuserkeycontrols.UpsertAPIKeyQuotaPolicyRow{
		APIKeyID:      row.APIKeyID,
		TenantID:      row.TenantID,
		ID:            row.ID,
		ScopeKind:     row.ScopeKind,
		ScopeID:       row.ScopeID,
		Metric:        row.Metric,
		WindowKind:    row.WindowKind,
		WindowSeconds: row.WindowSeconds,
		LimitValue:    row.LimitValue,
		Mode:          row.Mode,
		Priority:      row.Priority,
		Enabled:       row.Enabled,
		ValidFrom:     row.ValidFrom,
		ValidUntil:    row.ValidUntil,
	})
	if err != nil {
		return quotaPolicyRow{}, err
	}
	policy.APIKeyID = row.APIKeyID
	return policy, nil
}

func encodeNumeric(d decimal.Decimal) (pgtype.Numeric, error) {
	var n pgtype.Numeric
	if err := n.Scan(d.String()); err != nil {
		return pgtype.Numeric{}, fmt.Errorf("%w: encode numeric: %v", ErrInvalidQuota, err)
	}
	return n, nil
}

func decodeNumeric(n pgtype.Numeric) (decimal.Decimal, error) {
	if !n.Valid || n.Int == nil {
		return decimal.Decimal{}, fmt.Errorf("%w: invalid numeric", ErrBackend)
	}
	return decimal.NewFromBigInt(n.Int, n.Exp), nil
}

func timestamptz(t time.Time) pgtype.Timestamptz {
	if t.IsZero() {
		t = time.Now().UTC()
	}
	return pgtype.Timestamptz{Time: t.UTC(), Valid: true}
}

func timePtr(t pgtype.Timestamptz) *time.Time {
	if !t.Valid {
		return nil
	}
	out := t.Time
	return &out
}

func (s *PostgresStore) SetAPIKeyIPBlacklist(ctx context.Context, arg ipBlacklistAssignment) (int64, error) {
	if s == nil || s.db == nil {
		return 0, fmt.Errorf("%w: db unset", ErrServiceMisconfig)
	}
	result, err := s.db.Exec(ctx,
		`UPDATE api_keys
		    SET ip_blacklist = $1::text, updated_at = NOW()
		  WHERE id = $2
		    AND tenant_id = $3
		    AND user_id = $4
		    AND deleted_at IS NULL`,
		arg.IPBlacklist, arg.APIKeyID, arg.TenantID, arg.UserID,
	)
	if err != nil {
		return 0, fmt.Errorf("%w: set ip blacklist: %v", ErrBackend, err)
	}
	return result.RowsAffected(), nil
}

func (s *PostgresStore) GetAPIKeyIPBlacklist(ctx context.Context, tenantID, userID, apiKeyID int64) (keyIPBlacklistRow, error) {
	if s == nil || s.db == nil {
		return keyIPBlacklistRow{}, fmt.Errorf("%w: db unset", ErrServiceMisconfig)
	}
	row := s.db.QueryRow(ctx,
		`SELECT id, ip_blacklist
		   FROM api_keys
		  WHERE id = $1
		    AND tenant_id = $2
		    AND user_id = $3
		    AND deleted_at IS NULL`,
		apiKeyID, tenantID, userID,
	)
	var out keyIPBlacklistRow
	if err := row.Scan(&out.APIKeyID, &out.IPBlacklist); err != nil {
		return keyIPBlacklistRow{}, err
	}
	return out, nil
}

func isNoRows(err error) bool {
	return errors.Is(err, pgx.ErrNoRows)
}
