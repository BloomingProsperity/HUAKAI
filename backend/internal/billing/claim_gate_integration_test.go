//go:build integration_pg

// F-OBS-001 Tx1 ClaimGate integration tests against real PostgreSQL.
// Requires the dev PG container + applied migrations:
//
//	make db-up && make db-migrate
//	make test-integration
//
// Strong assertions per spec §Tx1 + AT-OBS-001/002.
package billing

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"

	"github.com/BloomingProsperity/HUAKAI/internal/db"
)

func dsn(t *testing.T) string {
	t.Helper()
	v := os.Getenv("HUAKAI_DATABASE_URL")
	if v == "" {
		t.Skip("HUAKAI_DATABASE_URL not set; skipping integration test")
	}
	return v
}

// seedTenant inserts a fresh tenant + real users + api_keys row + registers
// cleanup. Returns IDs. Migration 0009 replaced the previous
// synthetic-id pattern (apiKeyID = tenantID*100 + 1) with a real seed
// because migration 0009 added composite FKs from billing_ledger_claims
// (tenant_id, api_key_id) -> api_keys (tenant_id, id) and from
// (tenant_id, user_id) -> users (tenant_id, id).
//
// The bcrypt hash + key_prefix are placeholders — the resolver path is
// not exercised by these tests; the FK target just needs an api_keys row
// to exist with the same (tenant_id, id) pair.
func seedTenant(t *testing.T, ctx context.Context, pool *pgxpool.Pool, suffix string) (tenantID, apiKeyID, userID int64) {
	t.Helper()
	tenantName := fmt.Sprintf("test-tenant-%s-%d", suffix, time.Now().UnixNano())
	if err := pool.QueryRow(ctx,
		`INSERT INTO tenants (name) VALUES ($1) RETURNING id`,
		tenantName,
	).Scan(&tenantID); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO users (tenant_id, display_name) VALUES ($1, $2) RETURNING id`,
		tenantID, "user-"+suffix,
	).Scan(&userID); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO api_keys (tenant_id, user_id, name, key_hash, key_prefix, status)
		 VALUES ($1, $2, $3, $4, $5, 'active') RETURNING id`,
		tenantID, userID, "key-"+suffix,
		"$2a$10$placeholder-not-resolved-by-billing-tests",
		"hk_test_"+suffix[:min(len(suffix), 8)],
	).Scan(&apiKeyID); err != nil {
		t.Fatalf("seed api_key: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO user_balances (tenant_id, user_id, balance, held) VALUES ($1, $2, 10, 0)`,
		tenantID, userID,
	); err != nil {
		t.Fatalf("seed user balance: %v", err)
	}
	t.Cleanup(func() {
		c := context.Background()
		// FK chain: claims/usage/archive -> api_keys -> users -> tenants.
		_, _ = pool.Exec(c, `DELETE FROM usage_record_dlq WHERE tenant_id=$1`, tenantID)
		_, _ = pool.Exec(c, `DELETE FROM usage_records WHERE tenant_id=$1`, tenantID)
		_, _ = pool.Exec(c, `DELETE FROM billing_events WHERE tenant_id=$1`, tenantID)
		_, _ = pool.Exec(c, `DELETE FROM pool_slot_acquisitions WHERE tenant_id=$1`, tenantID)
		_, _ = pool.Exec(c, `DELETE FROM billing_ledger_claims WHERE tenant_id=$1`, tenantID)
		_, _ = pool.Exec(c, `DELETE FROM billing_ledger_archive WHERE tenant_id=$1`, tenantID)
		_, _ = pool.Exec(c, `DELETE FROM user_balances WHERE tenant_id=$1`, tenantID)
		_, _ = pool.Exec(c, `DELETE FROM api_keys WHERE tenant_id=$1`, tenantID)
		_, _ = pool.Exec(c, `DELETE FROM users WHERE tenant_id=$1`, tenantID)
		_, _ = pool.Exec(c, `DELETE FROM tenants WHERE id=$1`, tenantID)
	})
	return tenantID, apiKeyID, userID
}

// min is a tiny helper since the std-lib min(int, int) is only Go 1.21+.
// Once the module is verified to be on >= 1.21 this can be deleted.
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func openPool(t *testing.T, ctx context.Context) *pgxpool.Pool {
	t.Helper()
	p, err := db.Open(ctx, db.PoolConfig{DSN: dsn(t)})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(p.Close)
	return p
}

