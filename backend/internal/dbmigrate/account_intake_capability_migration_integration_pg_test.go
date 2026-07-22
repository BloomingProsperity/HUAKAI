//go:build integration_pg

package dbmigrate_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/golang-migrate/migrate/v4"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialacq/intake"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
	"github.com/BloomingProsperity/HUAKAI/internal/gatewayhttp/accountintake"
	"github.com/BloomingProsperity/HUAKAI/internal/tenantcapability"
)

func TestAccountIntakeCapabilityMigrationsAndStore(t *testing.T) {
	baseDSN := os.Getenv("HUAKAI_DATABASE_URL")
	if baseDSN == "" {
		t.Skip("HUAKAI_DATABASE_URL 未设置，跳过 integration_pg")
	}
	ctx := context.Background()

	t.Run("旧版明文授权流程升级后明确失败", func(t *testing.T) {
		dsn := createTemporaryMigrationDatabase(t, ctx, baseDSN, "huakai_legacy_auth_payload")
		runner := newEmbeddedMigrationRunner(t, dsn)
		if err := runner.Migrate(198); err != nil {
			t.Fatalf("迁移到 0198: %v", err)
		}
		conn, err := pgx.Connect(ctx, dsn)
		if err != nil {
			t.Fatalf("连接临时库: %v", err)
		}
		defer conn.Close(ctx)
		flowID := seedLegacyCredentialFlow(t, ctx, conn)
		if err := runner.Migrate(199); err != nil {
			t.Fatalf("升级到 0199: %v", err)
		}
		var status, errorClass, message, payload string
		var encrypted, nonce []byte
		if err := conn.QueryRow(ctx, `
SELECT status, error_class, error_message_redacted, device_code_payload::text,
       encrypted_pkce_verifier, nonce_hash
FROM credential_acquisition_flow_sessions
WHERE id=$1::uuid`, flowID).Scan(&status, &errorClass, &message, &payload, &encrypted, &nonce); err != nil {
			t.Fatalf("读取升级后流程: %v", err)
		}
		if status != "failed" || errorClass != "legacy_plaintext_auth_payload_removed" ||
			!strings.Contains(message, "重新发起") || payload != "{}" || encrypted != nil || nonce != nil {
			t.Fatalf("升级后流程状态不完整: status=%q class=%q message=%q payload=%q encrypted=%d nonce=%d",
				status, errorClass, message, payload, len(encrypted), len(nonce))
		}
	})

	t.Run("模式约束与空表迁移可往返", func(t *testing.T) {
		dsn := createTemporaryMigrationDatabase(t, ctx, baseDSN, "huakai_account_intake_roundtrip")
		runner := newEmbeddedMigrationRunner(t, dsn)
		if err := runner.Migrate(203); err != nil {
			t.Fatalf("迁移到 0203: %v", err)
		}
		conn, err := pgx.Connect(ctx, dsn)
		if err != nil {
			t.Fatalf("连接临时库: %v", err)
		}
		defer conn.Close(ctx)
		assertCredentialModeInsertCode(t, ctx, conn, "23514")
		if err := runner.Migrate(204); err != nil {
			t.Fatalf("升级到 0204: %v", err)
		}
		assertCredentialModeInsertCode(t, ctx, conn, "23503")
		if err := runner.Migrate(205); err != nil {
			t.Fatalf("升级到 0205: %v", err)
		}
		assertMigrationTablePresence(t, ctx, conn, "tenant_admin_capability_grants", true)
		if err := runner.Migrate(206); err != nil {
			t.Fatalf("升级到 0206: %v", err)
		}
		assertMigrationTablePresence(t, ctx, conn, "account_intake_staged_credentials", true)
		if err := runner.Migrate(207); err != nil {
			t.Fatalf("升级到 0207: %v", err)
		}
		assertAuditConstraintContains(t, ctx, conn, "admin_audit_events_action_check", "account_bundle_exported", true)
		assertAuditConstraintContains(t, ctx, conn, "admin_audit_events_target_type_check", "account_bundle", true)
		if err := runner.Steps(-1); err != nil {
			t.Fatalf("账号迁移包日志约束回退 0207: %v", err)
		}
		assertAuditConstraintContains(t, ctx, conn, "admin_audit_events_action_check", "account_bundle_exported", false)
		assertAuditConstraintContains(t, ctx, conn, "admin_audit_events_target_type_check", "account_bundle", false)
		if err := runner.Steps(-1); err != nil {
			t.Fatalf("短期凭据空表回退 0206: %v", err)
		}
		assertMigrationTablePresence(t, ctx, conn, "account_intake_staged_credentials", false)
		if err := runner.Steps(-1); err != nil {
			t.Fatalf("空授权表回退 0205: %v", err)
		}
		assertMigrationTablePresence(t, ctx, conn, "tenant_admin_capability_grants", false)
		if err := runner.Steps(-1); err != nil {
			t.Fatalf("无 Agent Identity 行时回退 0204: %v", err)
		}
		assertCredentialModeInsertCode(t, ctx, conn, "23514")
	})

	t.Run("授权默认关闭且变更与日志原子落库", func(t *testing.T) {
		dsn := createTemporaryMigrationDatabase(t, ctx, baseDSN, "huakai_tenant_capability_store")
		runner := newEmbeddedMigrationRunner(t, dsn)
		if err := runner.Migrate(205); err != nil {
			t.Fatalf("迁移到 0205: %v", err)
		}
		pool, err := pgxpool.New(ctx, dsn)
		if err != nil {
			t.Fatalf("连接临时库: %v", err)
		}
		defer pool.Close()
		var tenantID int64
		if err := pool.QueryRow(ctx, `INSERT INTO tenants (name) VALUES ($1) RETURNING id`, "capability-"+fmt.Sprint(time.Now().UnixNano())).Scan(&tenantID); err != nil {
			t.Fatalf("创建租户: %v", err)
		}
		store := tenantcapability.NewStore(pool)
		allowed, err := store.Allowed(ctx, tenantID, tenantcapability.AdvancedAccountIntake)
		if err != nil || allowed {
			t.Fatalf("缺失授权必须默认关闭：allowed=%v err=%v", allowed, err)
		}
		list, err := store.List(ctx, tenantID)
		if err != nil {
			t.Fatalf("默认投影不准确：list=%+v err=%v", list, err)
		}
		foundAdvanced := false
		for _, grant := range list {
			if grant.Configured || grant.Enabled || grant.UpdatedAt != nil {
				t.Fatalf("未授权能力必须默认关闭：grant=%+v", grant)
			}
			if grant.Capability == tenantcapability.AdvancedAccountIntake {
				foundAdvanced = true
			}
		}
		if !foundAdvanced {
			t.Fatalf("默认投影缺少 %s：list=%+v", tenantcapability.AdvancedAccountIntake, list)
		}

		input := tenantcapability.SetInput{
			TenantID: tenantID, Capability: tenantcapability.AdvancedAccountIntake, Enabled: true,
			Actor: "admin_token:9", ActorRole: "platform_admin", Reason: "部署者授权账号接入", RequestID: "req-capability-1",
		}
		result, err := store.Set(ctx, input)
		if err != nil || !result.Changed || !result.Grant.Enabled || !result.Grant.Configured || result.Grant.UpdatedAt == nil {
			t.Fatalf("授予失败：result=%+v err=%v", result, err)
		}
		if allowed, err = store.Allowed(ctx, tenantID, tenantcapability.AdvancedAccountIntake); err != nil || !allowed {
			t.Fatalf("授予后未放行：allowed=%v err=%v", allowed, err)
		}
		if again, err := store.Set(ctx, input); err != nil || again.Changed {
			t.Fatalf("同状态重放必须幂等：result=%+v err=%v", again, err)
		}
		assertCapabilityAuditCount(t, ctx, pool, tenantID, 1)

		input.Enabled = false
		input.Reason = "部署者撤销账号接入"
		input.RequestID = "req-capability-2"
		result, err = store.Set(ctx, input)
		if err != nil || !result.Changed || result.Grant.Enabled || !result.Grant.Configured || result.Grant.RevokedAt == nil {
			t.Fatalf("撤销失败：result=%+v err=%v", result, err)
		}
		if allowed, err = store.Allowed(ctx, tenantID, tenantcapability.AdvancedAccountIntake); err != nil || allowed {
			t.Fatalf("撤销后仍放行：allowed=%v err=%v", allowed, err)
		}
		assertCapabilityAuditCount(t, ctx, pool, tenantID, 2)
	})

	t.Run("存在授权事实时拒绝回退", func(t *testing.T) {
		dsn := createTemporaryMigrationDatabase(t, ctx, baseDSN, "huakai_tenant_capability_guard")
		runner := newEmbeddedMigrationRunner(t, dsn)
		if err := runner.Migrate(205); err != nil {
			t.Fatalf("迁移到 0205: %v", err)
		}
		conn, err := pgx.Connect(ctx, dsn)
		if err != nil {
			t.Fatalf("连接临时库: %v", err)
		}
		defer conn.Close(ctx)
		var tenantID int64
		if err := conn.QueryRow(ctx, `INSERT INTO tenants (name) VALUES ($1) RETURNING id`, "capability-guard-"+fmt.Sprint(time.Now().UnixNano())).Scan(&tenantID); err != nil {
			t.Fatalf("创建租户: %v", err)
		}
		if _, err := conn.Exec(ctx, `
INSERT INTO tenant_admin_capability_grants
    (tenant_id, capability, enabled, updated_by, reason, granted_at)
VALUES ($1, 'advanced_account_intake', true, 'admin_token:9', '回退保护', clock_timestamp())`, tenantID); err != nil {
			t.Fatalf("写入授权事实: %v", err)
		}
		if err := runner.Steps(-1); err == nil {
			t.Fatal("存在授权事实时回退 0205 竟然成功")
		}
		assertMigrationTablePresence(t, ctx, conn, "tenant_admin_capability_grants", true)
	})

	t.Run("Cookie转换材料密文暂存且领取即销毁", func(t *testing.T) {
		dsn := createTemporaryMigrationDatabase(t, ctx, baseDSN, "huakai_cookie_staging")
		runner := newEmbeddedMigrationRunner(t, dsn)
		if err := runner.Migrate(206); err != nil {
			t.Fatalf("迁移到 0206: %v", err)
		}
		if err := runner.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
			t.Fatalf("从 0206 升级到当前 schema: %v", err)
		}
		pool, err := pgxpool.New(ctx, dsn)
		if err != nil {
			t.Fatalf("连接临时库: %v", err)
		}
		defer pool.Close()
		var tenantID int64
		if err := pool.QueryRow(ctx, `INSERT INTO tenants (name) VALUES ($1) RETURNING id`, "cookie-stage-"+fmt.Sprint(time.Now().UnixNano())).Scan(&tenantID); err != nil {
			t.Fatalf("创建租户: %v", err)
		}
		keys, err := credentialstore.NewStaticKeyProvider("cookie-stage-v1", []byte("0123456789abcdef0123456789abcdef"))
		if err != nil {
			t.Fatalf("创建测试密钥: %v", err)
		}
		store := accountintake.NewStagedStore(pool, keys)
		secret := `{"access_token":"access-secret","refresh_token":"refresh-secret"}`
		planHash := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
		stageInput := accountintake.StageInput{
			TenantID: tenantID, ActorID: "admin_token:9", ActorRole: "tenant_operator",
			SourceKind: "claude_cookie", Vendor: credentialstore.VendorAnthropic,
			AuthMode: credentialstore.AuthModeClaudeAIOAuth, PlanHash: planHash, Content: secret,
			PlanInput: accountintake.PlanInput{
				TenantID: tenantID, SourceKind: intake.SourceJSON,
				DefaultVendor: credentialstore.VendorAnthropic, DefaultAuthMode: credentialstore.AuthModeClaudeAIOAuth,
				Account: accountintake.AccountDefaults{ProviderID: 1, ChannelID: 1, NamePrefix: "claude", AccountType: "oauth"},
				Now:     time.Now().UTC(),
			},
			RequestID: "req-cookie-stage", Reason: "Cookie自动导入",
		}
		staged, err := store.Stage(ctx, stageInput)
		if err != nil {
			t.Fatalf("暂存转换材料: %v", err)
		}
		var encrypted, planRaw []byte
		var status string
		if err := pool.QueryRow(ctx, `SELECT encrypted_content, plan_input, status FROM account_intake_staged_credentials WHERE id=$1::uuid`, staged.ID).Scan(&encrypted, &planRaw, &status); err != nil {
			t.Fatalf("读取暂存行: %v", err)
		}
		if status != "staged" || len(encrypted) == 0 || strings.Contains(string(encrypted), "access-secret") || strings.Contains(string(planRaw), "access-secret") {
			t.Fatalf("暂存秘密未被隔离：status=%s encrypted=%d plan=%s", status, len(encrypted), string(planRaw))
		}
		if _, err := store.Claim(ctx, tenantID, "admin_token:10", staged.ID, planHash); !errors.Is(err, accountintake.ErrStagedCredentialNotFound) {
			t.Fatalf("其他操作者领取 err=%v", err)
		}
		if _, err := store.Claim(ctx, tenantID, "admin_token:9", staged.ID, strings.Repeat("b", 64)); !errors.Is(err, accountintake.ErrPlanChanged) {
			t.Fatalf("错误计划哈希领取 err=%v", err)
		}
		claimed, err := store.Claim(ctx, tenantID, "admin_token:9", staged.ID, planHash)
		if err != nil || claimed.PlanInput.Content != secret {
			t.Fatalf("正确领取 content=%q err=%v", claimed.PlanInput.Content, err)
		}
		var encryptedAfter []byte
		if err := pool.QueryRow(ctx, `SELECT encrypted_content, status FROM account_intake_staged_credentials WHERE id=$1::uuid`, staged.ID).Scan(&encryptedAfter, &status); err != nil {
			t.Fatalf("读取领取后状态: %v", err)
		}
		if status != "claimed" || encryptedAfter != nil {
			t.Fatalf("领取后密文未销毁：status=%s encrypted=%d", status, len(encryptedAfter))
		}
		if _, err := store.Claim(ctx, tenantID, "admin_token:9", staged.ID, planHash); !errors.Is(err, accountintake.ErrStagedCredentialReplay) {
			t.Fatalf("重复领取 err=%v", err)
		}
		if err := store.Finish(ctx, tenantID, "admin_token:9", "tenant_operator", staged.ID, "req-cookie-finish", "完成导入", true, accountintake.ExecutionSummary{Created: 1}); err != nil {
			t.Fatalf("完成短期流程: %v", err)
		}
		var auditCount int
		if err := pool.QueryRow(ctx, `
SELECT count(*) FROM admin_audit_events
WHERE tenant_id=$1 AND actor_id='admin_token:9'
  AND action IN ('credential_acquisition_started','credential_acquisition_completed')
  AND payload->>'flow_id'=$2`, tenantID, staged.ID).Scan(&auditCount); err != nil || auditCount != 2 {
			t.Fatalf("流程日志 count=%d err=%v", auditCount, err)
		}

		expired, err := store.Stage(ctx, stageInput)
		if err != nil {
			t.Fatalf("创建过期样本: %v", err)
		}
		if _, err := pool.Exec(ctx, `UPDATE account_intake_staged_credentials SET expires_at=clock_timestamp()-interval '1 second' WHERE id=$1::uuid`, expired.ID); err != nil {
			t.Fatalf("设置过期样本: %v", err)
		}
		if err := store.Cleanup(ctx); err != nil {
			t.Fatalf("独立清理过期样本: %v", err)
		}
		if err := pool.QueryRow(ctx, `SELECT encrypted_content, status FROM account_intake_staged_credentials WHERE id=$1::uuid`, expired.ID).Scan(&encryptedAfter, &status); err != nil {
			t.Fatalf("读取过期后状态: %v", err)
		}
		if status != "expired" || encryptedAfter != nil {
			t.Fatalf("过期后密文未销毁：status=%s encrypted=%d", status, len(encryptedAfter))
		}

		abandoned, err := store.Stage(ctx, stageInput)
		if err != nil {
			t.Fatalf("创建中断样本: %v", err)
		}
		if _, err := store.Claim(ctx, tenantID, "admin_token:9", abandoned.ID, planHash); err != nil {
			t.Fatalf("领取中断样本: %v", err)
		}
		if _, err := pool.Exec(ctx, `UPDATE account_intake_staged_credentials SET claimed_at=clock_timestamp()-interval '2 hours' WHERE id=$1::uuid`, abandoned.ID); err != nil {
			t.Fatalf("设置中断样本: %v", err)
		}
		if err := store.Cleanup(ctx); err != nil {
			t.Fatalf("独立清理中断流程: %v", err)
		}
		if err := pool.QueryRow(ctx, `SELECT status FROM account_intake_staged_credentials WHERE id=$1::uuid`, abandoned.ID).Scan(&status); err != nil || status != "failed" {
			t.Fatalf("中断流程状态=%s err=%v", status, err)
		}
		if err := pool.QueryRow(ctx, `
SELECT count(*) FROM admin_audit_events
WHERE tenant_id=$1 AND actor_id='admin_token:9' AND action='credential_acquisition_failed'
  AND payload->>'flow_id'=$2 AND payload->>'recovery_state'='operator_required'`, tenantID, abandoned.ID).Scan(&auditCount); err != nil || auditCount != 1 {
			t.Fatalf("中断恢复日志 count=%d err=%v", auditCount, err)
		}
	})

	t.Run("存在 Agent Identity 凭据时拒绝回退", func(t *testing.T) {
		dsn := createTemporaryMigrationDatabase(t, ctx, baseDSN, "huakai_agent_identity_guard")
		runner := newEmbeddedMigrationRunner(t, dsn)
		if err := runner.Migrate(204); err != nil {
			t.Fatalf("迁移到 0204: %v", err)
		}
		conn, err := pgx.Connect(ctx, dsn)
		if err != nil {
			t.Fatalf("连接临时库: %v", err)
		}
		defer conn.Close(ctx)
		seedAgentIdentityCredential(t, ctx, conn)
		if err := runner.Steps(-1); err == nil {
			t.Fatal("存在 Agent Identity 凭据时回退 0204 竟然成功")
		}
		var definition string
		if err := conn.QueryRow(ctx, `SELECT pg_get_constraintdef(oid) FROM pg_constraint WHERE conname='account_credentials_vendor_mode_check'`).Scan(&definition); err != nil {
			t.Fatalf("读取回退失败后的约束: %v", err)
		}
		if definition == "" {
			t.Fatal("回退失败后 vendor-mode 约束丢失")
		}
	})

	t.Run("存在账号迁移包日志时明确拒绝回退", func(t *testing.T) {
		dsn := createTemporaryMigrationDatabase(t, ctx, baseDSN, "huakai_account_bundle_audit_guard")
		runner := newEmbeddedMigrationRunner(t, dsn)
		if err := runner.Migrate(207); err != nil {
			t.Fatalf("迁移到 0207: %v", err)
		}
		conn, err := pgx.Connect(ctx, dsn)
		if err != nil {
			t.Fatalf("连接临时库: %v", err)
		}
		defer conn.Close(ctx)
		if _, err := conn.Exec(ctx, `
INSERT INTO admin_audit_events (actor_id, actor_role, action, target_type)
VALUES ('admin_token:bundle-guard', 'platform_admin', 'account_bundle_exported', 'account_bundle')`); err != nil {
			t.Fatalf("写入迁移包日志: %v", err)
		}
		if err := runner.Steps(-1); err == nil || !strings.Contains(err.Error(), "仍存在账号迁移包日志") {
			t.Fatalf("存在迁移包日志时应明确拒绝回退，err=%v", err)
		}
		assertAuditConstraintContains(t, ctx, conn, "admin_audit_events_action_check", "account_bundle_exported", true)
		var count int
		if err := conn.QueryRow(ctx, `SELECT count(*) FROM admin_audit_events WHERE action='account_bundle_exported'`).Scan(&count); err != nil || count != 1 {
			t.Fatalf("回退失败后迁移包日志 count=%d err=%v", count, err)
		}
	})
}

