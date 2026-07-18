//go:build integration_pg

// dbmigrate 真 PG 验证:进程内自迁移对一个全新空库,必须把 schema 真正建起来并升到最新,
// 且重复运行幂等(无变更不报错)。
//
// 判别 fixture 关键:测试用 HUAKAI_DATABASE_URL 作维护连接、临时 CREATE 一个全新空库,对它跑
// dbmigrate.Up;断言 ① 核心业务表(tenants)在空库里被真正建出来 ② schema_migrations 记录了
// 非零版本且 dirty=false ③ 再跑一次 Up 仍返回 nil(幂等)。把 dbmigrate.Up 改成空操作(不真跑迁移),
// tenants 不存在 → 测试 red;符合 mutation 自检。用后 DROP 临时库,零残留。
package dbmigrate_test

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/jackc/pgx/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/dbmigrate"
	sqlmigrations "github.com/BloomingProsperity/HUAKAI/sql"
)

func TestUp_AppliesAllMigrationsToEmptyDB_AndIsIdempotent(t *testing.T) {
	baseDSN := os.Getenv("HUAKAI_DATABASE_URL")
	if baseDSN == "" {
		t.Skip("HUAKAI_DATABASE_URL not set; skipping integration_pg")
	}
	ctx := context.Background()
	tmpDSN := createTemporaryMigrationDatabase(t, ctx, baseDSN, "huakai_dbmigrate_test")

	// 首次:空库 → 应用全部迁移。
	if err := dbmigrate.Up(sqlmigrations.Files, tmpDSN); err != nil {
		t.Fatalf("首次 Up(空库)失败: %v", err)
	}

	conn, err := pgx.Connect(ctx, tmpDSN)
	if err != nil {
		t.Fatalf("连接临时库失败: %v", err)
	}
	defer conn.Close(ctx)

	// ① 核心业务表真的建出来了(迁移确实跑了,而非空操作)。
	var tenantsReg *string
	if err := conn.QueryRow(ctx, "SELECT to_regclass('public.tenants')::text").Scan(&tenantsReg); err != nil {
		t.Fatalf("查 tenants 表失败: %v", err)
	}
	if tenantsReg == nil {
		t.Fatal("空库跑完 Up 后 public.tenants 仍不存在 —— 迁移没真正应用")
	}

	// ② schema_migrations 记录非零版本且未 dirty。
	var version int64
	var dirty bool
	if err := conn.QueryRow(ctx, "SELECT version, dirty FROM schema_migrations").Scan(&version, &dirty); err != nil {
		t.Fatalf("查 schema_migrations 失败(应被 golang-migrate 建出): %v", err)
	}
	if version <= 0 {
		t.Fatalf("schema_migrations.version = %d,应为已应用的最高迁移版本(非零)", version)
	}
	if dirty {
		t.Fatal("schema_migrations.dirty = true —— 迁移中途失败,schema 处于脏态")
	}

	// ③ 再跑一次:无变更,幂等返回 nil(ErrNoChange 被吞)。
	if err := dbmigrate.Up(sqlmigrations.Files, tmpDSN); err != nil {
		t.Fatalf("第二次 Up(已最新)应幂等返回 nil,got %v", err)
	}
}