func baseRequest(tenantID, apiKeyID, userID int64) ReserveRequest {
	return ReserveRequest{
		TenantID:              tenantID,
		APIKeyID:              apiKeyID,
		UserID:                userID,
		LogicalRequestID:      fmt.Sprintf("logreq-%d", tenantID),
		EndpointFamily:        "chat",
		NormalizedPayloadHash: "hash-AAA",
		RequestedModel:        "claude-3-5-sonnet",
		PoolingGroupID:        0,
		BillingPolicyVersion:  "1.0",
		RequestClass:          "standard",
		PredictedCost:         decimal.NewFromFloat(0.01),
	}
}

// TestClaimReserveLeaseCoversMaxRequestLifetime 守 reserve 写入的 claim 租约窗口
// 覆盖最大请求生命周期。否则 LeaseSweeper(按 lease_expires_at<NOW() 捞 reserving
// claim 无条件 Abort)会在长流(可达 600s)仍在传输时把活 claim 误 Abort:已交付内容
// 永不计费(亏钱)+ in_flight 在流仍活时被减低估致上游账号超并发(CONC-1/LEAK-1)。
// 变异判据:把 DefaultClaimLeaseWindow 还原成旧值 90s → lease_expires_at-reserveStart
// ≈90s < 10min → 本测试 RED。
func TestClaimReserveLeaseCoversMaxRequestLifetime(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool := openPool(t, ctx)
	tenantID, apiKeyID, userID := seedTenant(t, ctx, pool, "lease")
	gate := NewClaimGate(pool)

	reserveStart := time.Now().UTC()
	r, err := gate.Reserve(ctx, baseRequest(tenantID, apiKeyID, userID))
	if err != nil {
		t.Fatalf("Reserve: %v", err)
	}
	if r.ClaimID == 0 {
		t.Fatalf("Reserve 须返非零 ClaimID")
	}
	var leaseExpiresAt time.Time
	if err := pool.QueryRow(ctx,
		`SELECT lease_expires_at FROM billing_ledger_claims WHERE tenant_id=$1 AND id=$2`,
		tenantID, r.ClaimID,
	).Scan(&leaseExpiresAt); err != nil {
		t.Fatalf("读 lease_expires_at: %v", err)
	}
	covered := leaseExpiresAt.Sub(reserveStart)
	const minCover = 10 * time.Minute // 须 > 最大流时长 600s + 结算/DLQ 余量
	if covered < minCover {
		t.Fatalf("claim 租约只覆盖 %v(< %v):长流会被 LeaseSweeper 中途 abort 致亏钱+超并发;租约须 >= 最大请求生命周期",
			covered, minCover)
	}
}

