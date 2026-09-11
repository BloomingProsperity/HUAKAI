package modelrate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/sign"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"
)

const overrideCacheTTL = 2 * time.Second

type beginTxFunc func(context.Context, pgx.TxOptions) (pgx.Tx, error)

// Store 持久化平台级绝对价覆盖,并作为热路径价表叠加器。
type Store struct {
	pool    *pgxpool.Pool
	signer  *sign.Signer
	now     func() time.Time
	beginTx beginTxFunc

	mu       sync.RWMutex
	cached   []Override
	cachedAt time.Time
	cacheGen uint64

	// afterListLoad 仅测试注入:在读库之后、写回内存缓存之前运行,用来复现写后失效竞态。
	afterListLoad func()
}

func NewPostgresStore(pool *pgxpool.Pool, signer *sign.Signer) *Store {
	store := &Store{pool: pool, signer: signer, now: time.Now}
	if pool != nil {
		store.beginTx = pool.BeginTx
	}
	return store
}

func (s *Store) OverlayPricingData(ctx context.Context, data json.RawMessage) (json.RawMessage, error) {
	overrides, err := s.List(ctx)
	if err != nil {
		return nil, err
	}
	if len(overrides) == 0 {
		return data, nil
	}
	return ApplyOverrides(data, overrides)
}

func (s *Store) Invalidate() {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.cacheGen++
	s.cached = nil
	s.cachedAt = time.Time{}
	s.mu.Unlock()
}

