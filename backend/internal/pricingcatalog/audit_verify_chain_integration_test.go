//go:build integration_pg

package pricingcatalog

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"

	"github.com/BloomingProsperity/HUAKAI/internal/db"
	"github.com/BloomingProsperity/HUAKAI/internal/sign"
)

// TestVerifyChainIntegration_RealPostgresTamperDetection 针对真实的 PostgreSQL
// pricing_ratio_audit_log 运行 VerifyChain：它通过审计 upsert 路径写入一条签名链，
// 先证明其干净，随后用原生 SQL 篡改其中一条已存储的 entry_hash（模拟悄悄改动
// 某条计费倍率变更记录），并断言链证明能精确定位被篡改的那条记录。
// 仅当设置了 HUAKAI_DATABASE_URL 时才运行（参见 db/pgconn_integration_test.go）。
func TestVerifyChainIntegration_RealPostgresTamperDetection(t *testing.T) {
	dsn := os.Getenv("HUAKAI_DATABASE_URL")
	if dsn == "" {
		t.Skip("HUAKAI_DATABASE_URL not set; skipping integration_pg")
	}
	ctx := context.Background()
	pool, err := db.Open(ctx, db.PoolConfig{DSN: dsn})
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(pool.Close)

	tenantID, poolGroupID := resetPricingRatioAuditState(t, ctx, pool)

	signer, err := sign.GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	store := NewPostgresStoreWithAuditSigner(pool, signer)

	for _, ratio := range []string{"1.10", "1.20"} {
		if _, err := store.UpsertRatio(ctx, UpsertRatioParams{
			TenantID:    tenantID,
			PoolGroupID: poolGroupID,
			Ratio:       decimal.RequireFromString(ratio),
			PublicRatio: true,
			Actor:       "admin_token:990",
			ActorRole:   "platform_admin",
		}); err != nil {
			t.Fatalf("UpsertRatio(%s): %v", ratio, err)
		}
	}

	clean, err := store.VerifyChain(ctx)
	if err != nil {
		t.Fatalf("VerifyChain(clean): %v", err)
	}
	if !clean.OK || clean.RowID != 0 {
		t.Fatalf("clean verify=%+v want OK with no offending row", clean)
	}

	// 直接在 Postgres 中篡改最新一条记录已存储的哈希值。
	var tamperedID int64
	if err := pool.QueryRow(ctx,
		`UPDATE pricing_ratio_audit_log
		    SET entry_hash = decode(repeat('00', 32), 'hex')
		  WHERE id = (SELECT max(id) FROM pricing_ratio_audit_log)
		RETURNING id`,
	).Scan(&tamperedID); err != nil {
		t.Fatalf("tamper entry_hash: %v", err)
	}

	tampered, err := store.VerifyChain(ctx)
	if err != nil {
		t.Fatalf("VerifyChain(tampered): %v", err)
	}
	if tampered.OK {
		t.Fatalf("real PG tampered chain reported OK=true; verifier missed mutation")
	}
	if tampered.RowID != tamperedID {
		t.Fatalf("offending row_id=%d want %d (the row mutated in PG)", tampered.RowID, tamperedID)
	}
	if tampered.Reason == "" {
		t.Fatalf("tampered verify gave empty reason")
	}
}

// resetPricingRatioAuditState 清空全局的定价倍率审计链，以免集成断言被其他
// 测试留下的记录干扰，然后种入一个临时的 tenant + pool group 以满足倍率的外键
// 约束。返回种入的 tenant id 和 pool group id。仅在由 build tag + 环境变量跳过
// 双重守护的专用集成数据库上运行。
func resetPricingRatioAuditState(t *testing.T, ctx context.Context, pool *pgxpool.Pool) (int64, int64) {
	t.Helper()
	if _, err := pool.Exec(ctx, `TRUNCATE pricing_ratio_audit_log RESTART IDENTITY`); err != nil {
		t.Fatalf("truncate pricing_ratio_audit_log: %v", err)
	}
	var tenantID int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO tenants (name) VALUES ('pricingcatalog-verify-it') RETURNING id`,
	).Scan(&tenantID); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	var poolGroupID int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO pool_groups (tenant_id, name) VALUES ($1, 'pricingcatalog-verify-it') RETURNING id`,
		tenantID,
	).Scan(&poolGroupID); err != nil {
		t.Fatalf("seed pool_group: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM pool_group_pricing_ratios WHERE tenant_id = $1`, tenantID)
		_, _ = pool.Exec(ctx, `DELETE FROM pool_groups WHERE tenant_id = $1`, tenantID)
		_, _ = pool.Exec(ctx, `DELETE FROM tenants WHERE id = $1`, tenantID)
	})
	return tenantID, poolGroupID
}
