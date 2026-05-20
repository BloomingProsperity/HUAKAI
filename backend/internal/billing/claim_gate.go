package billing

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"

	dbbilling "github.com/BloomingProsperity/HUAKAI/internal/db/billing"
)

// Sentinel errors per F-OBS-001 Failure Path classes.
var (
	// ErrFingerprintConflict ↔ spec §Failure Path TX1_FINGERPRINT_CONFLICT.
	// Same logical_request_id was already reserved with a different payload hash —
	// signals replay attack.
	ErrFingerprintConflict = errors.New("billing: TX1_FINGERPRINT_CONFLICT")

	// ErrClaimRace ↔ spec §Failure Path TX1_CLAIM_RACE.
	// Concurrent attempt won the idempotency claim; gateway should re-read
	// the settled response within a bounded retry budget.
	ErrClaimRace = errors.New("billing: TX1_CLAIM_RACE")

	// ErrPoolNotConfigured fires when DefaultClaimGate is constructed without
	// a real pgxpool.Pool. Per integration sprint contract: "function returns
	// a typed error, not a 200 OK" when PG is unreachable.
	ErrPoolNotConfigured = errors.New("billing: pgx pool not configured")
)

// DefaultClaimGate is the production-grade Tx1 ClaimGate backed by PostgreSQL
// via pgx + sqlc. Constructed via NewClaimGate(pool); methods always run a
// transaction and never silently succeed when stores are missing.
type DefaultClaimGate struct {
	pool *pgxpool.Pool
	q    *dbbilling.Queries
	// Lease window for claim row orphan-sweep recovery; default 90s.
	LeaseWindow time.Duration
}

// NewClaimGate constructs a DefaultClaimGate. A nil pool yields a gate whose
// methods return ErrPoolNotConfigured — call sites can no-op around it but
// MUST treat that as an unrecoverable misconfiguration in production.
func NewClaimGate(pool *pgxpool.Pool) *DefaultClaimGate {
	if pool == nil {
		return &DefaultClaimGate{pool: nil}
	}
	return &DefaultClaimGate{
		pool:        pool,
		q:           dbbilling.New(pool),
		LeaseWindow: 90 * time.Second,
	}
}