func seedLegacyCredentialFlow(t *testing.T, ctx context.Context, conn *pgx.Conn) string {
	t.Helper()
	suffix := fmt.Sprint(time.Now().UnixNano())
	var tenantID, providerID, poolGroupID, channelID, accountID int64
	if err := conn.QueryRow(ctx, `INSERT INTO tenants (name) VALUES ($1) RETURNING id`, "legacy-flow-"+suffix).Scan(&tenantID); err != nil {
		t.Fatalf("创建旧流程租户: %v", err)
	}
	if err := conn.QueryRow(ctx, `INSERT INTO pool_groups (tenant_id, name) VALUES ($1, $2) RETURNING id`, tenantID, "legacy-pool-"+suffix).Scan(&poolGroupID); err != nil {
		t.Fatalf("创建旧流程账号池: %v", err)
	}
	if err := conn.QueryRow(ctx, `INSERT INTO channels (tenant_id, pool_group_id, name) VALUES ($1, $2, $3) RETURNING id`, tenantID, poolGroupID, "legacy-channel-"+suffix).Scan(&channelID); err != nil {
		t.Fatalf("创建旧流程渠道: %v", err)
	}
	if err := conn.QueryRow(ctx, `
INSERT INTO providers (tenant_id, code, display_name, upstream_protocol)
VALUES ($1, $2, $3, 'openai_chat') RETURNING id`, tenantID, "legacy-provider-"+suffix, "旧流程 Provider").Scan(&providerID); err != nil {
		t.Fatalf("创建旧流程 provider: %v", err)
	}
	if err := conn.QueryRow(ctx, `
INSERT INTO provider_accounts (tenant_id, provider_id, channel_id, name, account_type)
VALUES ($1, $2, $3, $4, 'oauth') RETURNING id`, tenantID, providerID, channelID, "legacy-account-"+suffix).Scan(&accountID); err != nil {
		t.Fatalf("创建旧流程账号: %v", err)
	}
	flowID := uuid.NewString()
	if _, err := conn.Exec(ctx, `
INSERT INTO credential_acquisition_flow_sessions (
    id, tenant_id, provider_account_id, vendor, auth_mode, flow_kind, status,
    actor_id, actor_role, client_identity_source, requested_scopes, redacted_context,
    idempotency_key_hash, expires_at, auth_type, device_code_payload,
    encrypted_pkce_verifier, nonce_hash
) VALUES (
    $1::uuid, $2, $3, 'openai', 'codex_cli_oauth', 'oauth', 'waiting_for_user',
    'admin_token:legacy', 'tenant_operator', 'public_cli_client', '[]'::jsonb, '{}'::jsonb,
    decode(repeat('ab', 32), 'hex'), clock_timestamp() + interval '10 minutes', 'device_code',
    '{"device_code":"legacy-secret"}'::jsonb, decode('0102', 'hex'), decode('0304', 'hex')
)`, flowID, tenantID, accountID); err != nil {
		t.Fatalf("创建旧版明文授权流程: %v", err)
	}
	return flowID
}

