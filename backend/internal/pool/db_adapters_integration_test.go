//go:build integration_pg

// Phase C.2 adapters tested vs real PostgreSQL: DBClaimGate,
// DBSlotManager, DBAccountSource. The contract these enforce is the
// production-time bridge from the selector to billing_ledger_claims +
// pool_slot_acquisitions + provider_accounts.

package pool

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BloomingProsperity/HUAKAI/internal/db"
	dbbilling "github.com/BloomingProsperity/HUAKAI/internal/db/billing"
)

func openIntegrationPool(t *testing.T, ctx context.Context) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("HUAKAI_DATABASE_URL")
	if dsn == "" {
		t.Skip("HUAKAI_DATABASE_URL not set; skipping integration test")
	}
	p, err := db.Open(ctx, db.PoolConfig{DSN: dsn})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(p.Close)
	return p
}

type adapterSeed struct {
	tenantID          int64
	apiKeyID          int64
	userID            int64
	providerID        int64
	poolGroupID       int64
	channelID         int64
	providerAccountID int64
	claimID           int64
}

// seedAdapterGraph mirrors settler_integration_test seed but lives in the
// pool package to stay below the test-cycle limit.
func seedAdapterGraph(t *testing.T, ctx context.Context, pool *pgxpool.Pool, suffix string) *adapterSeed {
	t.Helper()
	seed := &adapterSeed{}
	unique := suffix + "-" + uuid.NewString()

	if err := pool.QueryRow(ctx,
		`INSERT INTO tenants (name) VALUES ($1) RETURNING id`,
		"adapter-tenant-"+unique,
	).Scan(&seed.tenantID); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	// Slice 2: real users + api_keys rows replace the
	// previous synthetic-id pattern. Migration 0009 added composite FKs
	// from billing_ledger_claims/usage_records/billing_ledger_archive
	// (tenant_id, api_key_id) -> api_keys, so claim seeds must reference
	// real api_keys rows.
	if err := pool.QueryRow(ctx,
		`INSERT INTO users (tenant_id, display_name) VALUES ($1, $2) RETURNING id`,
		seed.tenantID, "adapter-user-"+unique,
	).Scan(&seed.userID); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO api_keys (tenant_id, user_id, name, key_hash, key_prefix, status)
		 VALUES ($1, $2, $3, $4, $5, 'active') RETURNING id`,
		seed.tenantID, seed.userID, "adapter-key-"+unique,
		"$2a$10$placeholder-not-resolved-by-pool-tests",
		"hk_test_adp"+unique[:5],
	).Scan(&seed.apiKeyID); err != nil {
		t.Fatalf("seed api_key: %v", err)
	}

	t.Cleanup(func() {
		ctx := context.Background()
		// FK chain after migration 0009: pool_slot_acquisitions -> claims;
		//                      claims/usage/archive -> api_keys/users;
		//                      api_keys -> users.
		_, _ = pool.Exec(ctx, `DELETE FROM usage_records WHERE tenant_id=$1`, seed.tenantID)
		_, _ = pool.Exec(ctx, `DELETE FROM billing_events WHERE tenant_id=$1`, seed.tenantID)
		_, _ = pool.Exec(ctx, `DELETE FROM pool_slot_acquisitions WHERE tenant_id=$1`, seed.tenantID)
		_, _ = pool.Exec(ctx, `DELETE FROM billing_ledger_claims WHERE tenant_id=$1`, seed.tenantID)
		_, _ = pool.Exec(ctx, `DELETE FROM billing_ledger_archive WHERE tenant_id=$1`, seed.tenantID)
		_, _ = pool.Exec(ctx, `DELETE FROM provider_accounts WHERE tenant_id=$1`, seed.tenantID)
		_, _ = pool.Exec(ctx, `DELETE FROM channels WHERE tenant_id=$1`, seed.tenantID)
		_, _ = pool.Exec(ctx, `DELETE FROM pool_groups WHERE tenant_id=$1`, seed.tenantID)
		_, _ = pool.Exec(ctx, `DELETE FROM providers WHERE tenant_id=$1`, seed.tenantID)
		_, _ = pool.Exec(ctx, `DELETE FROM api_keys WHERE tenant_id=$1`, seed.tenantID)
		_, _ = pool.Exec(ctx, `DELETE FROM users WHERE tenant_id=$1`, seed.tenantID)
		_, _ = pool.Exec(ctx, `DELETE FROM tenants WHERE id=$1`, seed.tenantID)
	})

	if err := pool.QueryRow(ctx,
		`INSERT INTO providers (tenant_id, code, display_name, upstream_protocol)
		 VALUES ($1, $2, $3, 'openai_chat') RETURNING id`,
		seed.tenantID, "p-"+unique, "Provider "+unique,
	).Scan(&seed.providerID); err != nil {
		t.Fatalf("seed provider: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO pool_groups (tenant_id, name) VALUES ($1, $2) RETURNING id`,
		seed.tenantID, "pg-"+unique,
	).Scan(&seed.poolGroupID); err != nil {
		t.Fatalf("seed pool group: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO channels (tenant_id, pool_group_id, name) VALUES ($1, $2, $3) RETURNING id`,
		seed.tenantID, seed.poolGroupID, "ch-"+unique,
	).Scan(&seed.channelID); err != nil {
		t.Fatalf("seed channel: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO provider_accounts (
			tenant_id, provider_id, channel_id, name, account_type,
			cap_concurrency, in_flight_count
		) VALUES ($1, $2, $3, $4, 'api_key', 2, 0) RETURNING id`,
		seed.tenantID, seed.providerID, seed.channelID, "acct-"+unique,
	).Scan(&seed.providerAccountID); err != nil {
		t.Fatalf("seed provider account: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO billing_ledger_claims (
			tenant_id, idempotency_key, request_fingerprint, api_key_id, user_id,
			logical_request_id, endpoint_family, requested_model, pooling_group_id,
			billing_policy_version, request_class, attempt_seq, predicted_cost,
			currency_code, lease_expires_at
		) VALUES (
			$1, $2, $3, $4, $5,
			$6, 'chat', 'gpt-4.1-mini', $7,
			'1.0', 'standard', 1, 0.01,
			'USD', NOW() + interval '90 seconds'
		) RETURNING id`,
		seed.tenantID, "idem-"+unique, "fp-"+unique, seed.apiKeyID, seed.userID,
		"lr-"+unique, seed.poolGroupID,
	).Scan(&seed.claimID); err != nil {
		t.Fatalf("seed claim: %v", err)
	}
	return seed
}

