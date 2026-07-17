//go:build integration_pg

package quota

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"
)

// AT-CD1-006:真实 Serializable 旧快照在目标 reservation 被并发更新后，
// SELECT FOR UPDATE 必须把 40001 交给外层，以新事务重读并复用 reservation。
func TestServiceReserve_ATCD1006_RealReservationReadSerializationConflict(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	pool := openQuotaIntegrationPool(t, ctx)
	f := newQuotaFixture(t, ctx, pool)
	store := NewPostgresStore(pool)
	service := NewService(store)

	now := time.Now().UTC().Truncate(time.Second)
	req := ReserveRequest{
		TenantID:           f.tenantID,
		ClaimID:            f.seedClaim("at-cd1-006"),
		RequestFingerprint: "at-cd1-006",
		Scopes:             f.reserveScopes(),
		PredictedCost:      decimal.RequireFromString("0.01"),
		LeaseExpiresAt:     now.Add(5 * time.Minute),
		At:                 now,
	}
	seed, err := service.Reserve(ctx, req)
	if err != nil || !seed.Allowed || seed.Decision.Code != decisionCodeAllowed {
		t.Fatalf("seed Reserve err=%v result=%+v; want fresh allow", err, seed)
	}

	originalBeginTx := store.beginTx
	var begins atomic.Int32
	store.beginTx = func(ctx context.Context, opts pgx.TxOptions) (pgx.Tx, error) {
		tx, err := originalBeginTx(ctx, opts)
		if err != nil {
			return nil, err
		}
		if begins.Add(1) != 1 {
			return tx, nil
		}

		var snapshotLease time.Time
		if err := tx.QueryRow(ctx,
			`SELECT lease_expires_at FROM quota_reservations WHERE tenant_id=$1 AND id=$2`,
			f.tenantID, seed.Reservation.ID,
		).Scan(&snapshotLease); err != nil {
			_ = tx.Rollback(ctx)
			return nil, fmt.Errorf("establish serializable snapshot: %w", err)
		}

		updateDone := make(chan error, 1)
		go func() {
			tag, updateErr := pool.Exec(ctx,
				`UPDATE quota_reservations
				 SET lease_expires_at=lease_expires_at + interval '1 second'
				 WHERE tenant_id=$1 AND id=$2`,
				f.tenantID, seed.Reservation.ID,
			)
			if updateErr == nil && tag.RowsAffected() != 1 {
				updateErr = fmt.Errorf("concurrent update affected %d rows", tag.RowsAffected())
			}
			updateDone <- updateErr
		}()
		select {
		case updateErr := <-updateDone:
			if updateErr != nil {
				_ = tx.Rollback(ctx)
				return nil, fmt.Errorf("concurrent reservation update: %w", updateErr)
			}
		case <-ctx.Done():
			_ = tx.Rollback(context.Background())
			return nil, ctx.Err()
		}
		return tx, nil
	}

	result, err := service.Reserve(ctx, req)
	if err != nil {
		t.Fatalf("Reserve after real 40001 err=%v; want reused allow", err)
	}
	if !result.Allowed || !result.IdempotencyHit || result.Decision.Code != decisionCodeReused || result.Reservation.ID != seed.Reservation.ID {
		t.Fatalf("result=%+v; want reused reservation %d", result, seed.Reservation.ID)
	}
	if got := begins.Load(); got != 2 {
		t.Fatalf("BeginTx calls=%d; want old-snapshot transaction plus one retry", got)
	}
	if got := f.auditCount("reserve_allowed"); got != 1 {
		t.Fatalf("reserve_allowed audit count=%d; want seed only", got)
	}
	if got := f.auditDecisionCount(decisionCodeFailClosed); got != 0 {
		t.Fatalf("quota_fail_closed audit count=%d; want 0", got)
	}
}
