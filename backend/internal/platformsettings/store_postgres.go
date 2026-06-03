package platformsettings

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	admindb "github.com/BloomingProsperity/HUAKAI/internal/db/admin"
	dbplatformsettings "github.com/BloomingProsperity/HUAKAI/internal/db/platformsettings"
)

type platformSettingQueries interface {
	AcquirePlatformSettingLock(context.Context, dbplatformsettings.AcquirePlatformSettingLockParams) error
	GetPlatformSettingForUpdate(context.Context, dbplatformsettings.GetPlatformSettingForUpdateParams) (dbplatformsettings.PlatformSetting, error)
	GetPlatformSetting(context.Context, dbplatformsettings.GetPlatformSettingParams) (dbplatformsettings.PlatformSetting, error)
	ListPlatformSettingsByScope(context.Context, string) ([]dbplatformsettings.PlatformSetting, error)
	UpsertPlatformSetting(context.Context, dbplatformsettings.UpsertPlatformSettingParams) (dbplatformsettings.PlatformSetting, error)
}

type postgresAuditQueries interface {
	InsertAdminAuditEvent(context.Context, admindb.InsertAdminAuditEventParams) (admindb.InsertAdminAuditEventRow, error)
}

type PostgresStore struct {
	pool *pgxpool.Pool
	q    platformSettingQueries
}

func NewPostgresStore(pool *pgxpool.Pool) *PostgresStore {
	if pool == nil {
		return &PostgresStore{}
	}
	return &PostgresStore{pool: pool, q: dbplatformsettings.New(pool)}
}

