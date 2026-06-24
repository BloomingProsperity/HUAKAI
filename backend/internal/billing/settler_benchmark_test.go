//go:build integration_pg

package billing

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"

	"github.com/BloomingProsperity/HUAKAI/internal/db"
	"github.com/BloomingProsperity/HUAKAI/internal/gateway"
)

type benchmarkSettleBase struct {
	tenantID          int64
	apiKeyID          int64
	userID            int64
	providerID        int64
	poolGroupID       int64
	channelID         int64
	providerAccountID int64
	fingerprintPrefix string
}

type benchmarkSettleClaim struct {
	claimID          int64
	acquisitionToken uuid.UUID
	fingerprint      string
}

var benchmarkSettleResult *SettleResult

func BenchmarkDefaultSettlerSettle(b *testing.B) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	pg := openBenchmarkPool(b, ctx)
	base := seedBenchmarkSettleBase(b, ctx, pg, b.N)
	claims := seedBenchmarkSettleClaims(b, ctx, pg, base, b.N)
	settler := NewSettler(pg)
	cost := decimal.RequireFromString("0.00012345")

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		got, err := settler.Settle(ctx, benchmarkSettleRequest(base, claims[i], cost))
		if err != nil {
			b.Fatalf("Settle iteration %d: %v", i, err)
		}
		benchmarkSettleResult = got
	}
	b.StopTimer()

	stat := pg.Stat()
	b.ReportMetric(float64(stat.TotalConns()), "pg_total_conns")
	b.ReportMetric(float64(stat.IdleConns()), "pg_idle_conns")
}

func openBenchmarkPool(b *testing.B, ctx context.Context) *pgxpool.Pool {
	b.Helper()
	dsn := os.Getenv("HUAKAI_TEST_DATABASE_URL")
	if dsn == "" {
		dsn = os.Getenv("HUAKAI_DATABASE_URL")
	}
	if dsn == "" {
		b.Skip("HUAKAI_TEST_DATABASE_URL/HUAKAI_DATABASE_URL not set; skipping DB-backed settle benchmark")
	}
	pg, err := db.Open(ctx, db.PoolConfig{DSN: dsn})
	if err != nil {
		b.Fatalf("db.Open: %v", err)
	}
	b.Cleanup(pg.Close)
	return pg
}

func seedBenchmarkSettleBase(b *testing.B, ctx context.Context, pg *pgxpool.Pool, plannedClaims int) benchmarkSettleBase {
	b.Helper()
	unique := "bench-settle-" + uuid.NewString()
	base := benchmarkSettleBase{fingerprintPrefix: "fingerprint-" + unique}
	if err := pg.QueryRow(ctx,
		`INSERT INTO tenants (name) VALUES ($1) RETURNING id`,
		"tenant-"+unique,
	).Scan(&base.tenantID); err != nil {
		b.Fatalf("seed tenant: %v", err)
	}
	b.Cleanup(func() {
		c := context.Background()
		_, _ = pg.Exec(c, `DELETE FROM usage_record_dlq WHERE tenant_id=$1`, base.tenantID)
		_, _ = pg.Exec(c, `DELETE FROM usage_records WHERE tenant_id=$1`, base.tenantID)
		_, _ = pg.Exec(c, `DELETE FROM billing_events WHERE tenant_id=$1`, base.tenantID)
		_, _ = pg.Exec(c, `DELETE FROM pool_slot_acquisitions WHERE tenant_id=$1`, base.tenantID)
		_, _ = pg.Exec(c, `DELETE FROM billing_ledger_claims WHERE tenant_id=$1`, base.tenantID)
		_, _ = pg.Exec(c, `DELETE FROM user_balances WHERE tenant_id=$1`, base.tenantID)
		_, _ = pg.Exec(c, `DELETE FROM provider_accounts WHERE tenant_id=$1`, base.tenantID)
		_, _ = pg.Exec(c, `DELETE FROM channels WHERE tenant_id=$1`, base.tenantID)
		_, _ = pg.Exec(c, `DELETE FROM pool_groups WHERE tenant_id=$1`, base.tenantID)
		_, _ = pg.Exec(c, `DELETE FROM providers WHERE tenant_id=$1`, base.tenantID)
		_, _ = pg.Exec(c, `DELETE FROM api_keys WHERE tenant_id=$1`, base.tenantID)
		_, _ = pg.Exec(c, `DELETE FROM users WHERE tenant_id=$1`, base.tenantID)
		_, _ = pg.Exec(c, `DELETE FROM tenants WHERE id=$1`, base.tenantID)
	})

	if err := pg.QueryRow(ctx,
		`INSERT INTO users (tenant_id, display_name) VALUES ($1, $2) RETURNING id`,
		base.tenantID, "user-"+unique,
	).Scan(&base.userID); err != nil {
		b.Fatalf("seed user: %v", err)
	}
	if err := pg.QueryRow(ctx,
		`INSERT INTO api_keys (tenant_id, user_id, name, key_hash, key_prefix, status)
		 VALUES ($1, $2, $3, $4, $5, 'active') RETURNING id`,
		base.tenantID, base.userID, "key-"+unique,
		"$2a$10$placeholder-not-resolved-by-benchmark",
		"hk_bench_"+unique[len(unique)-8:],
	).Scan(&base.apiKeyID); err != nil {
		b.Fatalf("seed api key: %v", err)
	}
	if err := pg.QueryRow(ctx,
		`INSERT INTO providers (tenant_id, code, display_name, upstream_protocol)
		 VALUES ($1, $2, $3, 'openai_chat') RETURNING id`,
		base.tenantID, "provider-"+unique, "Provider "+unique,
	).Scan(&base.providerID); err != nil {
		b.Fatalf("seed provider: %v", err)
	}
	if err := pg.QueryRow(ctx,
		`INSERT INTO pool_groups (tenant_id, name) VALUES ($1, $2) RETURNING id`,
		base.tenantID, "pool-"+unique,
	).Scan(&base.poolGroupID); err != nil {
		b.Fatalf("seed pool group: %v", err)
	}
	if err := pg.QueryRow(ctx,
		`INSERT INTO channels (tenant_id, pool_group_id, name) VALUES ($1, $2, $3) RETURNING id`,
		base.tenantID, base.poolGroupID, "channel-"+unique,
	).Scan(&base.channelID); err != nil {
		b.Fatalf("seed channel: %v", err)
	}
	if err := pg.QueryRow(ctx,
		`INSERT INTO provider_accounts (
			tenant_id, provider_id, channel_id, name, account_type, cap_concurrency, in_flight_count
		) VALUES ($1, $2, $3, $4, 'api_key', $5, $5) RETURNING id`,
		base.tenantID, base.providerID, base.channelID, "account-"+unique, max(plannedClaims, 1),
	).Scan(&base.providerAccountID); err != nil {
		b.Fatalf("seed provider account: %v", err)
	}
	return base
}