func assertAuditConstraintContains(t *testing.T, ctx context.Context, conn *pgx.Conn, constraintName, token string, want bool) {
	t.Helper()
	var definition string
	if err := conn.QueryRow(ctx, `
SELECT pg_get_constraintdef(oid)
FROM pg_constraint
WHERE conrelid = 'admin_audit_events'::regclass AND conname = $1`, constraintName).Scan(&definition); err != nil {
		t.Fatalf("读取日志约束 %s: %v", constraintName, err)
	}
	if got := strings.Contains(definition, token); got != want {
		t.Fatalf("日志约束 %s 包含 %q=%v，期望 %v：%s", constraintName, token, got, want, definition)
	}
}

func assertCredentialModeInsertCode(t *testing.T, ctx context.Context, conn *pgx.Conn, want string) {
	t.Helper()
	tx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatalf("开始模式约束事务: %v", err)
	}
	defer tx.Rollback(ctx)
	_, err = tx.Exec(ctx, `
INSERT INTO account_credentials
    (tenant_id, provider_account_id, vendor, auth_mode, encrypted_payload, key_id, nonce, aad_hash)
VALUES (-1, -1, 'openai', 'codex_agent_identity', '\x00'::bytea, 'test-key', '\x00'::bytea, 'test-aad')`)
	if err == nil {
		t.Fatal("无效外键的测试凭据竟然写入成功")
	}
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != want {
		t.Fatalf("模式约束错误码=%v want=%s，err=%v", pgErr, want, err)
	}
}