// AT-OBS-001 strong: Idempotent replay (same fingerprint) returns cached
// claim, NO second row inserted, IdempotencyHit=true on the second call.
func TestAT_OBS_001_IdempotentReplay(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool := openPool(t, ctx)
	tenantID, apiKeyID, userID := seedTenant(t, ctx, pool, "001")
	gate := NewClaimGate(pool)

	req := baseRequest(tenantID, apiKeyID, userID)
	r1, err := gate.Reserve(ctx, req)
	if err != nil {
		t.Fatalf("first Reserve: %v", err)
	}
	if r1.IdempotencyHit {
		t.Fatalf("first Reserve must NOT report IdempotencyHit=true")
	}
	if r1.ClaimID == 0 {
		t.Fatalf("first Reserve must return non-zero ClaimID")
	}

	// Mark first claim committed so the second Reserve takes the cached-replay branch.
	if _, err := pool.Exec(ctx,
		`UPDATE billing_ledger_claims SET status='committed', settled_at=NOW() WHERE id=$1`,
		r1.ClaimID,
	); err != nil {
		t.Fatalf("mark committed: %v", err)
	}

	r2, err := gate.Reserve(ctx, req)
	if err != nil {
		t.Fatalf("second Reserve: %v", err)
	}
	if !r2.IdempotencyHit {
		t.Fatalf("second Reserve MUST set IdempotencyHit=true on same-fingerprint replay; got %+v", r2)
	}
	if r2.ClaimID != r1.ClaimID {
		t.Fatalf("second Reserve must return SAME ClaimID; got first=%d second=%d", r1.ClaimID, r2.ClaimID)
	}

	// Spec invariant: NO second claim row inserted.
	var count int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM billing_ledger_claims WHERE tenant_id=$1 AND api_key_id=$2`,
		tenantID, apiKeyID,
	).Scan(&count); err != nil {
		t.Fatalf("count rows: %v", err)
	}
	if count != 1 {
		t.Fatalf("idempotent replay must NOT insert a second row; got %d rows", count)
	}
}

// AT-OBS-002 strong: Replay attack (different fingerprint, same logical_request_id)
// returns ErrFingerprintConflict + FingerprintConflict=true, NO charge, NO row.
func TestAT_OBS_002_FingerprintConflict(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool := openPool(t, ctx)
	tenantID, apiKeyID, userID := seedTenant(t, ctx, pool, "002")
	gate := NewClaimGate(pool)

	req1 := baseRequest(tenantID, apiKeyID, userID)
	r1, err := gate.Reserve(ctx, req1)
	if err != nil {
		t.Fatalf("first Reserve: %v", err)
	}
	if r1.ClaimID == 0 {
		t.Fatalf("first Reserve must return ClaimID")
	}

	// Same logical_request_id, different normalized_payload_hash → replay attack.
	req2 := req1
	req2.NormalizedPayloadHash = "hash-BBB-attacker"
	r2, err := gate.Reserve(ctx, req2)
	if !errors.Is(err, ErrFingerprintConflict) {
		t.Fatalf("expected ErrFingerprintConflict on payload hash divergence; got err=%v r=%+v", err, r2)
	}
	if r2 == nil || !r2.FingerprintConflict {
		t.Fatalf("expected FingerprintConflict=true on result; got %+v", r2)
	}

	// Spec invariant: only ONE row exists for the original request.
	var count int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM billing_ledger_claims WHERE tenant_id=$1 AND api_key_id=$2`,
		tenantID, apiKeyID,
	).Scan(&count); err != nil {
		t.Fatalf("count rows: %v", err)
	}
	if count != 1 {
		t.Fatalf("fingerprint conflict must NOT insert a row; got %d total rows", count)
	}
}

// Plan §F contract: function returns typed error when PG is unreachable,
// not a 200 OK. Constructed with nil pool → ErrPoolNotConfigured.
func TestClaimGate_NilPool_ReturnsTypedError(t *testing.T) {
	gate := NewClaimGate(nil)
	_, err := gate.Reserve(context.Background(), ReserveRequest{TenantID: 1, APIKeyID: 1})
	if !errors.Is(err, ErrPoolNotConfigured) {
		t.Fatalf("expected ErrPoolNotConfigured for nil pool; got %v", err)
	}
}

// AT-OBS-014 partial: Money decimal precision survives PG numeric(20,8) round-trip.
// 1_000_000 × 0.0000001 == 0.10 exactly when stored and read back.
func TestAT_OBS_014_MoneyPrecisionRoundTrip(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool := openPool(t, ctx)
	tenantID, apiKeyID, userID := seedTenant(t, ctx, pool, "014")
	gate := NewClaimGate(pool)

	micropenny := decimal.NewFromFloat(0.0000001)
	expected := micropenny.Mul(decimal.NewFromInt(1_000_000))
	if !expected.Equal(decimal.NewFromFloat(0.10)) {
		t.Fatalf("test arithmetic broken: %s != 0.10", expected)
	}

	req := baseRequest(tenantID, apiKeyID, userID)
	req.PredictedCost = expected
	r, err := gate.Reserve(ctx, req)
	if err != nil {
		t.Fatalf("Reserve: %v", err)
	}

	var got decimal.Decimal
	if err := pool.QueryRow(ctx,
		`SELECT predicted_cost FROM billing_ledger_claims WHERE id=$1`,
		r.ClaimID,
	).Scan(&got); err != nil {
		t.Fatalf("read back predicted_cost: %v", err)
	}
	if !got.Equal(expected) {
		t.Fatalf("decimal lost precision through PG: stored=%s expected=%s", got, expected)
	}
}
