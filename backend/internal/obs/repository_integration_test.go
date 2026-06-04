//go:build integration_pg

// Reader integration tests run against real PostgreSQL.
// Validates read-only — Reader writes nothing by construction,
// and no credential fields surface by SQL coverage. Tenant
// scope is enforced by every query's WHERE clause.

package obs

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"

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

type readerSeed struct {
	tenantID         int64
	otherTenantID    int64 // for cross-tenant assertion
	apiKeyID         int64
	userID           int64
	providerID       int64
	poolGroupID      int64
	channelID        int64
	providerAcctID   int64
	committedClaimID int64
	abortedClaimID   int64
}

func seedReaderGraph(t *testing.T, ctx context.Context, pool *pgxpool.Pool) *readerSeed {
	t.Helper()
	unique := uuid.NewString()
	s := &readerSeed{}

	// Primary tenant.
	if err := pool.QueryRow(ctx,
		`INSERT INTO tenants (name) VALUES ($1) RETURNING id`,
		"obs-tenant-"+unique,
	).Scan(&s.tenantID); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	// Adversary tenant for cross-tenant probe.
	if err := pool.QueryRow(ctx,
		`INSERT INTO tenants (name) VALUES ($1) RETURNING id`,
		"obs-other-"+unique,
	).Scan(&s.otherTenantID); err != nil {
		t.Fatalf("seed other tenant: %v", err)
	}
	// Slice 2: real users + api_keys rows replace the
	// previous synthetic-id pattern (`s.apiKeyID = s.tenantID*100 + 1`).
	// Migration 0009 added composite FKs from billing_ledger_claims +
	// usage_records (tenant_id, api_key_id|user_id) -> api_keys|users.
	if err := pool.QueryRow(ctx,
		`INSERT INTO users (tenant_id, display_name) VALUES ($1, $2) RETURNING id`,
		s.tenantID, "obs-user-"+unique,
	).Scan(&s.userID); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO api_keys (tenant_id, user_id, name, key_hash, key_prefix, status)
		 VALUES ($1, $2, $3, $4, $5, 'active') RETURNING id`,
		s.tenantID, s.userID, "obs-key-"+unique,
		"$2a$10$placeholder-not-resolved-by-obs-tests",
		"hk_test_obs"+unique[:5],
	).Scan(&s.apiKeyID); err != nil {
		t.Fatalf("seed api_key: %v", err)
	}

	t.Cleanup(func() {
		c := context.Background()
		for _, tid := range []int64{s.tenantID, s.otherTenantID} {
			// FK chain after migration 0009: claims/usage/archive -> api_keys -> users -> tenants.
			_, _ = pool.Exec(c, `DELETE FROM usage_records WHERE tenant_id=$1`, tid)
			_, _ = pool.Exec(c, `DELETE FROM billing_events WHERE tenant_id=$1`, tid)
			_, _ = pool.Exec(c, `DELETE FROM pool_slot_acquisitions WHERE tenant_id=$1`, tid)
			_, _ = pool.Exec(c, `DELETE FROM billing_ledger_claims WHERE tenant_id=$1`, tid)
			_, _ = pool.Exec(c, `DELETE FROM billing_ledger_archive WHERE tenant_id=$1`, tid)
			_, _ = pool.Exec(c, `DELETE FROM provider_accounts WHERE tenant_id=$1`, tid)
			_, _ = pool.Exec(c, `DELETE FROM channels WHERE tenant_id=$1`, tid)
			_, _ = pool.Exec(c, `DELETE FROM pool_groups WHERE tenant_id=$1`, tid)
			_, _ = pool.Exec(c, `DELETE FROM providers WHERE tenant_id=$1`, tid)
			_, _ = pool.Exec(c, `DELETE FROM api_keys WHERE tenant_id=$1`, tid)
			_, _ = pool.Exec(c, `DELETE FROM users WHERE tenant_id=$1`, tid)
			_, _ = pool.Exec(c, `DELETE FROM tenants WHERE id=$1`, tid)
		}
	})

	// Pool/channel/account graph for primary tenant.
	if err := pool.QueryRow(ctx,
		`INSERT INTO providers (tenant_id, code, display_name, upstream_protocol)
		 VALUES ($1, $2, $3, 'openai_chat') RETURNING id`,
		s.tenantID, "p-"+unique, "P "+unique,
	).Scan(&s.providerID); err != nil {
		t.Fatalf("seed provider: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO pool_groups (tenant_id, name) VALUES ($1, $2) RETURNING id`,
		s.tenantID, "pg-"+unique,
	).Scan(&s.poolGroupID); err != nil {
		t.Fatalf("seed pool group: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO channels (tenant_id, pool_group_id, name) VALUES ($1, $2, $3) RETURNING id`,
		s.tenantID, s.poolGroupID, "ch-"+unique,
	).Scan(&s.channelID); err != nil {
		t.Fatalf("seed channel: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO provider_accounts (tenant_id, provider_id, channel_id, name, account_type, in_flight_count)
		 VALUES ($1, $2, $3, $4, 'api_key', 0) RETURNING id`,
		s.tenantID, s.providerID, s.channelID, "acct-"+unique,
	).Scan(&s.providerAcctID); err != nil {
		t.Fatalf("seed account: %v", err)
	}

	// One committed claim with usage_record + billing_event (claim_committed).
	if err := pool.QueryRow(ctx,
		`INSERT INTO billing_ledger_claims (
			tenant_id, idempotency_key, request_fingerprint, api_key_id, user_id,
			logical_request_id, endpoint_family, requested_model, pooling_group_id,
			billing_policy_version, request_class, provider_account_id, acquisition_token,
			attempt_seq, predicted_cost, actual_cost, currency_code, status, settled_at, lease_expires_at
		) VALUES (
			$1, $2, $3, $4, $5,
			$6, 'chat', 'gpt-4.1-mini', $7,
			'1.0', 'standard', $8, $9,
			1, 0.01, 0.02, 'USD', 'committed', NOW(), NOW() + interval '90 seconds'
		) RETURNING id`,
		s.tenantID, "idem-c-"+unique, "fp-c-"+unique, s.apiKeyID, s.userID,
		"lr-c-"+unique, s.poolGroupID, s.providerAcctID, uuid.New(),
	).Scan(&s.committedClaimID); err != nil {
		t.Fatalf("seed committed claim: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO usage_records (
			tenant_id, claim_id, api_key_id, user_id, provider_account_id,
			acquisition_token, attempt_seq,
			tokens_input, tokens_output,
			actual_cost, end_class, usage_source, requested_at, requested_model
		) VALUES (
			$1, $2, $3, $4, $5,
			$6, 1,
			10, 20,
			0.02, 'non_streaming', 'reported', NOW(), 'gpt-4.1-mini'
		)`,
		s.tenantID, s.committedClaimID, s.apiKeyID, s.userID, s.providerAcctID, uuid.New(),
	); err != nil {
		t.Fatalf("seed usage_record: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO billing_events (
			tenant_id, claim_id, event_type, actual_cost, actual_cost_signed, fingerprint
		) VALUES ($1, $2, 'claim_committed', 0.02, 0.02, $3)`,
		s.tenantID, s.committedClaimID, "fp-c-"+unique,
	); err != nil {
		t.Fatalf("seed billing_event: %v", err)
	}

	// One aborted claim for status counts.
	if err := pool.QueryRow(ctx,
		`INSERT INTO billing_ledger_claims (
			tenant_id, idempotency_key, request_fingerprint, api_key_id, user_id,
			logical_request_id, endpoint_family, requested_model, pooling_group_id,
			billing_policy_version, request_class, attempt_seq, predicted_cost,
			currency_code, status, aborted_reason, settled_at, lease_expires_at
		) VALUES (
			$1, $2, $3, $4, $5,
			$6, 'chat', 'gpt-4.1-mini', $7,
			'1.0', 'standard', 1, 0.01,
			'USD', 'aborted', 'test_abort', NOW(), NOW() + interval '90 seconds'
		) RETURNING id`,
		s.tenantID, "idem-a-"+unique, "fp-a-"+unique, s.apiKeyID, s.userID,
		"lr-a-"+unique, s.poolGroupID,
	).Scan(&s.abortedClaimID); err != nil {
		t.Fatalf("seed aborted claim: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO billing_events (
			tenant_id, claim_id, event_type, actual_cost, actual_cost_signed, fingerprint
		) VALUES ($1, $2, 'claim_aborted', 0, 0, $3)`,
		s.tenantID, s.abortedClaimID, "fp-a-"+unique,
	); err != nil {
		t.Fatalf("seed abort billing_event: %v", err)
	}

	return s
}

func TestReader_ListUsage_TenantScopedOnly(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool := openIntegrationPool(t, ctx)
	seed := seedReaderGraph(t, ctx, pool)

	r := NewPgxReader(dbbilling.New(pool))

	rows, err := r.ListUsage(ctx, seed.tenantID, Page{Limit: 100})
	if err != nil {
		t.Fatalf("ListUsage(tenant): %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("primary tenant should have 1 usage_record; got %d", len(rows))
	}
	if rows[0].TenantID != seed.tenantID {
		t.Fatalf("usage row tenant mismatch: %d vs seed %d", rows[0].TenantID, seed.tenantID)
	}

	// Cross-tenant probe — adversary should see 0 rows even though
	// the row exists under primary tenant.
	otherRows, err := r.ListUsage(ctx, seed.otherTenantID, Page{Limit: 100})
	if err != nil {
		t.Fatalf("ListUsage(other): %v", err)
	}
	if len(otherRows) != 0 {
		t.Fatalf("cross-tenant probe leaked %d rows; expected 0", len(otherRows))
	}
}

func TestReader_GetClaim_HappyPath(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool := openIntegrationPool(t, ctx)
	seed := seedReaderGraph(t, ctx, pool)

	r := NewPgxReader(dbbilling.New(pool))

	c, err := r.GetClaim(ctx, seed.tenantID, seed.committedClaimID)
	if err != nil {
		t.Fatalf("GetClaim: %v", err)
	}
	if c.Status != "committed" {
		t.Fatalf("expected committed; got %q", c.Status)
	}
	if !c.ActualCost.Equal(decimal.RequireFromString("0.02000000")) {
		t.Fatalf("ActualCost mismatch; got %s", c.ActualCost)
	}
}

func TestReader_GetClaim_RejectsCrossTenant(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool := openIntegrationPool(t, ctx)
	seed := seedReaderGraph(t, ctx, pool)

	r := NewPgxReader(dbbilling.New(pool))

	_, err := r.GetClaim(ctx, seed.otherTenantID, seed.committedClaimID)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-tenant read must collapse to ErrNotFound; got %v", err)
	}
}

func TestReader_ListBillingEvents_FilterByEventType(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool := openIntegrationPool(t, ctx)
	seed := seedReaderGraph(t, ctx, pool)

	r := NewPgxReader(dbbilling.New(pool))

	all, err := r.ListBillingEvents(ctx, seed.tenantID, "", Page{Limit: 100})
	if err != nil {
		t.Fatalf("ListBillingEvents(all): %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("expected 2 events (committed + aborted); got %d", len(all))
	}

	committed, err := r.ListBillingEvents(ctx, seed.tenantID, "claim_committed", Page{Limit: 100})
	if err != nil {
		t.Fatalf("ListBillingEvents(committed): %v", err)
	}
	if len(committed) != 1 || committed[0].EventType != "claim_committed" {
		t.Fatalf("filter mismatch; got %+v", committed)
	}
}

func TestReader_CountClaimsByStatus(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool := openIntegrationPool(t, ctx)
	seed := seedReaderGraph(t, ctx, pool)

	r := NewPgxReader(dbbilling.New(pool))

	counts, err := r.CountClaimsByStatus(ctx, seed.tenantID)
	if err != nil {
		t.Fatalf("CountClaimsByStatus: %v", err)
	}
	if counts["committed"] != 1 {
		t.Fatalf("expected 1 committed; got %v", counts)
	}
	if counts["aborted"] != 1 {
		t.Fatalf("expected 1 aborted; got %v", counts)
	}
}

// silence unused-import linter when integration_pg tag yields skipped tests
var _ = fmt.Sprintf