func (s *PostgresStore) Get(ctx context.Context, scope, key string) (StoredSetting, bool, error) {
	if s == nil || s.q == nil {
		return StoredSetting{}, false, ErrStoreNotConfigured
	}
	scope, key = strings.TrimSpace(scope), strings.TrimSpace(key)
	if scope == "" || key == "" {
		return StoredSetting{}, false, fmt.Errorf("%w: scope/key", ErrInvalidValue)
	}
	row, err := s.q.GetPlatformSetting(ctx, dbplatformsettings.GetPlatformSettingParams{
		Scope:      scope,
		SettingKey: key,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return StoredSetting{}, false, nil
	}
	if err != nil {
		return StoredSetting{}, false, err
	}
	return storedSettingFromDB(row), true, nil
}

func (s *PostgresStore) List(ctx context.Context, scope string) ([]StoredSetting, error) {
	if s == nil || s.q == nil {
		return nil, ErrStoreNotConfigured
	}
	scope = strings.TrimSpace(scope)
	if scope == "" {
		return nil, fmt.Errorf("%w: scope", ErrInvalidValue)
	}
	rows, err := s.q.ListPlatformSettingsByScope(ctx, scope)
	if err != nil {
		return nil, err
	}
	out := make([]StoredSetting, 0, len(rows))
	for _, row := range rows {
		out = append(out, storedSettingFromDB(row))
	}
	return out, nil
}

func (s *PostgresStore) Upsert(ctx context.Context, scope, key, value, updatedBy string) (StoredSetting, error) {
	if s == nil || s.q == nil {
		return StoredSetting{}, ErrStoreNotConfigured
	}
	scope, key = strings.TrimSpace(scope), strings.TrimSpace(key)
	value, updatedBy = strings.TrimSpace(value), strings.TrimSpace(updatedBy)
	if scope == "" || key == "" || value == "" {
		return StoredSetting{}, fmt.Errorf("%w: scope/key/value", ErrInvalidValue)
	}
	if updatedBy == "" {
		updatedBy = "system"
	}
	row, err := s.q.UpsertPlatformSetting(ctx, dbplatformsettings.UpsertPlatformSettingParams{
		Scope:        scope,
		SettingKey:   key,
		SettingValue: value,
		UpdatedBy:    updatedBy,
	})
	if err != nil {
		return StoredSetting{}, err
	}
	return storedSettingFromDB(row), nil
}

func (s *PostgresStore) UpsertWithAudit(ctx context.Context, in UpsertInput) (StoredSetting, error) {
	if s == nil || s.pool == nil {
		return StoredSetting{}, ErrStoreNotConfigured
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return StoredSetting{}, err
	}
	return runAuditedUpsertTx(ctx, tx, in)
}

func runAuditedUpsertTx(ctx context.Context, tx pgx.Tx, in UpsertInput) (StoredSetting, error) {
	committed := false
	defer rollbackUnlessCommitted(tx, &committed)
	pq := dbplatformsettings.New(tx)
	aq := admindb.New(tx)
	updated, err := auditedUpsert(ctx, pq, aq, in)
	if err != nil {
		return StoredSetting{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return StoredSetting{}, err
	}
	committed = true
	return updated, nil
}

func auditedUpsert(ctx context.Context, pq platformSettingQueries, aq postgresAuditQueries, in UpsertInput) (StoredSetting, error) {
	oldSetting, err := readPreviousSetting(ctx, pq, in.Key)
	if err != nil {
		return StoredSetting{}, err
	}
	updated, err := writePlatformSetting(ctx, pq, in)
	if err != nil {
		return StoredSetting{}, err
	}
	params := auditParamsFromInput(in, oldSetting, updated)
	if err := insertAdminAudit(ctx, aq, params); err != nil {
		return StoredSetting{}, err
	}
	return updated, nil
}

func readPreviousSetting(ctx context.Context, q platformSettingQueries, key SettingKey) (StoredSetting, error) {
	if err := q.AcquirePlatformSettingLock(ctx, dbplatformsettings.AcquirePlatformSettingLockParams{
		SettingKey: string(key), Scope: GlobalScope,
	}); err != nil {
		return StoredSetting{}, err
	}
	row, err := q.GetPlatformSettingForUpdate(ctx, dbplatformsettings.GetPlatformSettingForUpdateParams{
		Scope: GlobalScope, SettingKey: string(key),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return defaultSetting(key), nil
	}
	if err != nil {
		return StoredSetting{}, err
	}
	return normalizeStoredSetting(storedSettingFromDB(row), SourceDB)
}

func writePlatformSetting(ctx context.Context, q platformSettingQueries, in UpsertInput) (StoredSetting, error) {
	row, err := q.UpsertPlatformSetting(ctx, dbplatformsettings.UpsertPlatformSettingParams{
		Scope: GlobalScope, SettingKey: string(in.Key), SettingValue: in.Value, UpdatedBy: in.UpdatedBy,
	})
	if err != nil {
		return StoredSetting{}, err
	}
	return normalizeStoredSetting(storedSettingFromDB(row), SourceDB)
}

func insertAdminAudit(ctx context.Context, q postgresAuditQueries, params AuditParams) error {
	sink := NewAdminAuditSink(q)
	return sink.WriteAdminAudit(ctx, params)
}

func rollbackUnlessCommitted(tx pgx.Tx, committed *bool) {
	if *committed {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = tx.Rollback(ctx)
}

func storedSettingFromDB(row dbplatformsettings.PlatformSetting) StoredSetting {
	return StoredSetting{
		ID:        row.ID,
		Scope:     row.Scope,
		Key:       SettingKey(row.SettingKey),
		Value:     row.SettingValue,
		UpdatedAt: pgTime(row.UpdatedAt),
		UpdatedBy: row.UpdatedBy,
	}
}

func pgTime(ts pgtype.Timestamptz) time.Time {
	if !ts.Valid {
		return time.Time{}
	}
	return ts.Time.UTC()
}

var _ Store = (*PostgresStore)(nil)
var _ AtomicStore = (*PostgresStore)(nil)
var _ postgresAuditQueries = (*admindb.Queries)(nil)
var _ platformSettingQueries = (*dbplatformsettings.Queries)(nil)