func seedBenchmarkSettleClaims(b *testing.B, ctx context.Context, pg *pgxpool.Pool, base benchmarkSettleBase, n int) []benchmarkSettleClaim {
	b.Helper()
	claims := make([]benchmarkSettleClaim, n)
	for i := 0; i < n; i++ {
		token := uuid.New()
		fingerprint := fmt.Sprintf("%s-%d", base.fingerprintPrefix, i)
		var claimID int64
		if err := pg.QueryRow(ctx,
			`INSERT INTO billing_ledger_claims (
				tenant_id, idempotency_key, request_fingerprint, api_key_id, user_id,
				logical_request_id, endpoint_family, requested_model, pooling_group_id,
				billing_policy_version, request_class, provider_account_id, acquisition_token,
				attempt_seq, predicted_cost, currency_code, lease_expires_at
			) VALUES (
				$1, $2, $3, $4, $5,
				$6, 'chat', 'gpt-4.1-mini', $7,
				'bench-v1', 'standard', $8, $9,
				1, $10, 'USD', NOW() + interval '90 seconds'
			) RETURNING id`,
			base.tenantID, fmt.Sprintf("idempotency-%s-%d", base.fingerprintPrefix, i), fingerprint,
			base.apiKeyID, base.userID, fmt.Sprintf("logical-%s-%d", base.fingerprintPrefix, i),
			base.poolGroupID, base.providerAccountID, token, decimal.RequireFromString("0.00010000"),
		).Scan(&claimID); err != nil {
			b.Fatalf("seed claim %d: %v", i, err)
		}
		if _, err := pg.Exec(ctx,
			`INSERT INTO pool_slot_acquisitions (
				tenant_id, provider_account_id, acquisition_token, claim_id, attempt_seq, lease_expires_at
			) VALUES ($1, $2, $3, $4, 1, NOW() + interval '90 seconds')`,
			base.tenantID, base.providerAccountID, token, claimID,
		); err != nil {
			b.Fatalf("seed slot %d: %v", i, err)
		}
		claims[i] = benchmarkSettleClaim{claimID: claimID, acquisitionToken: token, fingerprint: fingerprint}
	}
	return claims
}

func benchmarkSettleRequest(base benchmarkSettleBase, claim benchmarkSettleClaim, actualCost decimal.Decimal) SettleRequest {
	return SettleRequest{
		ClaimID:           claim.claimID,
		AccountID:         base.providerAccountID,
		AcquisitionToken:  claim.acquisitionToken,
		ActualCost:        actualCost,
		TenantID:          base.tenantID,
		APIKeyID:          base.apiKeyID,
		UserID:            base.userID,
		ProviderAccountID: base.providerAccountID,
		AttemptSeq:        1,
		RequestedModel:    "gpt-4.1-mini",
		RequestedAt:       time.Now().UTC(),
		UpstreamModel:     "gpt-4.1-mini",
		Stream:            false,
		Fingerprint:       claim.fingerprint,
		SnapshotVersion:   "registry:bench;router:bench",
		Draft: gateway.UsageRecordDraft{
			TokensInput:           64,
			TokensOutput:          32,
			ActualCost:            actualCost,
			RoutingReason:         []byte(`{"route":"benchmark"}`),
			EndClass:              gateway.StreamEndClass("non_streaming"),
			UsageSource:           gateway.UsageSourceReported,
			PendingReconciliation: false,
		},
	}
}
