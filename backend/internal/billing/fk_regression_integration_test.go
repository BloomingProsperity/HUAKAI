//go:build integration_pg

// Slice 2 (N+4b1) regression tests for the FK constraints added by
// migration 0009. Each test exercises ONE invariant of the new
// composite-FK shape and asserts the database rejects the bad write at
// the SQL layer (not just at app-layer validation).

package billing

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// TestN4b1_BlocksOrphanAPIKeyOnClaim asserts that inserting a
// billing_ledger_claims row with an api_key_id that has no matching row
// in api_keys fails with a foreign-key violation. Without 0009 the
// insert silently succeeded and the synthetic-id pattern obscured the
// gap.
func TestN4b1_BlocksOrphanAPIKeyOnClaim(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool := openPool(t, ctx)
	tenantID, _, userID := seedTenant(t, ctx, pool, "n4b1-orphan-key-"+uuid.NewString())

	const fakeAPIKeyID = int64(99999999)
	_, err := pool.Exec(ctx,
		`INSERT INTO billing_ledger_claims (
			tenant_id, idempotency_key, request_fingerprint, api_key_id, user_id,
			logical_request_id, endpoint_family, requested_model, pooling_group_id,
			billing_policy_version, request_class, attempt_seq, predicted_cost,
			currency_code, lease_expires_at
		 ) VALUES (
			$1, $2, $3, $4, $5,
			$6, 'chat', 'gpt-4.1-mini', NULL,
			'1.0', 'standard', 1, 0.01,
			'USD', NOW() + interval '60 seconds'
		 )`,
		tenantID, "idem-orphan", "fp-orphan", fakeAPIKeyID, userID,
		"lr-orphan",
	)
	if err == nil {
		t.Fatal("expected FK violation on orphan api_key_id; got nil")
	}
	if !strings.Contains(err.Error(), "fk_claims_api_key") {
		t.Fatalf("expected fk_claims_api_key violation; got %v", err)
	}
}

// TestN4b1_BlocksCrossTenantAPIKeyOnClaim is the cross-tenant defense
// case. Tenant A's api_keys row referenced from tenant B's claim must
// fail the COMPOSITE (tenant_id, api_key_id) FK. Single-column FK would
// have allowed it.
func TestN4b1_BlocksCrossTenantAPIKeyOnClaim(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool := openPool(t, ctx)
	tenantA, apiKeyA, _ := seedTenant(t, ctx, pool, "n4b1-tenantA-"+uuid.NewString())
	tenantB, _, userB := seedTenant(t, ctx, pool, "n4b1-tenantB-"+uuid.NewString())

	// Tenant B's claim referencing tenant A's api_key_id — composite FK
	// requires (tenant_id=B, api_key_id=apiKeyA) to exist in api_keys, but
	// the row exists only as (tenant_id=A, id=apiKeyA).
	_, err := pool.Exec(ctx,
		`INSERT INTO billing_ledger_claims (
			tenant_id, idempotency_key, request_fingerprint, api_key_id, user_id,
			logical_request_id, endpoint_family, requested_model, pooling_group_id,
			billing_policy_version, request_class, attempt_seq, predicted_cost,
			currency_code, lease_expires_at
		 ) VALUES (
			$1, $2, $3, $4, $5,
			$6, 'chat', 'gpt-4.1-mini', NULL,
			'1.0', 'standard', 1, 0.01,
			'USD', NOW() + interval '60 seconds'
		 )`,
		tenantB, "idem-xtenant", "fp-xtenant", apiKeyA, userB,
		"lr-xtenant",
	)
	if err == nil {
		t.Fatalf("composite FK MUST reject cross-tenant binding (tenantA=%d apiKey=%d -> tenantB=%d)", tenantA, apiKeyA, tenantB)
	}
	if !strings.Contains(err.Error(), "fk_claims_api_key") {
		t.Fatalf("expected fk_claims_api_key violation; got %v", err)
	}
}

// TestN4b1_RestrictsDeleteOfReferencedAPIKey asserts that DELETE on an
// api_keys row that's referenced from billing_ledger_claims fails with
// RESTRICT. Operators must use status='revoked' instead of DELETE.
func TestN4b1_RestrictsDeleteOfReferencedAPIKey(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool := openPool(t, ctx)
	tenantID, apiKeyID, userID := seedTenant(t, ctx, pool, "n4b1-restrict-"+uuid.NewString())

	if _, err := pool.Exec(ctx,
		`INSERT INTO billing_ledger_claims (
			tenant_id, idempotency_key, request_fingerprint, api_key_id, user_id,
			logical_request_id, endpoint_family, requested_model, pooling_group_id,
			billing_policy_version, request_class, attempt_seq, predicted_cost,
			currency_code, lease_expires_at
		 ) VALUES (
			$1, $2, $3, $4, $5,
			$6, 'chat', 'gpt-4.1-mini', NULL,
			'1.0', 'standard', 1, 0.01,
			'USD', NOW() + interval '60 seconds'
		 )`,
		tenantID, "idem-restrict", "fp-restrict", apiKeyID, userID,
		"lr-restrict",
	); err != nil {
		t.Fatalf("seed claim: %v", err)
	}

	if _, err := pool.Exec(ctx, `DELETE FROM api_keys WHERE id=$1`, apiKeyID); err == nil {
		t.Fatal("expected RESTRICT on api_keys delete; got nil")
	} else if !strings.Contains(err.Error(), "fk_claims_api_key") {
		t.Fatalf("expected fk_claims_api_key restrict; got %v", err)
	}
}