func TestRequestObservationMigrationSeparatesAndRoundTrips(t *testing.T) {
	baseDSN := os.Getenv("HUAKAI_DATABASE_URL")
	if baseDSN == "" {
		t.Skip("HUAKAI_DATABASE_URL not set; skipping integration_pg")
	}
	ctx := context.Background()
	tmpDSN := createTemporaryMigrationDatabase(t, ctx, baseDSN, "huakai_request_observation_test")

	src, err := iofs.New(sqlmigrations.Files, "migrations")
	if err != nil {
		t.Fatalf("构建迁移源失败: %v", err)
	}
	m, err := migrate.NewWithSourceInstance("iofs", src, tmpDSN)
	if err != nil {
		t.Fatalf("初始化迁移器失败: %v", err)
	}
	defer func() {
		sourceErr, databaseErr := m.Close()
		if sourceErr != nil || databaseErr != nil {
			t.Logf("关闭迁移器: source=%v database=%v", sourceErr, databaseErr)
		}
	}()

	if err := m.Migrate(181); err != nil {
		t.Fatalf("迁移到旧合同版本 181 失败: %v", err)
	}
	conn, err := pgx.Connect(ctx, tmpDSN)
	if err != nil {
		t.Fatalf("连接临时库失败: %v", err)
	}
	defer conn.Close(ctx)

	passiveAt := time.Date(2026, 7, 15, 8, 0, 0, 0, time.UTC)
	activeAt := time.Date(2026, 7, 15, 9, 0, 0, 0, time.UTC)
	postMigrationObservedAt := time.Date(2026, 7, 15, 10, 0, 0, 0, time.UTC)
	passiveID, activeID := seedRequestObservationMigrationRows(t, ctx, conn, passiveAt, activeAt)

	if err := m.Migrate(189); err != nil {
		t.Fatalf("应用 0189 上迁移失败: %v", err)
	}
	assertRequestObservationMigrationState(t, ctx, conn, passiveID, activeID, passiveAt, activeAt)

	// 模拟迁移后同一主动探测账号又收到普通请求完成事件。回滚时不得用它覆盖 activeAt。
	if _, err := conn.Exec(ctx,
		`UPDATE provider_accounts SET last_request_observed_at = $1 WHERE id = $2`,
		postMigrationObservedAt, activeID,
	); err != nil {
		t.Fatalf("种入迁移后普通请求观测: %v", err)
	}
	if err := m.Migrate(181); err != nil {
		t.Fatalf("回滚 0189 失败: %v", err)
	}

	var passiveProbeAt, activeProbeAt *time.Time
	if err := conn.QueryRow(ctx,
		`SELECT
		    (SELECT last_probe_at FROM provider_accounts WHERE id = $1),
		    (SELECT last_probe_at FROM provider_accounts WHERE id = $2)`,
		passiveID, activeID,
	).Scan(&passiveProbeAt, &activeProbeAt); err != nil {
		t.Fatalf("读取回滚后时间: %v", err)
	}
	if passiveProbeAt == nil || !passiveProbeAt.Equal(passiveAt) {
		t.Fatalf("回滚未恢复历史被动时间: got %v want %v", passiveProbeAt, passiveAt)
	}
	if activeProbeAt == nil || !activeProbeAt.Equal(activeAt) {
		t.Fatalf("回滚覆盖了主动探测时间: got %v want %v", activeProbeAt, activeAt)
	}
	var observationColumnExists bool
	if err := conn.QueryRow(ctx, `
SELECT EXISTS (
    SELECT 1
    FROM information_schema.columns
    WHERE table_schema = 'public'
      AND table_name = 'provider_accounts'
      AND column_name = 'last_request_observed_at'
)`).Scan(&observationColumnExists); err != nil {
		t.Fatalf("检查回滚列状态: %v", err)
	}
	if observationColumnExists {
		t.Fatal("0189 down 后 last_request_observed_at 仍存在")
	}

	if err := m.Migrate(189); err != nil {
		t.Fatalf("0189 回滚后重新上迁移失败: %v", err)
	}
	assertRequestObservationMigrationState(t, ctx, conn, passiveID, activeID, passiveAt, activeAt)
}

func seedRequestObservationMigrationRows(
	t *testing.T,
	ctx context.Context,
	conn *pgx.Conn,
	passiveAt time.Time,
	activeAt time.Time,
) (passiveID int64, activeID int64) {
	t.Helper()
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	var tenantID, providerID, poolGroupID, channelID int64
	if err := conn.QueryRow(ctx,
		`INSERT INTO tenants (name) VALUES ($1) RETURNING id`,
		"request-observation-"+suffix,
	).Scan(&tenantID); err != nil {
		t.Fatalf("插入租户: %v", err)
	}
	if err := conn.QueryRow(ctx,
		`INSERT INTO providers (tenant_id, code, display_name, upstream_protocol)
		 VALUES ($1, $2, 'Request Observation Provider', 'openai_chat') RETURNING id`,
		tenantID, "request-observation-"+suffix,
	).Scan(&providerID); err != nil {
		t.Fatalf("插入 provider: %v", err)
	}
	if err := conn.QueryRow(ctx,
		`INSERT INTO pool_groups (tenant_id, name) VALUES ($1, $2) RETURNING id`,
		tenantID, "request-observation-pool-"+suffix,
	).Scan(&poolGroupID); err != nil {
		t.Fatalf("插入 pool group: %v", err)
	}
	if err := conn.QueryRow(ctx,
		`INSERT INTO channels (tenant_id, pool_group_id, name) VALUES ($1, $2, $3) RETURNING id`,
		tenantID, poolGroupID, "request-observation-channel-"+suffix,
	).Scan(&channelID); err != nil {
		t.Fatalf("插入 channel: %v", err)
	}
	if err := conn.QueryRow(ctx,
		`INSERT INTO provider_accounts (
		    tenant_id, provider_id, channel_id, name, account_type, last_probe_at
		 ) VALUES ($1, $2, $3, $4, 'api_key', $5) RETURNING id`,
		tenantID, providerID, channelID, "legacy-passive-"+suffix, passiveAt,
	).Scan(&passiveID); err != nil {
		t.Fatalf("插入历史被动记录: %v", err)
	}
	if err := conn.QueryRow(ctx,
		`INSERT INTO provider_accounts (
		    tenant_id, provider_id, channel_id, name, account_type,
		    last_probe_at, last_probe_latency_ms
		 ) VALUES ($1, $2, $3, $4, 'api_key', $5, 87) RETURNING id`,
		tenantID, providerID, channelID, "active-probe-"+suffix, activeAt,
	).Scan(&activeID); err != nil {
		t.Fatalf("插入主动探测记录: %v", err)
	}
	return passiveID, activeID
}

