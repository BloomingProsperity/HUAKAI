// Phase C.2 production adapter: DB-backed pool.SlotManager.
//
// Real Acquire/Release path:
//   - Acquire opens a Serializable Tx that increments
//     provider_accounts.in_flight_count and inserts a fresh
//     pool_slot_acquisitions row (status='acquired') in the same Tx.
//     On cap_concurrency overflow IncrementInFlightCount returns 0 rows,
//     which we map to ErrNoSlotAvailable.
//   - Release returns an idempotent ReleaseFunc that calls the same
//     ReleaseSlotAndDecrementInFlight CTE used by the settler. Calling
//     twice is a no-op because the CTE only flips status='acquired'
//     rows and only-then decrements.

package dispatcher

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	dbbilling "github.com/BloomingProsperity/HUAKAI/internal/db/billing"
)

// DefaultLeaseDuration is the slot lease grace window before orphan-slot
// recovery can reclaim it.
const DefaultLeaseDuration = 90 * time.Second

// DBSlotManager binds the selector's slot acquire/release seam to real
// pool_slot_acquisitions + provider_accounts rows.
type DBSlotManager struct {
	pool *pgxpool.Pool
	q    *dbbilling.Queries

	// leaseDuration overrides DefaultLeaseDuration when > 0. The zero value
	// falls back to DefaultLeaseDuration so an unconfigured manager keeps the
	// safe 90s grace window — operators tune it via
	// HUAKAI_POOL_SLOT_LEASE_DURATION_SECONDS, range-validated upstream in
	// config.PoolSelectorConfig.Validate to stay strictly above the orphan-
	// sweep cadence (billing.leaseSweepTickerInterval). This adapter only
	// stores the value; it does not re-validate the floor.
	leaseDuration time.Duration
}

// NewDBSlotManager constructs the adapter from a pgx pool. The pool is
// required because Acquire opens its own Tx for the increment+insert atom.
func NewDBSlotManager(pool *pgxpool.Pool) *DBSlotManager {
	if pool == nil {
		return &DBSlotManager{}
	}
	return &DBSlotManager{pool: pool, q: dbbilling.New(pool)}
}

// WithLeaseDuration sets the slot lease grace window written into
// pool_slot_acquisitions.lease_expires_at at Acquire time. A value <= 0 is
// ignored so the DefaultLeaseDuration fallback stands (zero-config → zero
// behavior change). The caller (selector wiring) owns floor validation; this
// setter only records the chosen value. Returns the receiver for chaining.
func (m *DBSlotManager) WithLeaseDuration(d time.Duration) *DBSlotManager {
	if m == nil {
		return m
	}
	if d > 0 {
		m.leaseDuration = d
	}
	return m
}

// effectiveLeaseDuration centralises the zero-value→DefaultLeaseDuration
// fallback in one place so the guard is unit-testable without a live DB and
// the Acquire path reads a single source of truth.
func (m *DBSlotManager) effectiveLeaseDuration() time.Duration {
	if m != nil && m.leaseDuration > 0 {
		return m.leaseDuration
	}
	return DefaultLeaseDuration
}

func (m *DBSlotManager) Acquire(ctx context.Context, account *AccountSnapshot, req SelectionRequest) (*AcquireResult, error) {
	if m == nil || m.pool == nil {
		return nil, ErrSlotManagerUnavailable
	}
	if account == nil {
		return nil, errors.New("pool: nil account snapshot")
	}
	// DR-001 跨表 tenant 一致性: 防 selector 在 req.TenantID 写 claim, 而
	// slot acquire 走 account.TenantID, 两边租户不一致会让 slot row 算到
	// 错租户名下, 后续 ReleaseSlotAndDecrement 跟 settlement 找不到对应。
	if req.TenantID != 0 && account.TenantID != req.TenantID {
		return nil, fmt.Errorf("pool: account tenant=%d ≠ request tenant=%d (cross-tenant slot acquire refused)",
			account.TenantID, req.TenantID)
	}
	return retrySerializableSlotAcquire(ctx, func(ctx context.Context) (*AcquireResult, error) {
		return m.acquireOnce(ctx, account, req)
	})
}

func (m *DBSlotManager) acquireOnce(ctx context.Context, account *AccountSnapshot, req SelectionRequest) (*AcquireResult, error) {
	tx, err := m.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return nil, fmt.Errorf("pool: begin acquire tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	qtx := m.q.WithTx(tx)
	rows, err := qtx.IncrementInFlightCount(ctx, dbbilling.IncrementInFlightCountParams{
		ID:       account.ID,
		TenantID: account.TenantID,
	})
	if err != nil {
		return nil, fmt.Errorf("pool: increment in_flight_count: %w", err)
	}
	if rows == 0 {
		return nil, ErrNoSlotAvailable
	}

	token := uuid.New()
	var claimID *int64
	if req.ClaimID != 0 {
		c := req.ClaimID
		claimID = &c
	}
	if _, err := qtx.InsertSlotAcquisition(ctx, dbbilling.InsertSlotAcquisitionParams{
		TenantID:          account.TenantID,
		ProviderAccountID: account.ID,
		AcquisitionToken:  token,
		ClaimID:           claimID,
		AttemptSeq:        int32(req.AttemptSeq),
		LeaseExpiresAt: pgtype.Timestamptz{
			Time:  time.Now().Add(m.effectiveLeaseDuration()).UTC(),
			Valid: true,
		},
	}); err != nil {
		return nil, fmt.Errorf("pool: insert slot acquisition: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("pool: commit acquire tx: %w", err)
	}

	releaseFn := NewIdempotentRelease(token, m.releaseFunc(token))
	return &AcquireResult{
		AcquisitionToken: token,
		Release:          releaseFn,
	}, nil
}

// releaseFunc returns the closure that reverses Acquire idempotently.
// The CTE inside ReleaseSlotAndDecrementInFlight only flips slots whose
// status is still 'acquired', so concurrent calls (e.g. settler vs
// selector cleanup) cannot double-decrement.
func (m *DBSlotManager) releaseFunc(token uuid.UUID) ReleaseFunc {
	return func(ctx context.Context) error {
		reason := "selector_release"
		if _, err := m.q.ReleaseSlotAndDecrementInFlight(ctx, dbbilling.ReleaseSlotAndDecrementInFlightParams{
			AcquisitionToken: token,
			ReleaseReason:    &reason,
		}); err != nil {
			return fmt.Errorf("pool: release slot: %w", err)
		}
		return nil
	}
}

var _ SlotManager = (*DBSlotManager)(nil)