func assertCapabilityAuditCount(t *testing.T, ctx context.Context, pool *pgxpool.Pool, tenantID int64, want int) {
	t.Helper()
	var count int
	err := pool.QueryRow(ctx, `
SELECT count(*)
FROM admin_audit_events
WHERE tenant_id IS NULL
  AND target_type = 'tenant'
  AND target_id = $1
  AND action IN ('grant_tenant_capability', 'revoke_tenant_capability')
  AND log_category = 'security'`, tenantID).Scan(&count)
	if err != nil || count != want {
		t.Fatalf("能力变更日志 count=%d want=%d err=%v", count, want, err)
	}
}

func seedAgentIdentityCredential(t *testing.T, ctx context.Context, conn *pgx.Conn) {
	t.Helper()
	suffix := fmt.Sprint(time.Now().UnixNano())
	var tenantID, providerID, poolGroupID, channelID, accountID int64
	if err := conn.QueryRow(ctx, `INSERT INTO tenants (name) VALUES ($1) RETURNING id`, "agent-guard-"+suffix).Scan(&tenantID); err != nil {
		t.Fatalf("创建租户: %v", err)
	}
	if err := conn.QueryRow(ctx, `
INSERT INTO providers (tenant_id, code, display_name, upstream_protocol)
VALUES ($1, $2, $3, 'openai_chat') RETURNING id`, tenantID, "agent-guard-"+suffix, "Agent Guard").Scan(&providerID); err != nil {
		t.Fatalf("创建 provider: %v", err)
	}
	if err := conn.QueryRow(ctx, `INSERT INTO pool_groups (tenant_id, name) VALUES ($1, $2) RETURNING id`, tenantID, "agent-pool-"+suffix).Scan(&poolGroupID); err != nil {
		t.Fatalf("创建账号池: %v", err)
	}
	if err := conn.QueryRow(ctx, `INSERT INTO channels (tenant_id, pool_group_id, name) VALUES ($1, $2, $3) RETURNING id`, tenantID, poolGroupID, "agent-channel-"+suffix).Scan(&channelID); err != nil {
		t.Fatalf("创建渠道: %v", err)
	}
	if err := conn.QueryRow(ctx, `
INSERT INTO provider_accounts (tenant_id, provider_id, channel_id, name, account_type)
VALUES ($1, $2, $3, $4, 'session') RETURNING id`, tenantID, providerID, channelID, "agent-account-"+suffix).Scan(&accountID); err != nil {
		t.Fatalf("创建账号: %v", err)
	}
	if _, err := conn.Exec(ctx, `
INSERT INTO account_credentials
    (tenant_id, provider_account_id, vendor, auth_mode, encrypted_payload, key_id, nonce, aad_hash)
VALUES ($1, $2, 'openai', 'codex_agent_identity', '\x00'::bytea, 'test-key', '\x00'::bytea, 'test-aad')`, tenantID, accountID); err != nil {
		t.Fatalf("写入 Agent Identity 凭据: %v", err)
	}
}