func assertRequestObservationMigrationState(
	t *testing.T,
	ctx context.Context,
	conn *pgx.Conn,
	passiveID int64,
	activeID int64,
	passiveAt time.Time,
	activeAt time.Time,
) {
	t.Helper()
	var passiveProbeAt, passiveObservedAt, activeProbeAt, activeObservedAt *time.Time
	var activeLatency *int32
	if err := conn.QueryRow(ctx, `
SELECT
    (SELECT last_probe_at FROM provider_accounts WHERE id = $1),
    (SELECT last_request_observed_at FROM provider_accounts WHERE id = $1),
    (SELECT last_probe_at FROM provider_accounts WHERE id = $2),
    (SELECT last_request_observed_at FROM provider_accounts WHERE id = $2),
    (SELECT last_probe_latency_ms FROM provider_accounts WHERE id = $2)`,
		passiveID, activeID,
	).Scan(&passiveProbeAt, &passiveObservedAt, &activeProbeAt, &activeObservedAt, &activeLatency); err != nil {
		t.Fatalf("读取迁移后字段: %v", err)
	}
	if passiveProbeAt != nil {
		t.Fatalf("历史被动值仍占用 last_probe_at: %v", passiveProbeAt)
	}
	if passiveObservedAt == nil || !passiveObservedAt.Equal(passiveAt) {
		t.Fatalf("历史被动值未迁入新列: got %v want %v", passiveObservedAt, passiveAt)
	}
	if activeProbeAt == nil || !activeProbeAt.Equal(activeAt) {
		t.Fatalf("主动探测时间被迁移破坏: got %v want %v", activeProbeAt, activeAt)
	}
	if activeLatency == nil || *activeLatency != 87 {
		t.Fatalf("主动探测延迟被迁移破坏: %v", activeLatency)
	}
	if activeObservedAt != nil {
		t.Fatalf("带 latency 的主动探测记录不应被推断出普通请求观测: %v", activeObservedAt)
	}
}

