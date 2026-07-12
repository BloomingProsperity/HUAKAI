//go:build integration_pg

// 针对真实 PostgreSQL 测试的 Postgres 适配器:DBClaimGate、
// DBSlotManager、DBAccountSource。它们要保证的契约是生产期
// 从 selector 到 billing_ledger_claims + pool_slot_acquisitions +
// provider_accounts 的桥接。

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

// seedAdapterGraph 镜像 settler_integration_test 的 seed,但放在
// pool 包内,以保持在测试 cycle 限制之下。
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
	// 切片 2:用真实的 users + api_keys 行替代
	// 之前的合成 id 方案。Migration 0009 为
	// billing_ledger_claims/usage_records/billing_ledger_archive 的
	// (tenant_id, api_key_id) -> api_keys 增加了复合 FK,因此 claim 的 seed
	// 必须引用真实的 api_keys 行。
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
		// migration 0009 之后的 FK 链:pool_slot_acquisitions -> claims;
		//                      claims/usage/archive -> api_keys/users;
		//                      api_keys -> users。
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

	// 通过第二个 channel 在同一个 pool_group 内追加第二个 account。
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

func TestStaticWeightSelection(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	pgPool := openIntegrationPool(t, ctx)
	seed := seedAdapterGraph(t, ctx, pgPool, "static-weight")

	if _, err := pgPool.Exec(ctx,
		`UPDATE provider_accounts SET static_weight = 1, priority = 10, in_flight_count = 0, last_dispatch_at = NULL WHERE id = $1`,
		seed.providerAccountID,
	); err != nil {
		t.Fatalf("seed low-weight provider account: %v", err)
	}

	suffix := uuid.NewString()
	var heavyAccountID int64
	if err := pgPool.QueryRow(ctx,
		`INSERT INTO provider_accounts (
			tenant_id, provider_id, channel_id, name, account_type,
			cap_concurrency, in_flight_count, priority, last_dispatch_at, static_weight
		) VALUES ($1, $2, $3, $4, 'api_key', 2, 0, 10, NULL, 4) RETURNING id`,
		seed.tenantID, seed.providerID, seed.channelID, "acct-heavy-"+suffix,
	).Scan(&heavyAccountID); err != nil {
		t.Fatalf("seed heavy provider account: %v", err)
	}

	selector := NewDefaultSelector(
		NewDBAccountSource(dbbilling.New(pgPool)),
		WithRoutingPolicySource(&stubPolicy{p: &RoutingPolicy{SelectionMode: "priority_weighted"}}),
		WithSlotManager(newMemSlotManager()),
	)
	counts := map[int64]int{}
	for i := 0; i < 1000; i++ {
		res, err := selector.Select(ctx, SelectionRequest{
			TenantID:    seed.tenantID,
			PoolGroupID: seed.poolGroupID,
		})
		if err != nil {
			t.Fatalf("Select iteration %d: %v", i, err)
		}
		counts[res.AccountID]++
	}
	share := float64(counts[heavyAccountID]) / 1000.0
	// 变异:DBAccountSource 不把 static_weight 写入 AccountSnapshot.Weight 时,
	// 两个同优先级账号退化为约 50/50, 不会落在 70%-85% 窗口。
	if share < 0.70 || share > 0.85 {
		t.Fatalf("heavy account selected %d/1000 (share %.3f), want weight-4 share in [0.70,0.85]; counts=%v",
			counts[heavyAccountID], share, counts)
	}
}

func TestDBAccountSource_ListByPoolGroupFiltersProtocolFamily(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pgPool := openIntegrationPool(t, ctx)
	seed := seedAdapterGraph(t, ctx, pgPool, "src-family")

	suffix := uuid.NewString()
	var sessionProviderID, sessionChannelID, sessionAccountID int64
	if err := pgPool.QueryRow(ctx,
		`INSERT INTO providers (tenant_id, code, display_name, upstream_protocol)
		 VALUES ($1, $2, $3, 'anthropic_claude_session') RETURNING id`,
		seed.tenantID, "claude-session-"+suffix, "Claude Session "+suffix,
	).Scan(&sessionProviderID); err != nil {
		t.Fatalf("seed session provider: %v", err)
	}
	if err := pgPool.QueryRow(ctx,
		`INSERT INTO channels (tenant_id, pool_group_id, name) VALUES ($1, $2, $3) RETURNING id`,
		seed.tenantID, seed.poolGroupID, "session-ch-"+suffix,
	).Scan(&sessionChannelID); err != nil {
		t.Fatalf("seed session channel: %v", err)
	}
	if err := pgPool.QueryRow(ctx,
		`INSERT INTO provider_accounts (
			tenant_id, provider_id, channel_id, name, account_type,
			cap_concurrency, in_flight_count, priority
		) VALUES ($1, $2, $3, $4, 'oauth', 4, 0, 50) RETURNING id`,
		seed.tenantID, sessionProviderID, sessionChannelID, "session-acct-"+suffix,
	).Scan(&sessionAccountID); err != nil {
		t.Fatalf("seed session account: %v", err)
	}

	src := NewDBAccountSource(dbbilling.New(pgPool))
	accounts, err := src.ListAccounts(ctx, SelectionRequest{
		TenantID:       seed.tenantID,
		PoolGroupID:    seed.poolGroupID,
		RequestedModel: "claude-3-5-sonnet",
		ProtocolFamily: "anthropic_claude_session",
	})
	if err != nil {
		t.Fatalf("ListAccounts: %v", err)
	}
	// 变异:去掉 upstream_protocol 谓词会返回两个 allow-list 为空的 account。
	if len(accounts) != 1 || accounts[0].ID != sessionAccountID {
		t.Fatalf("accounts=%+v; want only Claude session protocol account %d", accounts, sessionAccountID)
	}
	if accounts[0].ProtocolFamily != "anthropic_claude_session" {
		t.Fatalf("ProtocolFamily=%q, want anthropic_claude_session", accounts[0].ProtocolFamily)
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
