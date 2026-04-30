// Phase C.2 production adapter: DB-backed pool.ClaimGate.
//
// Bridges the selector's claim-writeback seam to the sqlc-generated
// WriteAcquisitionToken query (Pattern B per F-POOL-001 §6 + F-OBS-001 §Tx1).
// Tenant-scoped by design — Phase B.5 P1 fix on Settler.Abort taught us that
// any UPDATE keyed only on claim_id is a multi-tenant footgun.

package pool

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/BloomingProsperity/HUAKAI/internal/db"
)

// DBClaimGate writes the (provider_account_id, acquisition_token) pair onto
// the reserving claim row identified by (id, tenant_id). It returns
// ErrClaimRace if the WHERE clause matched zero rows — meaning the claim is
// no longer in 'reserving' state, was already written by a concurrent
// selector, or the tenant scope rejected the write.
type DBClaimGate struct {
	q *db.Queries
}

// NewDBClaimGate constructs the adapter from a sqlc.Queries handle.
func NewDBClaimGate(q *db.Queries) *DBClaimGate {
	return &DBClaimGate{q: q}
}

// WriteAcquisition implements pool.ClaimGate.
func (g *DBClaimGate) WriteAcquisition(ctx context.Context, tenantID, claimID, accountID int64, token uuid.UUID) error {
	if g == nil || g.q == nil {
		return errors.New("pool: DBClaimGate not configured")
	}
	rows, err := g.q.WriteAcquisitionToken(ctx, db.WriteAcquisitionTokenParams{
		ID:                claimID,
		ProviderAccountID: &accountID,
		AcquisitionToken:  token,
		TenantID:          tenantID,
	})
	if err != nil {
		return fmt.Errorf("pool: write acquisition token: %w", err)
	}
	if rows == 0 {
		return ErrClaimRace
	}
	return nil
}

var _ ClaimGate = (*DBClaimGate)(nil)