func TestDBClaimGate_WriteAcquisition_Success(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool := openIntegrationPool(t, ctx)
	seed := seedAdapterGraph(t, ctx, pool, "cg-success")

	gate := NewDBClaimGate(dbbilling.New(pool))
	token := uuid.New()
	if err := gate.WriteAcquisition(ctx, seed.tenantID, seed.claimID, seed.providerAccountID, token); err != nil {
		t.Fatalf("WriteAcquisition: %v", err)
	}

	var paID int64
	var dbToken uuid.UUID
	if err := pool.QueryRow(ctx,
		`SELECT provider_account_id, acquisition_token FROM billing_ledger_claims WHERE id=$1`,
		seed.claimID,
	).Scan(&paID, &dbToken); err != nil {
		t.Fatalf("readback: %v", err)
	}
	if paID != seed.providerAccountID || dbToken != token {
		t.Fatalf("writeback mismatch: paID=%d expected=%d, token=%s expected=%s",
			paID, seed.providerAccountID, dbToken, token)
	}
}

func TestDBClaimGate_RejectsCrossTenant(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool := openIntegrationPool(t, ctx)
	seed := seedAdapterGraph(t, ctx, pool, "cg-xtenant")

	gate := NewDBClaimGate(dbbilling.New(pool))
	wrongTenant := seed.tenantID + 99999
	err := gate.WriteAcquisition(ctx, wrongTenant, seed.claimID, seed.providerAccountID, uuid.New())
	if !errors.Is(err, ErrClaimRace) {
		t.Fatalf("cross-tenant write must return ErrClaimRace; got %v", err)
	}
}

