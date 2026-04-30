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

package pool

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BloomingProsperity/HUAKAI/internal/db"
)

// DefaultLeaseDuration is the slot lease grace window before orphan-sweep
// can reclaim it (Phase 4.5 sweeper not yet implemented).
const DefaultLeaseDuration = 90 * time.Second

// DBSlotManager binds the selector's slot acquire/release seam to real
// pool_slot_acquisitions + provider_accounts rows.
type DBSlotManager struct {
	pool *pgxpool.Pool
	q    *db.Queries
}

// NewDBSlotManager constructs the adapter from a pgx pool. The pool is
// required because Acquire opens its own Tx for the increment+insert atom.
func NewDBSlotManager(pool *pgxpool.Pool) *DBSlotManager {
	if pool == nil {
		return &DBSlotManager{}
	}
	return &DBSlotManager{pool: pool, q: db.New(pool)}
}

// Acquire implements pool.SlotManager.
//
// TODO(phase-e): wrap the body in a Serializable retry loop. PostgreSQL can
// return SQLSTATE 40001 (serialization_failure) under concurrent
// IncrementInFlightCount on the same provider_account. Phase C smoke runs
// single-request so will not hit this; production hot path will. Current
// behavior surfaces 40001 as a fatal slot error → request fails. Codex
// pass2 P2 finding 2026-04-30; deferred to Phase E along with the rest of
// the production contention story.
func (m *DBSlotManager) Acquire(ctx context.Context, account *AccountSnapshot, req SelectionRequest) (*AcquireResult, error) {
	if m == nil || m.pool == nil {
		return nil, ErrSlotManagerUnavailable
	}
	if account == nil {
		return nil, errors.New("pool: nil account snapshot")
	}

	tx, err := m.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return nil, fmt.Errorf("pool: begin acquire tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	qtx := m.q.WithTx(tx)
	rows, err := qtx.IncrementInFlightCount(ctx, db.IncrementInFlightCountParams{
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
	if _, err := qtx.InsertSlotAcquisition(ctx, db.InsertSlotAcquisitionParams{
		TenantID:          account.TenantID,
		ProviderAccountID: account.ID,
		AcquisitionToken:  token,
		ClaimID:           claimID,
		AttemptSeq:        int32(req.AttemptSeq),
		LeaseExpiresAt: pgtype.Timestamptz{
			Time:  time.Now().Add(DefaultLeaseDuration).UTC(),
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
		if _, err := m.q.ReleaseSlotAndDecrementInFlight(ctx, db.ReleaseSlotAndDecrementInFlightParams{
			AcquisitionToken: token,
			ReleaseReason:    &reason,
		}); err != nil {
			return fmt.Errorf("pool: release slot: %w", err)
		}
		return nil
	}
}

var _ SlotManager = (*DBSlotManager)(nil)
