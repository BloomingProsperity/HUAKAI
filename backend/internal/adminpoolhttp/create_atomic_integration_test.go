//go:build integration_pg

package adminpoolhttp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
	"github.com/BloomingProsperity/HUAKAI/internal/db"
	admindb "github.com/BloomingProsperity/HUAKAI/internal/db/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/dbmigrate"
	sqlmigrations "github.com/BloomingProsperity/HUAKAI/sql"
)

func installAdminAuditRejectTrigger(t *testing.T, ctx context.Context, pool *pgxpool.Pool, name, rejectActorID string) {
	t.Helper()
	fnName := "audit_reject_" + name
	trigName := "trg_" + name
	if _, err := pool.Exec(ctx, `
		CREATE OR REPLACE FUNCTION `+fnName+`() RETURNS trigger AS $$
		BEGIN
			IF NEW.actor_id = '`+rejectActorID+`' THEN
				RAISE EXCEPTION 'admin_pool_account test reject actor_id %', NEW.actor_id;
			END IF;
			RETURN NEW;
		END;
		$$ LANGUAGE plpgsql`); err != nil {
		t.Fatalf("创建审计拒绝函数: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		DROP TRIGGER IF EXISTS `+trigName+` ON admin_audit_events;
		CREATE TRIGGER `+trigName+` BEFORE INSERT ON admin_audit_events
		FOR EACH ROW EXECUTE FUNCTION `+fnName+`()`); err != nil {
		t.Fatalf("创建审计拒绝触发器: %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx := context.Background()
		_, _ = pool.Exec(cleanupCtx, `DROP TRIGGER IF EXISTS `+trigName+` ON admin_audit_events`)
		_, _ = pool.Exec(cleanupCtx, `DROP FUNCTION IF EXISTS `+fnName+`()`)
	})
}

// 建号（账号插入 + 凭据写入 + 管理审计）合入单事务的真 PG 验证：审计写失败必须整体
// 回滚，杜绝残留悬空账号占用唯一名，导致同名重试撞 uq_provider_accounts_tenant_name。
//
// 判别 fixture：装 BEFORE INSERT trigger 拒收失败 actor 的 admin_audit_events 行
// （复用 installAdminAuditRejectTrigger）；账号与凭据可写、唯独审计写必失败。
// 变异自检：把 handler 的单事务拆回「账号先独立提交 + 凭据/审计另调」（缺陷结构）→
// 审计拒后账号已落 → 本用例断言 count=0 转红，且同名重试撞唯一约束不再 201。

func TestAdminPoolCreate_AuditFailureRollsBackAccountAndCredential(t *testing.T) {
	ctx := context.Background()
	pool := openAdminPoolCreatePGPool(t, ctx)
	suffix := uuid.NewString()
	tenantID, providerID, channelID := seedAdminPoolCreateFixture(t, ctx, pool, suffix)
	keys := mustAdminPoolCreateKeys(t)

	const failToken, retryToken = int64(990101), int64(990102)
	rejectActor := fmt.Sprintf("admin_token:%d", failToken)
	installAdminAuditRejectTrigger(t, ctx, pool, "poolacct_create_"+strings.ReplaceAll(suffix, "-", "_"), rejectActor)

	name := "atomic-" + suffix
	body := fmt.Sprintf(`{"provider_id":%d,"channel_id":%d,"name":%q,"account_type":"api_key","vendor":"openai","auth_mode":"api_key","credentials":{"api_key":"sk-%s"}}`,
		providerID, channelID, name, suffix)

	// actor=failToken 的建号审计被 trigger 拒 → 单事务应整体回滚，返回 503 audit_write_failed。
	failDeps := adminPoolCreateDeps(pool, keys, tenantID, failToken)
	rec := invokeAdminPoolWithDeps(t, failDeps, http.MethodPost, "/admin/v1/provider-accounts", body)
	if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), "audit_write_failed") {
		t.Fatalf("审计失败应回 503 audit_write_failed；实际 status=%d body=%s", rec.Code, rec.Body.String())
	}
	// 账号与凭据均不得残留（整体回滚）。
	if got := adminPoolCreateCount(t, ctx, pool,
		`SELECT count(*)::int FROM provider_accounts WHERE tenant_id=$1 AND name=$2 AND deleted_at IS NULL`, tenantID, name); got != 0 {
		t.Fatalf("审计失败后残留活体账号 count=%d，期望 0（整体回滚）", got)
	}
	if got := adminPoolCreateCount(t, ctx, pool,
		`SELECT count(*)::int FROM account_credentials WHERE tenant_id=$1`, tenantID); got != 0 {
		t.Fatalf("审计失败后残留凭据 count=%d，期望 0", got)
	}
	if got := adminPoolCreateCount(t, ctx, pool,
		`SELECT count(*)::int FROM channel_health_state WHERE tenant_id=$1`, tenantID); got != 0 {
		t.Fatalf("审计失败后残留健康状态 count=%d，期望 0", got)
	}

	// 同名立即重发（换一个不被拒的 actor）必须成功，证明回滚后唯一名可复用。
	retryDeps := adminPoolCreateDeps(pool, keys, tenantID, retryToken)
	rec2 := invokeAdminPoolWithDeps(t, retryDeps, http.MethodPost, "/admin/v1/provider-accounts", body)
	if rec2.Code != http.StatusCreated {
		t.Fatalf("回滚后同名重建应 201（不撞 uq_provider_accounts_tenant_name）；实际 status=%d body=%s", rec2.Code, rec2.Body.String())
	}
	var resp providerAccountResponse
	if err := json.Unmarshal(rec2.Body.Bytes(), &resp); err != nil {
		t.Fatalf("解析重建响应: %v", err)
	}
	if resp.ID <= 0 || resp.Name != name {
		t.Fatalf("重建响应异常: %+v", resp)
	}
	if got := adminPoolCreateCount(t, ctx, pool,
		`SELECT count(*)::int FROM provider_accounts WHERE tenant_id=$1 AND name=$2 AND deleted_at IS NULL`, tenantID, name); got != 1 {
		t.Fatalf("重建后账号 count=%d，期望 1", got)
	}
	if got := adminPoolCreateCount(t, ctx, pool,
		`SELECT count(*)::int FROM account_credentials WHERE tenant_id=$1 AND provider_account_id=$2 AND deleted_at IS NULL`, tenantID, resp.ID); got != 1 {
		t.Fatalf("重建后凭据 count=%d，期望 1", got)
	}
	// 建号审计只应在成功那次落库；失败那次因回滚不得留下（单事务）。
	if got := adminPoolCreateCount(t, ctx, pool,
		`SELECT count(*)::int FROM admin_audit_events WHERE tenant_id=$1 AND action='create_provider_account'`, tenantID); got != 1 {
		t.Fatalf("建号审计总数=%d，期望仅成功那次 1 条", got)
	}
}

// TestAdminPoolCreate_HappyPathWritesAdvancedFieldsAndAudit 守正向契约：成功建号返回
// 201 与完整 DTO 回显；高级字段经 accountadvanced.ApplyCreate 落库；凭据只进
// account_credentials（legacy provider_accounts.credentials 保持空对象）；建号审计负载
// 带 credential 元信息且不含明文凭据。
func TestAdminPoolCreate_HappyPathWritesAdvancedFieldsAndAudit(t *testing.T) {
	ctx := context.Background()
	pool := openAdminPoolCreatePGPool(t, ctx)
	suffix := uuid.NewString()
	tenantID, providerID, channelID := seedAdminPoolCreateFixture(t, ctx, pool, suffix)
	keys := mustAdminPoolCreateKeys(t)
	deps := adminPoolCreateDeps(pool, keys, tenantID, 990201)

	name := "advanced-" + suffix
	body := fmt.Sprintf(`{
		"provider_id":%d,"channel_id":%d,"name":%q,"account_type":"api_key","vendor":"openai","auth_mode":"api_key",
		"credentials":{"api_key":"sk-secret-%s"},
		"cap_concurrency":3,"priority":10,"static_weight":4,"probe_model":" gpt-probe ",
		"tags":[" prod ",""],"model_allow_list":[" gpt-4o "],"capability_flags":["chat"],
		"extra":{"azure_api_version":"2024-08-01"},
		"upstream_cost_ratio":0.5,"rpm_limit":0,"tpm_limit":1200,"window_cost_limit_cents":345,"max_sessions":6,
		"disable_cooling":true,"refresh_lead_seconds":90,"expires_at":"2025-01-02T03:04:05Z","tls_fingerprint_rotate":true,
		"custom_error_codes_enabled":true,"custom_error_codes":[429,529],"pool_mode":true,
		"temp_unschedulable_enabled":true,
		"temp_unschedulable_rules":[{"rule_id":"busy-529","error_code":529,"keywords":["busy"],"duration_minutes":5,"client_status":503,"client_code":"account_busy","message_mode":"custom","client_message":"账号暂不可用","affect_health":false}]
	}`, providerID, channelID, name, suffix)

	rec := invokeAdminPoolWithDeps(t, deps, http.MethodPost, "/admin/v1/provider-accounts", body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var resp providerAccountResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("解析响应: %v", err)
	}
	if resp.ID <= 0 || resp.Name != name || resp.AccountType != "api_key" {
		t.Fatalf("基础字段回显错误: %+v", resp)
	}
	if resp.CapConcurrency != 3 || resp.Priority != 10 || resp.StaticWeight != 4 {
		t.Fatalf("容量字段回显错误: %+v", resp)
	}
	if resp.RPMLimit != 0 || resp.TPMLimit != 1200 || resp.WindowCostLimitCents != 345 || resp.MaxSessions != 6 {
		t.Fatalf("限额字段回显错误: %+v", resp)
	}
	if resp.UpstreamCostRatio == nil || *resp.UpstreamCostRatio != 0.5 {
		t.Fatalf("成本比例回显错误: %+v", resp)
	}
	if !resp.DisableCooling || resp.RefreshLeadSeconds == nil || *resp.RefreshLeadSeconds != 90 || !resp.TLSFingerprintRotate {
		t.Fatalf("开关/刷新回显错误: %+v", resp)
	}
	if !resp.CustomErrorCodesEnabled || len(resp.CustomErrorCodes) != 2 || !resp.PoolMode || !resp.TempUnschedulableEnabled {
		t.Fatalf("错误策略/池模式回显错误: %+v", resp)
	}
	if resp.ExpiresAt == nil || !resp.ExpiresAt.Equal(time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC)) {
		t.Fatalf("expires_at 回显错误: %v", resp.ExpiresAt)
	}

	// 凭据只落 account_credentials；legacy provider_accounts.credentials 保持空对象。
	var legacy []byte
	if err := pool.QueryRow(ctx, `SELECT credentials FROM provider_accounts WHERE tenant_id=$1 AND id=$2`, tenantID, resp.ID).Scan(&legacy); err != nil {
		t.Fatal(err)
	}
	if string(legacy) != "{}" {
		t.Fatalf("legacy credentials=%s，期望空对象", legacy)
	}
	if got := adminPoolCreateCount(t, ctx, pool,
		`SELECT count(*)::int FROM account_credentials WHERE tenant_id=$1 AND provider_account_id=$2 AND deleted_at IS NULL`, tenantID, resp.ID); got != 1 {
		t.Fatalf("凭据 count=%d，期望 1", got)
	}
	if got := adminPoolCreateCount(t, ctx, pool,
		`SELECT count(*)::int FROM channel_health_state WHERE tenant_id=$1 AND provider_account_id=$2`, tenantID, resp.ID); got != 1 {
		t.Fatalf("健康初始状态 count=%d，期望与账号同事务落 1 条", got)
	}

	// 建号审计负载含 credential 元信息、不含明文密钥。
	var payload []byte
	if err := pool.QueryRow(ctx,
		`SELECT payload FROM admin_audit_events WHERE tenant_id=$1 AND action='create_provider_account' AND target_id=$2`,
		tenantID, resp.ID).Scan(&payload); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(payload), "sk-secret") {
		t.Fatalf("审计负载泄漏明文凭据: %s", payload)
	}
	var auditPayload map[string]any
	if err := json.Unmarshal(payload, &auditPayload); err != nil {
		t.Fatalf("解析审计负载: %v", err)
	}
	if auditPayload["credentials_present"] != true {
		t.Fatalf("审计负载 credentials_present 非 true: %s", payload)
	}
	if credID, ok := auditPayload["credential_id"].(float64); !ok || credID <= 0 {
		t.Fatalf("审计负载缺有效 credential_id: %s", payload)
	}
}

// openAdminPoolCreatePGPool 新建并全量迁移一个临时库，保证 provider_accounts 等表带最新
// 列（本地共享 huakai 库可能陈旧），且与其它测试隔离。
func openAdminPoolCreatePGPool(t *testing.T, ctx context.Context) *pgxpool.Pool {
	t.Helper()
	dsn := strings.TrimSpace(os.Getenv("HUAKAI_TEST_DATABASE_URL"))
	if dsn == "" {
		dsn = strings.TrimSpace(os.Getenv("HUAKAI_DATABASE_URL"))
	}
	if dsn == "" {
		t.Skip("未设置 HUAKAI_TEST_DATABASE_URL/HUAKAI_DATABASE_URL，跳过 integration_pg")
	}
	adminConn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer adminConn.Close(ctx)
	databaseName := "huakai_poolacct_create_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err := adminConn.Exec(ctx, "CREATE DATABASE "+adminPoolCreateQuoteIdent(databaseName)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanup, err := pgx.Connect(context.Background(), dsn)
		if err != nil {
			t.Logf("连接维护库清理临时库失败：%v", err)
			return
		}
		defer cleanup.Close(context.Background())
		_, _ = cleanup.Exec(context.Background(),
			"SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname=$1", databaseName)
		if _, err := cleanup.Exec(context.Background(), "DROP DATABASE IF EXISTS "+adminPoolCreateQuoteIdent(databaseName)); err != nil {
			t.Logf("删除临时库失败：%v", err)
		}
	})
	testDSN := adminPoolCreateReplaceDBName(t, dsn, databaseName)
	if err := dbmigrate.Up(sqlmigrations.Files, testDSN); err != nil {
		t.Fatalf("迁移临时库失败：%v", err)
	}
	pool, err := db.Open(ctx, db.PoolConfig{DSN: testDSN})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func adminPoolCreateReplaceDBName(t *testing.T, dsn, databaseName string) string {
	t.Helper()
	parsed, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	parsed.Path = "/" + databaseName
	return parsed.String()
}

func adminPoolCreateQuoteIdent(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
}

func seedAdminPoolCreateFixture(t *testing.T, ctx context.Context, pool *pgxpool.Pool, suffix string) (int64, int64, int64) {
	t.Helper()
	var tenantID, providerID, poolGroupID, channelID int64
	if err := pool.QueryRow(ctx, `INSERT INTO tenants (name) VALUES ($1) RETURNING id`, "poolacct-create-"+suffix).Scan(&tenantID); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	t.Cleanup(func() {
		c := context.Background()
		for _, q := range []string{
			`DELETE FROM credential_audit_events WHERE tenant_id=$1`,
			`DELETE FROM admin_audit_events WHERE tenant_id=$1`,
			`DELETE FROM account_credentials WHERE tenant_id=$1`,
			`DELETE FROM provider_accounts WHERE tenant_id=$1`,
			`DELETE FROM channels WHERE tenant_id=$1`,
			`DELETE FROM pool_groups WHERE tenant_id=$1`,
			`DELETE FROM providers WHERE tenant_id=$1`,
			`DELETE FROM tenants WHERE id=$1`,
		} {
			_, _ = pool.Exec(c, q, tenantID)
		}
	})
	if err := pool.QueryRow(ctx,
		`INSERT INTO providers (tenant_id, code, display_name, upstream_protocol) VALUES ($1,$2,$3,'openai_chat') RETURNING id`,
		tenantID, "provider-"+suffix, "Provider "+suffix).Scan(&providerID); err != nil {
		t.Fatalf("seed provider: %v", err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO pool_groups (tenant_id, name) VALUES ($1,$2) RETURNING id`, tenantID, "pool-"+suffix).Scan(&poolGroupID); err != nil {
		t.Fatalf("seed pool_group: %v", err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO channels (tenant_id, pool_group_id, name) VALUES ($1,$2,$3) RETURNING id`, tenantID, poolGroupID, "channel-"+suffix).Scan(&channelID); err != nil {
		t.Fatalf("seed channel: %v", err)
	}
	return tenantID, providerID, channelID
}

func adminPoolCreateDeps(pool *pgxpool.Pool, keys credentialstore.KeyProvider, tenantID, tokenID int64) AdminPoolAccountDeps {
	return AdminPoolAccountDeps{
		Auth:         adminPoolAuthStub{ident: admin.AdminIdentity{TokenID: tokenID, Role: admin.RoleTenantOperator, ScopeTenantID: tenantID}},
		Store:        NewAdminPoolAccountStoreAdapter(admindb.New(pool), pool),
		Credentials:  credentialstore.NewStore(pool, keys, credentialstore.DefaultHandlerRegistry()),
		Capabilities: allowAdminPoolCapability{},
	}
}

func mustAdminPoolCreateKeys(t *testing.T) credentialstore.KeyProvider {
	t.Helper()
	keys, err := credentialstore.NewStaticKeyProvider("admin-pool-create-test", bytes.Repeat([]byte{9}, 32))
	if err != nil {
		t.Fatalf("key provider: %v", err)
	}
	return keys
}

func adminPoolCreateCount(t *testing.T, ctx context.Context, pool *pgxpool.Pool, query string, args ...any) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(ctx, query, args...).Scan(&n); err != nil {
		t.Fatalf("count 查询 %q: %v", query, err)
	}
	return n
}