func TestRefundFactMigrationsRefuseDestructiveRollback(t *testing.T) {
	baseDSN := os.Getenv("HUAKAI_DATABASE_URL")
	if baseDSN == "" {
		t.Skip("HUAKAI_DATABASE_URL not set; skipping integration_pg")
	}
	ctx := context.Background()

	t.Run("0193 保留退款操作事实", func(t *testing.T) {
		tmpDSN := createTemporaryMigrationDatabase(t, ctx, baseDSN, "huakai_refund_0193_guard")
		runner := newEmbeddedMigrationRunner(t, tmpDSN)
		if err := runner.Migrate(193); err != nil {
			t.Fatalf("迁移到 0193: %v", err)
		}
		conn, err := pgx.Connect(ctx, tmpDSN)
		if err != nil {
			t.Fatalf("连接 0193 临时库: %v", err)
		}
		defer conn.Close(ctx)
		seedBillingRefundOperationFact(t, ctx, conn)

		if _, err := conn.Exec(ctx, `UPDATE billing_refund_operations SET reason=reason`); err == nil {
			t.Fatal("退款操作事实意外允许 UPDATE")
		}
		if _, err := conn.Exec(ctx, `DELETE FROM billing_refund_operations`); err == nil {
			t.Fatal("退款操作事实意外允许 DELETE")
		}
		if err := runner.Steps(-1); err == nil {
			t.Fatal("0193 在存在退款操作事实时意外允许 down")
		}
		var count int64
		if err := conn.QueryRow(ctx, `SELECT count(*) FROM billing_refund_operations`).Scan(&count); err != nil {
			t.Fatalf("0193 拒绝回滚后读取退款事实: %v", err)
		}
		if count != 1 {
			t.Fatalf("0193 拒绝回滚后退款事实数=%d want 1", count)
		}
	})

	t.Run("0194 保留支付退款语义", func(t *testing.T) {
		tmpDSN := createTemporaryMigrationDatabase(t, ctx, baseDSN, "huakai_refund_0194_guard")
		runner := newEmbeddedMigrationRunner(t, tmpDSN)
		if err := runner.Migrate(194); err != nil {
			t.Fatalf("迁移到 0194: %v", err)
		}
		conn, err := pgx.Connect(ctx, tmpDSN)
		if err != nil {
			t.Fatalf("连接 0194 临时库: %v", err)
		}
		defer conn.Close(ctx)
		orderID := seedPartialPaymentRefundFact(t, ctx, conn)

		if err := runner.Steps(-1); err == nil {
			t.Fatal("0194 在存在支付退款事实时意外允许 down")
		}
		var status string
		var requestedAmount int64
		var requireExact bool
		if err := conn.QueryRow(ctx, `
SELECT orders.status, refunds.requested_amount_cents, refunds.require_exact
FROM payment_orders AS orders
JOIN payment_refunds AS refunds
  ON refunds.tenant_id = orders.tenant_id AND refunds.order_id = orders.id
WHERE orders.id=$1`, orderID).Scan(&status, &requestedAmount, &requireExact); err != nil {
			t.Fatalf("0194 拒绝回滚后读取部分退款: %v", err)
		}
		if status != "completed" || requestedAmount != 100 || requireExact {
			t.Fatalf("0194 拒绝回滚后状态=%q requested=%d exact=%v want completed/100/false", status, requestedAmount, requireExact)
		}
		if _, err := conn.Exec(ctx, `UPDATE payment_refunds SET reason=reason WHERE order_id=$1`, orderID); err == nil {
			t.Fatal("0194 拒绝回滚后 append-only 触发器未恢复")
		}
	})
}

func newEmbeddedMigrationRunner(t *testing.T, dsn string) *migrate.Migrate {
	t.Helper()
	src, err := iofs.New(sqlmigrations.Files, "migrations")
	if err != nil {
		t.Fatalf("构建迁移源: %v", err)
	}
	runner, err := migrate.NewWithSourceInstance("iofs", src, dsn)
	if err != nil {
		t.Fatalf("初始化迁移器: %v", err)
	}
	t.Cleanup(func() {
		sourceErr, databaseErr := runner.Close()
		if sourceErr != nil || databaseErr != nil {
			t.Logf("关闭迁移器: source=%v database=%v", sourceErr, databaseErr)
		}
	})
	return runner
}

func seedBillingRefundOperationFact(t *testing.T, ctx context.Context, conn *pgx.Conn) {
	t.Helper()
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	var tenantID, userID, apiKeyID, claimID int64
	if err := conn.QueryRow(ctx, `INSERT INTO tenants (name) VALUES ($1) RETURNING id`, "refund-operation-"+suffix).Scan(&tenantID); err != nil {
		t.Fatalf("插入 0193 租户: %v", err)
	}
	if err := conn.QueryRow(ctx, `INSERT INTO users (tenant_id, display_name) VALUES ($1, $2) RETURNING id`, tenantID, "refund-user-"+suffix).Scan(&userID); err != nil {
		t.Fatalf("插入 0193 用户: %v", err)
	}
	if err := conn.QueryRow(ctx, `
INSERT INTO api_keys (tenant_id, user_id, name, key_hash, key_prefix, status)
VALUES ($1, $2, $3, $4, $5, 'active') RETURNING id`, tenantID, userID,
		"refund-key-"+suffix, "$2a$10$refund-migration-test", "hk_refund_"+suffix).Scan(&apiKeyID); err != nil {
		t.Fatalf("插入 0193 API key: %v", err)
	}
	if err := conn.QueryRow(ctx, `
INSERT INTO billing_ledger_claims (
    tenant_id, idempotency_key, request_fingerprint, api_key_id, user_id,
    logical_request_id, endpoint_family, requested_model, billing_policy_version,
    request_class, attempt_seq, predicted_cost, currency_code, lease_expires_at
) VALUES ($1, $2, $3, $4, $5, $6, 'chat', 'test-model', '1.0', 'standard', 1, 0, 'USD', NOW() + interval '1 minute')
RETURNING id`, tenantID, "claim-"+suffix, "claim-fingerprint-"+suffix, apiKeyID, userID, "logical-"+suffix).Scan(&claimID); err != nil {
		t.Fatalf("插入 0193 claim: %v", err)
	}
	if _, err := conn.Exec(ctx, `
INSERT INTO billing_refund_operations (
    tenant_id, claim_id, idempotency_key, request_fingerprint,
    requested_amount_micro_usd, reason, require_exact,
    applied_amount_micro_usd, covered_amount_micro_usd, outcome
) VALUES ($1, $2, $3, $4, 0, 'migration guard', FALSE, 0, 0, 'skipped_zero')`,
		tenantID, claimID, "refund-operation-"+suffix, strings.Repeat("a", 64)); err != nil {
		t.Fatalf("插入 0193 退款操作事实: %v", err)
	}
}