// Reserve runs the full Tx1 protocol: SELECT FOR UPDATE the candidate row,
// detect idempotent replay vs fingerprint conflict, INSERT a new reserving
// claim if neither, COMMIT.
//
// The returned *ReserveResult carries:
//   - IdempotencyHit=true    → caller skips the upstream call and replays cache
//   - FingerprintConflict=true → caller returns 409 to client; no charge
//   - ClaimID > 0 + neither flag → caller proceeds to Pool acquire + upstream
func (g *DefaultClaimGate) Reserve(ctx context.Context, req ReserveRequest) (*ReserveResult, error) {
	if g == nil || g.pool == nil {
		return nil, ErrPoolNotConfigured
	}
	idempotencyKey := ComputeIdempotencyFingerprint(req)

	tx, err := g.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return nil, fmt.Errorf("billing: begin Tx1: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	qtx := g.q.WithTx(tx)

	// Step 1: idempotent lookup with row lock.
	existing, err := qtx.GetClaimByIdempotency(ctx, dbbilling.GetClaimByIdempotencyParams{
		TenantID:       req.TenantID,
		APIKeyID:       req.APIKeyID,
		IdempotencyKey: idempotencyKey,
	})
	if err == nil {
		// Existing claim with matching fingerprint — replay paths per spec §Tx1 step 3.
		switch existing.Status {
		case "committed":
			if err := tx.Commit(ctx); err != nil {
				return nil, fmt.Errorf("billing: commit idempotent-hit Tx1: %w", err)
			}
			return &ReserveResult{ClaimID: existing.ID, IdempotencyHit: true}, nil
		case "reserving":
			return nil, ErrClaimRace
		case "aborted":
			// Aborted predecessor — re-attempt by resurrecting the row.
			// Inserting a new row under the same (tenant, api_key, idempotency_key)
			// would violate uq_claims_idempotency. ReReserveAbortedClaim flips
			// status back to 'reserving' and bumps attempt_seq.
			leaseExpiresAt := time.Now().UTC().Add(g.leaseWindow())
			row, err := qtx.ReReserveAbortedClaim(ctx, dbbilling.ReReserveAbortedClaimParams{
				ID:             existing.ID,
				LeaseExpiresAt: pgtype.Timestamptz{Time: leaseExpiresAt, Valid: true},
				PredictedCost:  req.PredictedCost,
				TenantID:       req.TenantID,
			})
			if err != nil {
				return nil, fmt.Errorf("billing: re-reserve aborted claim: %w", err)
			}
			if err := tx.Commit(ctx); err != nil {
				return nil, fmt.Errorf("billing: commit re-reserve Tx1: %w", err)
			}
			return &ReserveResult{ClaimID: row.ID}, nil
		}
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("billing: claim idempotency lookup: %w", err)
	}

	// Step 2: replay-attack check — same logical_request_id with a different fingerprint.
	if req.LogicalRequestID != "" {
		rows, err := qtx.GetClaimFingerprintByLogicalRequestID(ctx, dbbilling.GetClaimFingerprintByLogicalRequestIDParams{
			TenantID:         req.TenantID,
			APIKeyID:         req.APIKeyID,
			LogicalRequestID: req.LogicalRequestID,
		})
		if err != nil {
			return nil, fmt.Errorf("billing: claim fingerprint scan: %w", err)
		}
		for _, r := range rows {
			if r.RequestFingerprint != idempotencyKey {
				return &ReserveResult{FingerprintConflict: true}, ErrFingerprintConflict
			}
		}
	}

	// Step 3: insert a new reserving claim.
	leaseExpiresAt := time.Now().UTC().Add(g.leaseWindow())
	inserted, err := qtx.InsertClaim(ctx, dbbilling.InsertClaimParams{
		TenantID:             req.TenantID,
		IdempotencyKey:       idempotencyKey,
		RequestFingerprint:   idempotencyKey,
		APIKeyID:             req.APIKeyID,
		UserID:               req.UserID,
		LogicalRequestID:     req.LogicalRequestID,
		EndpointFamily:       req.EndpointFamily,
		RequestedModel:       req.RequestedModel,
		PoolingGroupID:       nullableInt64(req.PoolingGroupID),
		BillingPolicyVersion: req.BillingPolicyVersion,
		RequestClass:         req.RequestClass,
		PredictedCost:        req.PredictedCost,
		CurrencyCode:         "USD",
		LeaseExpiresAt:       pgtype.Timestamptz{Time: leaseExpiresAt, Valid: true},
	})
	if err != nil {
		// Unique violation = idempotency race (concurrent inserter). Treat as race.
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return nil, ErrClaimRace
		}
		return nil, fmt.Errorf("billing: insert claim: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("billing: commit Tx1: %w", err)
	}
	return &ReserveResult{ClaimID: inserted.ID}, nil
}

// ComputeIdempotencyFingerprint hashes the 9 PERSISTED fields per spec §Tx1
// step 1. The IdempotencyKeyClientHeader in ReserveRequest is intentionally
// EXCLUDED from this hash — see docs/process/plans/2026-04-29-integration-sprint-plan.md.
//
// PoolingGroupID is also EXCLUDED: the pool group is now derived by Registry/Router from
// mutable admin state, not from the client request. If an admin reroutes
// a model→pool binding mid-flight, a legitimate retry with the same
// Idempotency-Key would otherwise hash to a new fingerprint and surface
// as idempotency_conflict. Excluding it makes idempotency depend only
// on client-controlled inputs (tenant + key + logical id + payload +
// model alias + endpoint + billing policy + request class).
func ComputeIdempotencyFingerprint(r ReserveRequest) string {
	h := sha256.New()
	for _, field := range []string{
		strconv.FormatInt(r.TenantID, 10),
		strconv.FormatInt(r.APIKeyID, 10),
		r.LogicalRequestID,
		r.EndpointFamily,
		r.NormalizedPayloadHash,
		r.RequestedModel,
		r.BillingPolicyVersion,
		r.RequestClass,
	} {
		h.Write([]byte(field))
		h.Write([]byte{0x1F}) // unit separator: prevents adjacent-field collision
	}
	return hex.EncodeToString(h.Sum(nil))
}

func (g *DefaultClaimGate) leaseWindow() time.Duration {
	if g.LeaseWindow > 0 {
		return g.LeaseWindow
	}
	return 90 * time.Second
}

func nullableInt64(v int64) *int64 {
	if v == 0 {
		return nil
	}
	return &v
}

// Compile-time interface check — DefaultClaimGate must satisfy ClaimGate.
var _ ClaimGate = (*DefaultClaimGate)(nil)

// Suppress unused-import warning for decimal until Settler is implemented.
var _ = decimal.Zero
