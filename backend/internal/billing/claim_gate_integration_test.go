//go:build integration_pg

// F-OBS-001 Tx1 ClaimGate 针对真实 PostgreSQL 的集成测试。
// 需要 dev PG 容器 + 已应用的迁移:
//
//	make db-up && make db-migrate
//	make test-integration
//
// 按规格 §Tx1 + AT-OBS-001/002 做强断言。
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

// seedTenant 插入一个全新的 tenant + 真实 users + api_keys 行,并注册
// 清理逻辑。返回各 ID。迁移 0009 用真实 seed 取代了此前的合成 id 模式
// (apiKeyID = tenantID*100 + 1),因为迁移 0009 为 billing_ledger_claims
// 增加了从 (tenant_id, api_key_id) -> api_keys (tenant_id, id) 以及从
// (tenant_id, user_id) -> users (tenant_id, id) 的复合 FK。
//
// bcrypt hash + key_prefix 只是占位符 —— 这些测试不会走 resolver 路径;
// FK 目标只需存在一个 (tenant_id, id) 对相同的 api_keys 行即可。
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
		// FK 链:claims/usage/archive -> api_keys -> users -> tenants。
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

// min 是一个小工具,因为标准库的 min(int, int) 仅 Go 1.21+ 才有。
// 一旦确认模块已在 >= 1.21,即可删除它。
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

// 租户停用与资金预占共用 tenants 行锁。停用完成后，即使调用方在更早的
// API Key 认证阶段拿到过有效身份，也不得再创建 claim、hold 或触发上游。
// 变异：删除 ClaimGate 的 active tenant 锁定查询，本测试会落下一条 reserving claim。
func TestClaimGateRejectsDisabledTenantBeforeCreatingMoneyFacts(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool := openPool(t, ctx)
	tenantID, apiKeyID, userID := seedTenant(t, ctx, pool, "tenant-disabled")
	if _, err := pool.Exec(ctx,
		`UPDATE tenants SET status='disabled', version=version+1 WHERE id=$1`,
		tenantID,
	); err != nil {
		t.Fatalf("停用测试租户: %v", err)
	}

	_, err := NewClaimGate(pool).Reserve(ctx, baseRequest(tenantID, apiKeyID, userID))
	if !errors.Is(err, ErrTenantInactive) {
		t.Fatalf("停用租户 Reserve err=%v want ErrTenantInactive", err)
	}
	var claims int
	var balance, held decimal.Decimal
	if err := pool.QueryRow(ctx,
		`SELECT count(*)::int FROM billing_ledger_claims WHERE tenant_id=$1`,
		tenantID,
	).Scan(&claims); err != nil {
		t.Fatalf("读取停用租户 claim: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`SELECT balance, held FROM user_balances WHERE tenant_id=$1 AND user_id=$2`,
		tenantID, userID,
	).Scan(&balance, &held); err != nil {
		t.Fatalf("读取停用租户余额: %v", err)
	}
	if claims != 0 || !balance.Equal(decimal.NewFromInt(10)) || !held.IsZero() {
		t.Fatalf("停用租户仍产生资金事实 claims=%d balance=%s held=%s", claims, balance, held)
	}
}

// AT-OBS-001 强断言:幂等重放(相同指纹)返回缓存的 claim,
// 不插入第二行,第二次调用时 IdempotencyHit=true。
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

	// 把第一个 claim 标记为 committed,使第二次 Reserve 走缓存重放分支。
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

	// 规格不变式:不插入第二行 claim。
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

// AT-OBS-002 强断言:重放攻击(不同指纹、相同 logical_request_id)
// 返回 ErrFingerprintConflict + FingerprintConflict=true,不计费、不插行。
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

	// 相同 logical_request_id、不同 normalized_payload_hash → 重放攻击。
	req2 := req1
	req2.NormalizedPayloadHash = "hash-BBB-attacker"
	r2, err := gate.Reserve(ctx, req2)
	if !errors.Is(err, ErrFingerprintConflict) {
		t.Fatalf("expected ErrFingerprintConflict on payload hash divergence; got err=%v r=%+v", err, r2)
	}
	if r2 == nil || !r2.FingerprintConflict {
		t.Fatalf("expected FingerprintConflict=true on result; got %+v", r2)
	}

	// 规格不变式:原请求只存在唯一一行。
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

// 计划 §F 契约:PG 不可达时函数返回一个有类型的错误,而非 200 OK。
// 用 nil pool 构造 → ErrPoolNotConfigured。
func TestClaimGate_NilPool_ReturnsTypedError(t *testing.T) {
	gate := NewClaimGate(nil)
	_, err := gate.Reserve(context.Background(), ReserveRequest{TenantID: 1, APIKeyID: 1})
	if !errors.Is(err, ErrPoolNotConfigured) {
		t.Fatalf("expected ErrPoolNotConfigured for nil pool; got %v", err)
	}
}

// AT-OBS-014 部分覆盖:金额 decimal 精度在 PG numeric(20,8) 往返后保持不变。
// 存入再读回时,1_000_000 × 0.0000001 精确等于 0.10。
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