func seedPartialPaymentRefundFact(t *testing.T, ctx context.Context, conn *pgx.Conn) int64 {
	t.Helper()
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	var tenantID, userID, orderID int64
	if err := conn.QueryRow(ctx, `INSERT INTO tenants (name) VALUES ($1) RETURNING id`, "payment-refund-"+suffix).Scan(&tenantID); err != nil {
		t.Fatalf("插入 0194 租户: %v", err)
	}
	if err := conn.QueryRow(ctx, `INSERT INTO users (tenant_id, display_name) VALUES ($1, $2) RETURNING id`, tenantID, "payment-user-"+suffix).Scan(&userID); err != nil {
		t.Fatalf("插入 0194 用户: %v", err)
	}
	if err := conn.QueryRow(ctx, `
INSERT INTO payment_orders (tenant_id, user_id, out_trade_no, amount_cents, currency_code, status, provider_kind)
VALUES ($1, $2, $3, 1000, 'USD', 'completed', 'manual') RETURNING id`, tenantID, userID, "payment-order-"+suffix).Scan(&orderID); err != nil {
		t.Fatalf("插入 0194 订单: %v", err)
	}
	if _, err := conn.Exec(ctx, `
INSERT INTO payment_credits (tenant_id, payment_order_id, user_id, amount_cents, currency_code)
VALUES ($1, $2, $3, 1000, 'USD')`, tenantID, orderID, userID); err != nil {
		t.Fatalf("插入 0194 入账事实: %v", err)
	}
	if _, err := conn.Exec(ctx, `
INSERT INTO payment_refunds (
    tenant_id, order_id, user_id, amount_cents, requested_amount_cents,
    require_exact, currency, idempotency_key, reason, actor_kind, actor_id
) VALUES ($1, $2, $3, 100, 100, FALSE, 'USD', $4, 'migration guard', 'admin', 7)`,
		tenantID, orderID, userID, "payment-refund-"+suffix); err != nil {
		t.Fatalf("插入 0194 部分退款事实: %v", err)
	}
	return orderID
}

func createTemporaryMigrationDatabase(t *testing.T, ctx context.Context, baseDSN, prefix string) string {
	t.Helper()
	tmpDB := fmt.Sprintf("%s_%d", prefix, time.Now().UnixNano())
	admin, err := pgx.Connect(ctx, baseDSN)
	if err != nil {
		t.Fatalf("连接维护库失败: %v", err)
	}
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+pgxQuoteIdent(tmpDB)); err != nil {
		_ = admin.Close(ctx)
		t.Fatalf("CREATE DATABASE %s 失败: %v", tmpDB, err)
	}
	if err := admin.Close(ctx); err != nil {
		t.Fatalf("关闭维护连接失败: %v", err)
	}
	t.Cleanup(func() {
		cleanup, cleanupErr := pgx.Connect(context.Background(), baseDSN)
		if cleanupErr != nil {
			t.Logf("清理连接失败(临时库 %s 可能残留): %v", tmpDB, cleanupErr)
			return
		}
		defer cleanup.Close(context.Background())
		_, _ = cleanup.Exec(context.Background(),
			"SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = $1", tmpDB)
		if _, dropErr := cleanup.Exec(context.Background(), "DROP DATABASE IF EXISTS "+pgxQuoteIdent(tmpDB)); dropErr != nil {
			t.Logf("DROP DATABASE %s 失败(需手动清理): %v", tmpDB, dropErr)
		}
	})
	return swapDatabaseName(t, baseDSN, tmpDB)
}

// swapDatabaseName 把 DSN 里的库名换成 newDB,保留其余连接参数。
func swapDatabaseName(t *testing.T, dsn, newDB string) string {
	t.Helper()
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatalf("解析 DSN 失败: %v", err)
	}
	u.Path = "/" + newDB
	return u.String()
}

// pgxQuoteIdent 给库名加双引号转义,防注入/特殊字符(库名由测试内生成,此处为防御性处理)。
func pgxQuoteIdent(ident string) string {
	return `"` + strings.ReplaceAll(ident, `"`, `""`) + `"`
}