func TestDBClaimGate_RejectsAlreadyCommitted(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool := openIntegrationPool(t, ctx)
	seed := seedAdapterGraph(t, ctx, pool, "cg-already-committed")

	if _, err := pool.Exec(ctx,
		`UPDATE billing_ledger_claims SET status='committed' WHERE id=$1`,
		seed.claimID,
	); err != nil {
		t.Fatalf("flip claim status: %v", err)
	}

	gate := NewDBClaimGate(dbbilling.New(pool))
	err := gate.WriteAcquisition(ctx, seed.tenantID, seed.claimID, seed.providerAccountID, uuid.New())
	if !errors.Is(err, ErrClaimRace) {
		t.Fatalf("non-reserving claim must return ErrClaimRace; got %v", err)
	}
}

func TestDBSlotManager_Acquire_HappyPath(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pgPool := openIntegrationPool(t, ctx)
	seed := seedAdapterGraph(t, ctx, pgPool, "slot-happy")

	mgr := NewDBSlotManager(pgPool)
	acct := &AccountSnapshot{ID: seed.providerAccountID, TenantID: seed.tenantID, MaxConcurrency: 2}
	res, err := mgr.Acquire(ctx, acct, SelectionRequest{
		TenantID: seed.tenantID, ClaimID: seed.claimID, AttemptSeq: 1,
	})
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if res == nil || res.AcquisitionToken == uuid.Nil {
		t.Fatalf("acquire returned no token: %+v", res)
	}

	var inFlight int32
	if err := pgPool.QueryRow(ctx,
		`SELECT in_flight_count FROM provider_accounts WHERE id=$1`, seed.providerAccountID,
	).Scan(&inFlight); err != nil {
		t.Fatalf("read in_flight: %v", err)
	}
	if inFlight != 1 {
		t.Fatalf("in_flight expected 1; got %d", inFlight)
	}
	var slotCount int
	if err := pgPool.QueryRow(ctx,
		`SELECT count(*) FROM pool_slot_acquisitions WHERE acquisition_token=$1 AND status='acquired'`,
		res.AcquisitionToken,
	).Scan(&slotCount); err != nil {
		t.Fatalf("count slot: %v", err)
	}
	if slotCount != 1 {
		t.Fatalf("expected 1 acquired slot row; got %d", slotCount)
	}
}

func TestDBSlotManager_Acquire_AtCapReturnsErrNoSlot(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pgPool := openIntegrationPool(t, ctx)
	seed := seedAdapterGraph(t, ctx, pgPool, "slot-at-cap")

	if _, err := pgPool.Exec(ctx,
		`UPDATE provider_accounts SET in_flight_count=cap_concurrency WHERE id=$1`,
		seed.providerAccountID,
	); err != nil {
		t.Fatalf("force at-cap: %v", err)
	}

	mgr := NewDBSlotManager(pgPool)
	acct := &AccountSnapshot{ID: seed.providerAccountID, TenantID: seed.tenantID, MaxConcurrency: 2}
	_, err := mgr.Acquire(ctx, acct, SelectionRequest{
		TenantID: seed.tenantID, ClaimID: seed.claimID, AttemptSeq: 1,
	})
	if !errors.Is(err, ErrNoSlotAvailable) {
		t.Fatalf("expected ErrNoSlotAvailable; got %v", err)
	}
}

func TestDBSlotManager_Release_Idempotent(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pgPool := openIntegrationPool(t, ctx)
	seed := seedAdapterGraph(t, ctx, pgPool, "slot-release-idem")

	mgr := NewDBSlotManager(pgPool)
	acct := &AccountSnapshot{ID: seed.providerAccountID, TenantID: seed.tenantID, MaxConcurrency: 2}
	res, err := mgr.Acquire(ctx, acct, SelectionRequest{
		TenantID: seed.tenantID, ClaimID: seed.claimID, AttemptSeq: 1,
	})
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}

	if err := res.Release(ctx); err != nil {
		t.Fatalf("first Release: %v", err)
	}
	if err := res.Release(ctx); err != nil {
		t.Fatalf("second (idempotent) Release: %v", err)
	}

	var inFlight int32
	if err := pgPool.QueryRow(ctx,
		`SELECT in_flight_count FROM provider_accounts WHERE id=$1`, seed.providerAccountID,
	).Scan(&inFlight); err != nil {
		t.Fatalf("read in_flight: %v", err)
	}
	if inFlight != 0 {
		t.Fatalf("in_flight must decrement once; got %d after two releases", inFlight)
	}
}

