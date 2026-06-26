//go:build integration_pg

// 针对迁移 0009 新增的 FK 约束的 Slice 2 回归测试。每个测试只验证新的
// 复合 FK 形态的一个不变式,并断言数据库在 SQL 层(而非仅在应用层校验)
// 拒绝错误写入。

package billing

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// TestN4b1_BlocksOrphanAPIKeyOnClaim 断言:插入一条 api_key_id 在 api_keys 中
// 没有对应行的 billing_ledger_claims 行,会因外键违例而失败。在 0009 之前,
// 这种插入会静默成功,而合成 id 模式掩盖了这一缺口。
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

// TestN4b1_BlocksCrossTenantAPIKeyOnClaim 是跨租户防御用例。从租户 B 的 claim
// 引用租户 A 的 api_keys 行必须被复合 (tenant_id, api_key_id) FK 拒绝。单列 FK
// 本会放行。
func TestN4b1_BlocksCrossTenantAPIKeyOnClaim(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool := openPool(t, ctx)
	tenantA, apiKeyA, _ := seedTenant(t, ctx, pool, "n4b1-tenantA-"+uuid.NewString())
	tenantB, _, userB := seedTenant(t, ctx, pool, "n4b1-tenantB-"+uuid.NewString())

	// 租户 B 的 claim 引用租户 A 的 api_key_id —— 复合 FK 要求
	// (tenant_id=B, api_key_id=apiKeyA) 在 api_keys 中存在,但该行只以
	// (tenant_id=A, id=apiKeyA) 形式存在。
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

// TestN4b1_RestrictsDeleteOfReferencedAPIKey 断言:对被 billing_ledger_claims
// 引用的 api_keys 行执行 DELETE 会因 RESTRICT 而失败。运维必须改用
// status='revoked' 而非 DELETE。
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

// seedProviderGraph 为需要插入 pool_slot_acquisitions 行的测试播种一个
// 租户范围的 provider_account 图。清理逻辑按 FK 正确顺序注册,使在我们之后
// 运行的 seedTenant 清理能成功删除父 tenant。
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
		// 按 FK 顺序:provider_accounts -> channels -> pool_groups -> providers。
		_, _ = pool.Exec(c, `DELETE FROM pool_slot_acquisitions WHERE tenant_id=$1`, tenantID)
		_, _ = pool.Exec(c, `DELETE FROM provider_accounts WHERE tenant_id=$1`, tenantID)
		_, _ = pool.Exec(c, `DELETE FROM channels WHERE tenant_id=$1`, tenantID)
		_, _ = pool.Exec(c, `DELETE FROM pool_groups WHERE tenant_id=$1`, tenantID)
		_, _ = pool.Exec(c, `DELETE FROM providers WHERE tenant_id=$1`, tenantID)
	})
	return providerID, channelID, accountID
}

// TestN4b1_BlocksOrphanClaimOnPoolSlotAcquisition 断言:新的
// pool_slot_acquisitions.(tenant_id, claim_id) FK 拒绝孤儿 claim_id 值。
//(最初的迁移 0001 把它当作一条 deferred-FK 注释搁置了一年多。)
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

// TestN4b1_BlocksCrossTenantClaimOnUsageRecord 断言:usage_records 上的复合
// (tenant_id, claim_id) FK(取代迁移 0002 的单列 claim_id FK)拒绝租户 B 写入
// 一条指向租户 A 的 claim 的 usage 行。与 pool_slot_acquisitions 同样的防御,
// 范围更广。
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
	_, _, accountB := seedProviderGraph(t, ctx, pool, tenantB, "n4b1-acctB-"+uuid.NewString())
	// 租户 B 写入一条引用租户 A 的 claim 的 usage_records 行。
	// (tenant_id, claim_id) 上的复合 FK 拒绝它;api_key/user 的 FK 由租户 B
	// 自己的 key 满足,所以唯一的 schema 门就是新的复合 claim FK。没有它,
	// 不可变计费数据的租户隔离会被静默破坏。
	_, err := pool.Exec(ctx,
		`INSERT INTO usage_records (
			tenant_id, claim_id, api_key_id, user_id, provider_account_id,
			acquisition_token, attempt_seq, end_class, requested_at, requested_model
		 ) VALUES (
			$1, $2, $3, $4, $6,
			$5, 1, 'non_streaming', NOW(), 'gpt-4.1-mini'
		 )`,
		tenantB, claimAID, apiKeyB, userB, uuid.New(), accountB,
	)
	if err == nil {
		t.Fatalf("composite FK MUST reject cross-tenant usage_records claim binding (tenantA=%d claim=%d -> tenantB=%d)", tenantA, claimAID, tenantB)
	}
	if !strings.Contains(err.Error(), "fk_usage_claim") {
		t.Fatalf("expected fk_usage_claim violation; got %v", err)
	}
}

// TestN4b1_BlocksCrossTenantClaimOnPoolSlotAcquisition 断言:复合
// (tenant_id, claim_id) FK 拒绝租户 B 把一个 slot 绑定到租户 A 的 claim。
// 单列 FK 本会放行这种自伤式操作。
func TestN4b1_BlocksCrossTenantClaimOnPoolSlotAcquisition(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool := openPool(t, ctx)

	// 租户 A:真实 api_key + 插入一条 billing_ledger_claims 行,
	// 用作跨租户目标。
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

	// 租户 B:自己的 provider 图;尝试把 slot 绑定到租户 A 的 claim。
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