// seedProviderGraph seeds a tenant-scoped provider_account graph for tests
// that need to insert pool_slot_acquisitions rows. Cleanup is registered
// in FK-correct order so the seedTenant cleanup that runs AFTER us can
// drop the parent tenant successfully (codex N+4b1 pass-1 P2 fix).
func seedProviderGraph(t *testing.T, ctx context.Context, pool *pgxpool.Pool, tenantID int64, suffix string) (providerID, channelID, accountID int64) {
	t.Helper()
	short := suffix
	if len(short) > 8 {
		short = short[:8]
	}
	var poolGroupID int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO providers (tenant_id, code, display_name, upstream_protocol)
		 VALUES ($1, $2, $3, 'openai_chat') RETURNING id`,
		tenantID, "p-"+short, "P",
	).Scan(&providerID); err != nil {
		t.Fatalf("seed provider: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO pool_groups (tenant_id, name) VALUES ($1, $2) RETURNING id`,
		tenantID, "pg-"+short,
	).Scan(&poolGroupID); err != nil {
		t.Fatalf("seed pg: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO channels (tenant_id, pool_group_id, name) VALUES ($1, $2, $3) RETURNING id`,
		tenantID, poolGroupID, "ch-"+short,
	).Scan(&channelID); err != nil {
		t.Fatalf("seed ch: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO provider_accounts (
			tenant_id, provider_id, channel_id, name, account_type,
			cap_concurrency, in_flight_count
		 ) VALUES ($1, $2, $3, $4, 'api_key', 2, 0) RETURNING id`,
		tenantID, providerID, channelID, "acct-"+short,
	).Scan(&accountID); err != nil {
		t.Fatalf("seed account: %v", err)
	}
	t.Cleanup(func() {
		c := context.Background()
		// FK-ordered: provider_accounts -> channels -> pool_groups -> providers.
		_, _ = pool.Exec(c, `DELETE FROM pool_slot_acquisitions WHERE tenant_id=$1`, tenantID)
		_, _ = pool.Exec(c, `DELETE FROM provider_accounts WHERE tenant_id=$1`, tenantID)
		_, _ = pool.Exec(c, `DELETE FROM channels WHERE tenant_id=$1`, tenantID)
		_, _ = pool.Exec(c, `DELETE FROM pool_groups WHERE tenant_id=$1`, tenantID)
		_, _ = pool.Exec(c, `DELETE FROM providers WHERE tenant_id=$1`, tenantID)
	})
	return providerID, channelID, accountID
}

// TestN4b1_BlocksOrphanClaimOnPoolSlotAcquisition asserts that the new
// pool_slot_acquisitions.(tenant_id, claim_id) FK rejects orphan claim_id
// values. (The original migration 0001 left this as a deferred-FK
// comment for over a year.)
func TestN4b1_BlocksOrphanClaimOnPoolSlotAcquisition(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool := openPool(t, ctx)
	suffix := "n4b1-orphan-claim-" + uuid.NewString()
	tenantID, _, _ := seedTenant(t, ctx, pool, suffix)
	_, _, accountID := seedProviderGraph(t, ctx, pool, tenantID, uuid.NewString())

	const fakeClaimID = int64(99999999)
	_, err := pool.Exec(ctx,
		`INSERT INTO pool_slot_acquisitions (
			tenant_id, provider_account_id, acquisition_token, claim_id, lease_expires_at
		 ) VALUES ($1, $2, $3, $4, NOW() + interval '60 seconds')`,
		tenantID, accountID, uuid.NewString(), fakeClaimID,
	)
	if err == nil {
		t.Fatal("expected fk_psa_claim violation; got nil")
	}
	if !strings.Contains(err.Error(), "fk_psa_claim") {
		t.Fatalf("expected fk_psa_claim violation; got %v", err)
	}
}

// TestN4b1_BlocksCrossTenantClaimOnUsageRecord asserts that the composite
// (tenant_id, claim_id) FK on usage_records (replacing the single-column
// claim_id FK from migration 0002) rejects tenant B writing a usage row
// pointing at tenant A's claim. Codex N+4b1 pass-2 P1: same defense as
// pool_slot_acquisitions, broader scope.
func TestN4b1_BlocksCrossTenantClaimOnUsageRecord(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool := openPool(t, ctx)

	tenantA, apiKeyA, userA := seedTenant(t, ctx, pool, "n4b1-uA-"+uuid.NewString())
	var claimAID int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO billing_ledger_claims (
			tenant_id, idempotency_key, request_fingerprint, api_key_id, user_id,
			logical_request_id, endpoint_family, requested_model, pooling_group_id,
			billing_policy_version, request_class, attempt_seq, predicted_cost,
			currency_code, lease_expires_at
		 ) VALUES (
			$1, $2, $3, $4, $5,
			$6, 'chat', 'gpt-4.1-mini', NULL,
			'1.0', 'standard', 1, 0.01,
			'USD', NOW() + interval '60 seconds'
		 ) RETURNING id`,
		tenantA, "idem-uA", "fp-uA", apiKeyA, userA, "lr-uA",
	).Scan(&claimAID); err != nil {
		t.Fatalf("seed tenant A claim: %v", err)
	}

	tenantB, apiKeyB, userB := seedTenant(t, ctx, pool, "n4b1-uB-"+uuid.NewString())
	// Tenant B writes a usage_records row referencing tenant A's claim.
	// Composite FK on (tenant_id, claim_id) rejects this; api_key/user
	// FKs are satisfied by tenant B's own keys, so the only schema gate
	// is the new composite claim FK. Without it, tenant isolation for
	// immutable billing data is silently broken.
	_, err := pool.Exec(ctx,
		`INSERT INTO usage_records (
			tenant_id, claim_id, api_key_id, user_id, provider_account_id,
			acquisition_token, attempt_seq, end_class, requested_at, requested_model
		 ) VALUES (
			$1, $2, $3, $4, 1,
			$5, 1, 'non_streaming', NOW(), 'gpt-4.1-mini'
		 )`,
		tenantB, claimAID, apiKeyB, userB, uuid.New(),
	)
	if err == nil {
		t.Fatalf("composite FK MUST reject cross-tenant usage_records claim binding (tenantA=%d claim=%d -> tenantB=%d)", tenantA, claimAID, tenantB)
	}
	if !strings.Contains(err.Error(), "fk_usage_claim") {
		t.Fatalf("expected fk_usage_claim violation; got %v", err)
	}
}