func TestDBAccountSource_ListByPoolGroup(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pgPool := openIntegrationPool(t, ctx)
	seed := seedAdapterGraph(t, ctx, pgPool, "src-pg")

	// Add a second account in the same pool_group via a second channel.
	var secondChannelID, secondAccountID int64
	suffix := uuid.NewString()
	if err := pgPool.QueryRow(ctx,
		`INSERT INTO channels (tenant_id, pool_group_id, name) VALUES ($1, $2, $3) RETURNING id`,
		seed.tenantID, seed.poolGroupID, "ch2-"+suffix,
	).Scan(&secondChannelID); err != nil {
		t.Fatalf("seed second channel: %v", err)
	}
	if err := pgPool.QueryRow(ctx,
		`INSERT INTO provider_accounts (
			tenant_id, provider_id, channel_id, name, account_type,
			cap_concurrency, in_flight_count, priority
		) VALUES ($1, $2, $3, $4, 'api_key', 4, 1, 2) RETURNING id`,
		seed.tenantID, seed.providerID, secondChannelID, "acct2-"+suffix,
	).Scan(&secondAccountID); err != nil {
		t.Fatalf("seed second account: %v", err)
	}

	src := NewDBAccountSource(dbbilling.New(pgPool))
	accounts, err := src.ListAccounts(ctx, SelectionRequest{
		TenantID:    seed.tenantID,
		PoolGroupID: seed.poolGroupID,
	})
	if err != nil {
		t.Fatalf("ListAccounts: %v", err)
	}
	if len(accounts) != 2 {
		t.Fatalf("expected 2 accounts in pool group; got %d", len(accounts))
	}

	got := map[int64]*AccountSnapshot{}
	for _, a := range accounts {
		got[a.ID] = a
	}
	if got[secondAccountID] == nil || got[secondAccountID].MaxConcurrency != 4 {
		t.Fatalf("second account snapshot missing or MaxConcurrency != 4: %+v", got[secondAccountID])
	}
	if got[secondAccountID].LoadRate <= 0 || got[secondAccountID].LoadRate >= 1 {
		t.Fatalf("expected LoadRate in (0,1) for in_flight=1/cap=4; got %v", got[secondAccountID].LoadRate)
	}
}

func TestDBAccountSource_ListByPoolGroupSkipsDisabledOrDeletedChannels(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pgPool := openIntegrationPool(t, ctx)
	seed := seedAdapterGraph(t, ctx, pgPool, "src-channel-lifecycle")

	suffix := uuid.NewString()
	for _, tc := range []struct {
		name       string
		channelSQL string
	}{
		{
			name:       "disabled",
			channelSQL: `INSERT INTO channels (tenant_id, pool_group_id, name, enabled) VALUES ($1, $2, $3, false) RETURNING id`,
		},
		{
			name:       "deleted",
			channelSQL: `INSERT INTO channels (tenant_id, pool_group_id, name, deleted_at) VALUES ($1, $2, $3, NOW()) RETURNING id`,
		},
	} {
		var channelID int64
		if err := pgPool.QueryRow(ctx, tc.channelSQL,
			seed.tenantID, seed.poolGroupID, "ch-"+tc.name+"-"+suffix,
		).Scan(&channelID); err != nil {
			t.Fatalf("seed %s channel: %v", tc.name, err)
		}
		if _, err := pgPool.Exec(ctx,
			`INSERT INTO provider_accounts (
				tenant_id, provider_id, channel_id, name, account_type,
				cap_concurrency, in_flight_count, priority
			) VALUES ($1, $2, $3, $4, 'api_key', 4, 0, 1)`,
			seed.tenantID, seed.providerID, channelID, "acct-"+tc.name+"-"+suffix,
		); err != nil {
			t.Fatalf("seed %s account: %v", tc.name, err)
		}
	}

	src := NewDBAccountSource(dbbilling.New(pgPool))
	accounts, err := src.ListAccounts(ctx, SelectionRequest{
		TenantID:    seed.tenantID,
		PoolGroupID: seed.poolGroupID,
	})
	if err != nil {
		t.Fatalf("ListAccounts: %v", err)
	}
	if len(accounts) != 1 || accounts[0].ID != seed.providerAccountID {
		t.Fatalf("accounts=%+v; want only account on enabled, live channel %d", accounts, seed.providerAccountID)
	}
}