func (s *Store) List(ctx context.Context) ([]Override, error) {
	if s == nil || s.pool == nil {
		return nil, ErrStoreNotConfigured
	}
	now := s.now()
	s.mu.RLock()
	if !s.cachedAt.IsZero() && now.Sub(s.cachedAt) < overrideCacheTTL {
		out := append([]Override(nil), s.cached...)
		s.mu.RUnlock()
		return out, nil
	}
	gen := s.cacheGen
	s.mu.RUnlock()

	rows, err := s.pool.Query(ctx, `
SELECT id, vendor, model,
       input_micro_usd::text, output_micro_usd::text, cache_read_micro_usd::text,
       cache_creation_micro_usd::text, cache_creation_5m_micro_usd::text, cache_creation_1h_micro_usd::text,
       created_by, updated_by, created_at, updated_at
  FROM model_rate_overrides
 ORDER BY vendor, model`)
	if err != nil {
		return nil, fmt.Errorf("%w: list model rate overrides: %w", ErrBackend, err)
	}
	defer rows.Close()

	var out []Override
	for rows.Next() {
		item, err := scanOverride(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("%w: iterate model rate overrides: %w", ErrBackend, err)
	}
	if s.afterListLoad != nil {
		s.afterListLoad()
	}

	s.mu.Lock()
	if s.cacheGen == gen {
		s.cached = append([]Override(nil), out...)
		s.cachedAt = now
	}
	s.mu.Unlock()
	return out, nil
}

func (s *Store) Get(ctx context.Context, vendor, model string) (Override, error) {
	if s == nil || s.pool == nil {
		return Override{}, ErrStoreNotConfigured
	}
	vendor = normalizeVendor(vendor)
	model = normalizeModel(model)
	row := s.pool.QueryRow(ctx, `
SELECT id, vendor, model,
       input_micro_usd::text, output_micro_usd::text, cache_read_micro_usd::text,
       cache_creation_micro_usd::text, cache_creation_5m_micro_usd::text, cache_creation_1h_micro_usd::text,
       created_by, updated_by, created_at, updated_at
  FROM model_rate_overrides
 WHERE vendor = $1 AND model = $2`, vendor, model)
	item, err := scanOverride(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return Override{}, ErrNotFound
	}
	if err != nil {
		return Override{}, fmt.Errorf("%w: get model rate override: %w", ErrBackend, err)
	}
	return item, nil
}

func (s *Store) Upsert(ctx context.Context, p UpsertParams) (Override, error) {
	if s == nil || s.pool == nil {
		return Override{}, ErrStoreNotConfigured
	}
	if err := validateUpsert(p); err != nil {
		return Override{}, err
	}
	if s.signer == nil {
		return Override{}, ErrAuditSignerMissing
	}
	if s.beginTx == nil {
		return Override{}, ErrAuditTxMissing
	}
	p.Vendor = normalizeVendor(p.Vendor)
	p.Model = normalizeModel(p.Model)
	p.Actor = strings.TrimSpace(p.Actor)
	p.ActorRole = strings.TrimSpace(p.ActorRole)

	var out Override
	if err := s.withWriteTx(ctx, func(tx pgx.Tx) error {
		old, hasOld, err := getOverrideInTx(ctx, tx, p.Vendor, p.Model)
		if err != nil {
			return err
		}
		row := tx.QueryRow(ctx, `
INSERT INTO model_rate_overrides (
    vendor, model,
    input_micro_usd, output_micro_usd, cache_read_micro_usd,
    cache_creation_micro_usd, cache_creation_5m_micro_usd, cache_creation_1h_micro_usd,
    created_by, updated_by
) VALUES ($1, $2, $3::text::numeric, $4::text::numeric, $5::text::numeric, $6::text::numeric, $7::text::numeric, $8::text::numeric, $9, $9)
ON CONFLICT (vendor, model) DO UPDATE SET
    input_micro_usd = EXCLUDED.input_micro_usd,
    output_micro_usd = EXCLUDED.output_micro_usd,
    cache_read_micro_usd = EXCLUDED.cache_read_micro_usd,
    cache_creation_micro_usd = EXCLUDED.cache_creation_micro_usd,
    cache_creation_5m_micro_usd = EXCLUDED.cache_creation_5m_micro_usd,
    cache_creation_1h_micro_usd = EXCLUDED.cache_creation_1h_micro_usd,
    updated_by = EXCLUDED.updated_by,
    updated_at = now()
RETURNING id, vendor, model,
          input_micro_usd::text, output_micro_usd::text, cache_read_micro_usd::text,
          cache_creation_micro_usd::text, cache_creation_5m_micro_usd::text, cache_creation_1h_micro_usd::text,
          created_by, updated_by, created_at, updated_at`,
			p.Vendor, p.Model,
			decimalArg(p.Rates.Input),
			decimalArg(p.Rates.Output),
			decimalArg(p.Rates.CacheRead),
			decimalArg(p.Rates.CacheCreation),
			decimalArg(p.Rates.CacheCreation5m),
			decimalArg(p.Rates.CacheCreation1h),
			p.Actor,
		)
		out, err = scanOverride(row)
		if err != nil {
			return fmt.Errorf("%w: upsert model rate override: %w", ErrBackend, err)
		}
		event := auditEvent{
			OccurredAt: s.now().UTC(),
			ActorID:    p.Actor,
			ActorRole:  p.ActorRole,
			Vendor:     p.Vendor,
			Model:      p.Model,
			Action:     ActionUpsert,
			NewPayload: bucketsJSON(out.Rates),
		}
		if hasOld {
			event.OldPayload = bucketsJSON(old.Rates)
		}
		_, err = appendAuditInTx(ctx, tx, s.signer, event)
		return err
	}); err != nil {
		return Override{}, err
	}
	s.Invalidate()
	return out, nil
}

func (s *Store) Delete(ctx context.Context, p DeleteParams) error {
	if s == nil || s.pool == nil {
		return ErrStoreNotConfigured
	}
	if err := validateDelete(p); err != nil {
		return err
	}
	if s.signer == nil {
		return ErrAuditSignerMissing
	}
	if s.beginTx == nil {
		return ErrAuditTxMissing
	}
	p.Vendor = normalizeVendor(p.Vendor)
	p.Model = normalizeModel(p.Model)
	p.Actor = strings.TrimSpace(p.Actor)
	p.ActorRole = strings.TrimSpace(p.ActorRole)

	if err := s.withWriteTx(ctx, func(tx pgx.Tx) error {
		old, hasOld, err := getOverrideInTx(ctx, tx, p.Vendor, p.Model)
		if err != nil {
			return err
		}
		if !hasOld {
			return ErrNotFound
		}
		tag, err := tx.Exec(ctx, `DELETE FROM model_rate_overrides WHERE vendor = $1 AND model = $2`, p.Vendor, p.Model)
		if err != nil {
			return fmt.Errorf("%w: delete model rate override: %w", ErrBackend, err)
		}
		if tag.RowsAffected() == 0 {
			return ErrNotFound
		}
		_, err = appendAuditInTx(ctx, tx, s.signer, auditEvent{
			OccurredAt: s.now().UTC(),
			ActorID:    p.Actor,
			ActorRole:  p.ActorRole,
			Vendor:     p.Vendor,
			Model:      p.Model,
			Action:     ActionDelete,
			OldPayload: bucketsJSON(old.Rates),
		})
		return err
	}); err != nil {
		return err
	}
	s.Invalidate()
	return nil
}

func (s *Store) VerifyChain(ctx context.Context) (VerifyResult, error) {
	if s == nil || s.pool == nil {
		return VerifyResult{}, ErrStoreNotConfigured
	}
	if s.signer == nil {
		return VerifyResult{}, ErrAuditSignerMissing
	}
	entries, err := loadAuditEntries(ctx, s.pool)
	if err != nil {
		return VerifyResult{}, err
	}
	return VerifyAuditEntries(ctx, s.signer.PublicKey(), entries), nil
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanOverride(row rowScanner) (Override, error) {
	var (
		item                                            Override
		input, output, cacheRead                        *string
		cacheCreation, cacheCreation5m, cacheCreation1h *string
	)
	if err := row.Scan(
		&item.ID,
		&item.Vendor,
		&item.Model,
		&input,
		&output,
		&cacheRead,
		&cacheCreation,
		&cacheCreation5m,
		&cacheCreation1h,
		&item.CreatedBy,
		&item.UpdatedBy,
		&item.CreatedAt,
		&item.UpdatedAt,
	); err != nil {
		return Override{}, err
	}
	rates, err := bucketsFromText(input, output, cacheRead, cacheCreation, cacheCreation5m, cacheCreation1h)
	if err != nil {
		return Override{}, err
	}
	item.Rates = rates
	return item, nil
}

func getOverrideInTx(ctx context.Context, tx pgx.Tx, vendor, model string) (Override, bool, error) {
	row := tx.QueryRow(ctx, `
SELECT id, vendor, model,
       input_micro_usd::text, output_micro_usd::text, cache_read_micro_usd::text,
       cache_creation_micro_usd::text, cache_creation_5m_micro_usd::text, cache_creation_1h_micro_usd::text,
       created_by, updated_by, created_at, updated_at
  FROM model_rate_overrides
 WHERE vendor = $1 AND model = $2`, vendor, model)
	item, err := scanOverride(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return Override{}, false, nil
	}
	if err != nil {
		return Override{}, false, fmt.Errorf("%w: read existing override: %w", ErrBackend, err)
	}
	return item, true, nil
}

func bucketsFromText(values ...*string) (RateBuckets, error) {
	parse := func(raw *string) (*decimal.Decimal, error) {
		if raw == nil || strings.TrimSpace(*raw) == "" {
			return nil, nil
		}
		value, err := decimal.NewFromString(strings.TrimSpace(*raw))
		if err != nil {
			return nil, fmt.Errorf("%w: decimal: %v", ErrInvalidInput, err)
		}
		return &value, nil
	}
	var out RateBuckets
	var err error
	if out.Input, err = parse(values[0]); err != nil {
		return RateBuckets{}, err
	}
	if out.Output, err = parse(values[1]); err != nil {
		return RateBuckets{}, err
	}
	if out.CacheRead, err = parse(values[2]); err != nil {
		return RateBuckets{}, err
	}
	if out.CacheCreation, err = parse(values[3]); err != nil {
		return RateBuckets{}, err
	}
	if out.CacheCreation5m, err = parse(values[4]); err != nil {
		return RateBuckets{}, err
	}
	if out.CacheCreation1h, err = parse(values[5]); err != nil {
		return RateBuckets{}, err
	}
	return out, nil
}

const writeRetryLimit = 8

func (s *Store) withWriteTx(ctx context.Context, fn func(pgx.Tx) error) error {
	if s.beginTx == nil {
		return ErrAuditTxMissing
	}
	var last error
	for attempt := 0; attempt < writeRetryLimit; attempt++ {
		err := s.runWriteTx(ctx, fn)
		if err == nil || !isRetryableWriteConflict(err) {
			return err
		}
		last = err
		if ctx.Err() != nil {
			return ctx.Err()
		}
	}
	return last
}

func (s *Store) runWriteTx(ctx context.Context, fn func(pgx.Tx) error) error {
	tx, err := s.beginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return fmt.Errorf("%w: begin write: %w", ErrBackend, err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", auditLockKey()); err != nil {
		return fmt.Errorf("%w: model rate write lock: %w", ErrBackend, err)
	}
	if err := fn(tx); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("%w: commit write: %w", ErrBackend, err)
	}
	return nil
}

func isRetryableWriteConflict(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && (pgErr.Code == "40001" || pgErr.Code == "40P01")
}

func decimalArg(value *decimal.Decimal) any {
	if value == nil {
		return nil
	}
	return value.String()
}