// TestN4b1_BlocksCrossTenantClaimOnPoolSlotAcquisition asserts that the
// composite (tenant_id, claim_id) FK rejects tenant B binding a slot to
// tenant A's claim. Codex N+4b1 pass-1 P1: single-column FK would have
// allowed this footgun.
func TestN4b1_BlocksCrossTenantClaimOnPoolSlotAcquisition(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool := openPool(t, ctx)

	// Tenant A: real api_key + insert one billing_ledger_claims row to
	// borrow as the cross-tenant target.
	tenantA, apiKeyA, userA := seedTenant(t, ctx, pool, "n4b1-tA-"+uuid.NewString())
	var claimAID int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO billing_ledger_claims (
			tenant_id, idempotency_key, request_fingerprint, api_key_id, user_id,
			logical_request_id, endpoint_family, requested_model, pooling_group_id,
			billing_policy_version, request_class, attempt_seq, predicted_cost,
			currency_code, lease_expires_at
		 ) VALUES (
			$1, $2, $3, $4, $5,
			$6, 'chat', 'gpt-4.1-mini', NULL,
			'1.0', 'standard', 1, 0.01,
			'USD', NOW() + interval '60 seconds'
		 ) RETURNING id`,
		tenantA, "idem-tA", "fp-tA", apiKeyA, userA,
		"lr-tA",
	).Scan(&claimAID); err != nil {
		t.Fatalf("seed tenant A claim: %v", err)
	}

	// Tenant B: own provider graph; try to bind slot to tenant A's claim.
	tenantB, _, _ := seedTenant(t, ctx, pool, "n4b1-tB-"+uuid.NewString())
	_, _, accountBID := seedProviderGraph(t, ctx, pool, tenantB, uuid.NewString())

	_, err := pool.Exec(ctx,
		`INSERT INTO pool_slot_acquisitions (
			tenant_id, provider_account_id, acquisition_token, claim_id, lease_expires_at
		 ) VALUES ($1, $2, $3, $4, NOW() + interval '60 seconds')`,
		tenantB, accountBID, uuid.NewString(), claimAID,
	)
	if err == nil {
		t.Fatalf("composite FK MUST reject cross-tenant slot/claim binding (tenantA=%d claim=%d -> tenantB=%d)", tenantA, claimAID, tenantB)
	}
	if !strings.Contains(err.Error(), "fk_psa_claim") {
		t.Fatalf("expected fk_psa_claim violation; got %v", err)
	}
}
