//go:build integration_pg

package dbmigrate_test

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func TestOperationalContractMigrationsRoundTripWithoutBusinessFacts(t *testing.T) {
	baseDSN := os.Getenv("HUAKAI_DATABASE_URL")
	if baseDSN == "" {
		t.Skip("HUAKAI_DATABASE_URL 未设置，跳过 integration_pg")
	}
	ctx := context.Background()
	dsn := createTemporaryMigrationDatabase(t, ctx, baseDSN, "huakai_operational_migrations")
	runner := newEmbeddedMigrationRunner(t, dsn)
	if err := runner.Migrate(220); err != nil {
		t.Fatalf("迁移到 0220: %v", err)
	}

	for version := uint(221); version <= 231; version++ {
		if err := runner.Steps(1); err != nil {
			t.Fatalf("应用 %04d 上迁移: %v", version, err)
		}
		if err := runner.Steps(-1); err != nil {
			t.Fatalf("无业务事实时应用 %04d 下迁移: %v", version, err)
		}
		if err := runner.Steps(1); err != nil {
			t.Fatalf("回退后重新应用 %04d 上迁移: %v", version, err)
		}
	}
}

func TestManualReplayActorMigrationRefusesFactLoss(t *testing.T) {
	baseDSN := os.Getenv("HUAKAI_DATABASE_URL")
	if baseDSN == "" {
		t.Skip("HUAKAI_DATABASE_URL 未设置，跳过 integration_pg")
	}
	ctx := context.Background()
	dsn := createTemporaryMigrationDatabase(t, ctx, baseDSN, "huakai_manual_replay_guard")
	runner := newEmbeddedMigrationRunner(t, dsn)
	if err := runner.Migrate(221); err != nil {
		t.Fatalf("迁移到 0221: %v", err)
	}
	conn := connectMigrationDatabase(t, ctx, dsn)
	defer conn.Close(ctx)

	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	var tenantID int64
	if err := conn.QueryRow(ctx,
		`INSERT INTO tenants (name) VALUES ($1) RETURNING id`,
		"replay-guard-"+suffix,
	).Scan(&tenantID); err != nil {
		t.Fatalf("插入人工重放迁移租户: %v", err)
	}
	outboxID := "replay-outbox-" + suffix
	if _, err := conn.Exec(ctx, `
INSERT INTO outbox_events (id, tenant_id, event_type, priority, payload, status)
VALUES ($1, $2, 'migration.guard', 'default', '{}'::jsonb, 'failed_dead')`,
		outboxID, tenantID,
	); err != nil {
		t.Fatalf("插入人工重放 outbox: %v", err)
	}
	if _, err := conn.Exec(ctx, `
INSERT INTO dlq_events (
    id, outbox_event_id, tenant_id, payload, dead_reason,
    last_replay_at, last_replay_actor
) VALUES ($1, $2, $3, '{}'::jsonb, '迁移保护', now(), 'admin_token:305')`,
		"replay-dlq-"+suffix, outboxID, tenantID,
	); err != nil {
		t.Fatalf("插入人工重放日志: %v", err)
	}

	if err := runner.Steps(-1); err == nil {
		t.Fatal("存在人工重放操作者日志时 0221 回退意外成功")
	}
	var actor string
	if err := conn.QueryRow(ctx,
		`SELECT last_replay_actor FROM dlq_events WHERE outbox_event_id=$1`,
		outboxID,
	).Scan(&actor); err != nil {
		t.Fatalf("拒绝回退后读取人工重放操作者: %v", err)
	}
	if actor != "admin_token:305" {
		t.Fatalf("拒绝回退后操作者=%q want admin_token:305", actor)
	}
}

func TestAdminOperationLogMigrationsRefuseFactLoss(t *testing.T) {
	baseDSN := os.Getenv("HUAKAI_DATABASE_URL")
	if baseDSN == "" {
		t.Skip("HUAKAI_DATABASE_URL 未设置，跳过 integration_pg")
	}
	ctx := context.Background()
	cases := []struct {
		name       string
		version    uint
		action     string
		targetType string
	}{
		{
			name:       "社交身份解绑",
			version:    222,
			action:     "unlink_social_identity",
			targetType: "user",
		},
		{
			name:       "账号池删除",
			version:    223,
			action:     "delete_pool_group",
			targetType: "pool_group",
		},
		{
			name:       "账号真实探测",
			version:    224,
			action:     "test_provider_account",
			targetType: "provider_account",
		},
		{
			name:       "模型能力绑定",
			version:    225,
			action:     "update_model_capability_binding",
			targetType: "model_capability_binding",
		},
		{
			name:       "用户通知设置",
			version:    226,
			action:     "update_user_notification_settings",
			targetType: "user_notification_settings",
		},
		{
			name:       "分组路由创建",
			version:    228,
			action:     "create_route_rule",
			targetType: "route_rule",
		},
	}

	for _, testCase := range cases {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			dsn := createTemporaryMigrationDatabase(
				t,
				ctx,
				baseDSN,
				fmt.Sprintf("huakai_operation_log_%04d", testCase.version),
			)
			runner := newEmbeddedMigrationRunner(t, dsn)
			if err := runner.Migrate(testCase.version); err != nil {
				t.Fatalf("迁移到 %04d: %v", testCase.version, err)
			}
			conn := connectMigrationDatabase(t, ctx, dsn)
			defer conn.Close(ctx)

			suffix := fmt.Sprintf("%d", time.Now().UnixNano())
			var tenantID int64
			if err := conn.QueryRow(ctx,
				`INSERT INTO tenants (name) VALUES ($1) RETURNING id`,
				fmt.Sprintf("operation-log-%04d-%s", testCase.version, suffix),
			).Scan(&tenantID); err != nil {
				t.Fatalf("插入 %04d 日志迁移租户: %v", testCase.version, err)
			}
			if _, err := conn.Exec(ctx, `
INSERT INTO admin_audit_events (
    tenant_id, actor_id, actor_role, action, target_type, target_id, reason
) VALUES ($1, 'admin_token:305', 'platform_admin', $2, $3, 31, '迁移保护')`,
				tenantID, testCase.action, testCase.targetType,
			); err != nil {
				t.Fatalf("插入 %04d 受保护日志: %v", testCase.version, err)
			}

			if err := runner.Steps(-1); err == nil {
				t.Fatalf("存在 %s 日志时 %04d 回退意外成功", testCase.name, testCase.version)
			}
			var count int
			if err := conn.QueryRow(ctx, `
SELECT count(*)
FROM admin_audit_events
WHERE tenant_id=$1 AND action=$2 AND target_type=$3`,
				tenantID, testCase.action, testCase.targetType,
			).Scan(&count); err != nil {
				t.Fatalf("拒绝回退后读取 %04d 日志: %v", testCase.version, err)
			}
			if count != 1 {
				t.Fatalf("拒绝回退后 %04d 日志数=%d want 1", testCase.version, count)
			}
		})
	}
}

func TestSettlementRecoveryPayloadMigrationRefusesFactLoss(t *testing.T) {
	baseDSN := os.Getenv("HUAKAI_DATABASE_URL")
	if baseDSN == "" {
		t.Skip("HUAKAI_DATABASE_URL 未设置，跳过 integration_pg")
	}
	ctx := context.Background()
	dsn := createTemporaryMigrationDatabase(t, ctx, baseDSN, "huakai_settlement_recovery_guard")
	runner := newEmbeddedMigrationRunner(t, dsn)
	if err := runner.Migrate(227); err != nil {
		t.Fatalf("迁移到 0227: %v", err)
	}
	conn := connectMigrationDatabase(t, ctx, dsn)
	defer conn.Close(ctx)

	tenantID, _, apiKeyID, claimID := seedMigrationBillingClaim(t, ctx, conn, "settlement-recovery")
	requestID := "settlement-recovery-" + uuid.NewString()
	if _, err := conn.Exec(ctx, `
INSERT INTO settlement_intents (
    tenant_id, request_id, logical_request_id, attempt_seq, claim_id,
    api_key_id, request_fingerprint, status, recovery_payload,
    recovery_failure_class
) VALUES (
    $1,$2,$2,1,$3,$4,$5,'failed',
    '{"claim_id":31,"actual_cost":"0.25"}'::jsonb,
    'settlement_and_enqueue_failed'
)`,
		tenantID, requestID, claimID, apiKeyID, "fingerprint-"+requestID,
	); err != nil {
		t.Fatalf("插入结算恢复载荷: %v", err)
	}

	if err := runner.Steps(-1); err == nil {
		t.Fatal("存在结算恢复载荷时 0227 回退意外成功")
	}
	var payload string
	var failureClass string
	if err := conn.QueryRow(ctx, `
SELECT recovery_payload::text, recovery_failure_class
FROM settlement_intents
WHERE tenant_id=$1 AND request_id=$2`,
		tenantID, requestID,
	).Scan(&payload, &failureClass); err != nil {
		t.Fatalf("拒绝回退后读取结算恢复载荷: %v", err)
	}
	if payload == "" || failureClass != "settlement_and_enqueue_failed" {
		t.Fatalf("拒绝回退后恢复载荷/分类=%q/%q", payload, failureClass)
	}
}

func TestExternalMediaRelayUsageMigrationRefusesFactLoss(t *testing.T) {
	baseDSN := os.Getenv("HUAKAI_DATABASE_URL")
	if baseDSN == "" {
		t.Skip("HUAKAI_DATABASE_URL 未设置，跳过 integration_pg")
	}
	ctx := context.Background()
	dsn := createTemporaryMigrationDatabase(t, ctx, baseDSN, "huakai_external_media_usage_guard")
	runner := newEmbeddedMigrationRunner(t, dsn)
	if err := runner.Migrate(230); err != nil {
		t.Fatalf("迁移到 0230: %v", err)
	}
	conn := connectMigrationDatabase(t, ctx, dsn)
	defer conn.Close(ctx)

	tenantID, userID, apiKeyID, claimID := seedMigrationBillingClaim(t, ctx, conn, "external-media")
	var usageID int64
	if err := conn.QueryRow(ctx, `
INSERT INTO usage_records (
    tenant_id, claim_id, api_key_id, user_id,
    provider_account_id, acquisition_token, settlement_source,
    attempt_seq, actual_cost, end_class, usage_source,
    routing_reason, requested_at, requested_model,
    stream, stream_state
) VALUES (
    $1,$2,$3,$4,NULL,NULL,'external_media_relay',
    1,0.25,'non_streaming','reported',
    '{}'::jsonb,now(),'video_generate',false,2
)
RETURNING id`,
		tenantID, claimID, apiKeyID, userID,
	).Scan(&usageID); err != nil {
		t.Fatalf("插入外部媒体中继用量: %v", err)
	}

	if err := runner.Steps(-1); err == nil {
		t.Fatal("存在外部媒体中继用量时 0230 回退意外成功")
	}
	var source string
	if err := conn.QueryRow(ctx,
		`SELECT settlement_source FROM usage_records WHERE id=$1`,
		usageID,
	).Scan(&source); err != nil {
		t.Fatalf("拒绝回退后读取外部媒体中继用量: %v", err)
	}
	if source != "external_media_relay" {
		t.Fatalf("拒绝回退后结算来源=%q want external_media_relay", source)
	}
}

func TestMediaSubmissionRecoveryMigrationRefusesFactLoss(t *testing.T) {
	baseDSN := os.Getenv("HUAKAI_DATABASE_URL")
	if baseDSN == "" {
		t.Skip("HUAKAI_DATABASE_URL 未设置，跳过 integration_pg")
	}
	ctx := context.Background()
	dsn := createTemporaryMigrationDatabase(t, ctx, baseDSN, "huakai_media_submission_guard")
	runner := newEmbeddedMigrationRunner(t, dsn)
	if err := runner.Migrate(232); err != nil {
		t.Fatalf("迁移到 0232: %v", err)
	}
	conn := connectMigrationDatabase(t, ctx, dsn)
	defer conn.Close(ctx)

	taskIDs := seedMediaMigrationTasks(t, ctx, conn, []string{"submission-unknown-guard"})
	if _, err := conn.Exec(ctx,
		`UPDATE media_tasks SET status='submission_unknown' WHERE id=$1`,
		taskIDs[0],
	); err != nil {
		t.Fatalf("写入提交结果未知恢复状态: %v", err)
	}

	if err := runner.Steps(-1); err == nil {
		t.Fatal("存在提交结果未知恢复状态时 0232 回退意外成功")
	}
	var status string
	if err := conn.QueryRow(ctx,
		`SELECT status FROM media_tasks WHERE id=$1`,
		taskIDs[0],
	).Scan(&status); err != nil {
		t.Fatalf("拒绝回退后读取媒体恢复状态: %v", err)
	}
	if status != "submission_unknown" {
		t.Fatalf("拒绝回退后媒体状态=%q want submission_unknown", status)
	}
}

func TestSessionAuthVersionMigrationBackfillsAndRefusesSecurityDowngrade(t *testing.T) {
	baseDSN := os.Getenv("HUAKAI_DATABASE_URL")
	if baseDSN == "" {
		t.Skip("HUAKAI_DATABASE_URL 未设置，跳过 integration_pg")
	}
	ctx := context.Background()
	dsn := createTemporaryMigrationDatabase(t, ctx, baseDSN, "huakai_session_auth_guard")
	runner := newEmbeddedMigrationRunner(t, dsn)
	if err := runner.Migrate(228); err != nil {
		t.Fatalf("迁移到 0228: %v", err)
	}
	conn := connectMigrationDatabase(t, ctx, dsn)
	defer conn.Close(ctx)

	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	var tenantID, userID int64
	if err := conn.QueryRow(ctx,
		`INSERT INTO tenants (name) VALUES ($1) RETURNING id`,
		"session-auth-"+suffix,
	).Scan(&tenantID); err != nil {
		t.Fatalf("插入会话迁移租户: %v", err)
	}
	if err := conn.QueryRow(ctx, `
INSERT INTO users (tenant_id, display_name, password_version)
VALUES ($1, $2, 7)
RETURNING id`, tenantID, "session-auth-user-"+suffix).Scan(&userID); err != nil {
		t.Fatalf("插入会话迁移用户: %v", err)
	}
	familyID := uuid.New()
	if _, err := conn.Exec(ctx, `
INSERT INTO session_families (id, tenant_id, user_id)
VALUES ($1, $2, $3)`, familyID, tenantID, userID); err != nil {
		t.Fatalf("插入存量会话: %v", err)
	}

	if err := runner.Migrate(229); err != nil {
		t.Fatalf("带存量会话升级 0229: %v", err)
	}
	var authVersion int
	if err := conn.QueryRow(ctx,
		`SELECT auth_version FROM session_families WHERE id=$1`,
		familyID,
	).Scan(&authVersion); err != nil {
		t.Fatalf("读取迁移后认证版本: %v", err)
	}
	if authVersion != 7 {
		t.Fatalf("迁移后认证版本=%d want 7", authVersion)
	}
	if err := runner.Steps(-1); err == nil {
		t.Fatal("仍有认证版本绑定会话时 0229 回退意外成功")
	}
	if err := conn.QueryRow(ctx,
		`SELECT auth_version FROM session_families WHERE id=$1`,
		familyID,
	).Scan(&authVersion); err != nil {
		t.Fatalf("拒绝回退后读取认证版本: %v", err)
	}
	if authVersion != 7 {
		t.Fatalf("拒绝回退后认证版本=%d want 7", authVersion)
	}
}

func TestOperationalLogMigrationRefusesNewRecoveryFactLoss(t *testing.T) {
	baseDSN := os.Getenv("HUAKAI_DATABASE_URL")
	if baseDSN == "" {
		t.Skip("HUAKAI_DATABASE_URL 未设置，跳过 integration_pg")
	}
	ctx := context.Background()
	dsn := createTemporaryMigrationDatabase(t, ctx, baseDSN, "huakai_operational_log_guard")
	runner := newEmbeddedMigrationRunner(t, dsn)
	if err := runner.Migrate(231); err != nil {
		t.Fatalf("迁移到 0231: %v", err)
	}
	conn := connectMigrationDatabase(t, ctx, dsn)
	defer conn.Close(ctx)

	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	var tenantID int64
	if err := conn.QueryRow(ctx,
		`INSERT INTO tenants (name) VALUES ($1) RETURNING id`,
		"operation-log-"+suffix,
	).Scan(&tenantID); err != nil {
		t.Fatalf("插入运营日志迁移租户: %v", err)
	}
	if _, err := conn.Exec(ctx, `
INSERT INTO admin_audit_events (
    tenant_id, actor_id, actor_role, action, target_type, target_id, reason
) VALUES (
    $1, 'admin_token:305', 'platform_admin',
    'orphan_provider_task_attached', 'media_task_orphan', 31, '迁移保护'
)`, tenantID); err != nil {
		t.Fatalf("插入媒体恢复操作日志: %v", err)
	}

	if err := runner.Steps(-1); err == nil {
		t.Fatal("存在新增媒体恢复操作日志时 0231 回退意外成功")
	}
	var count int
	if err := conn.QueryRow(ctx, `
SELECT count(*)
FROM admin_audit_events
WHERE tenant_id=$1 AND action='orphan_provider_task_attached'`, tenantID).Scan(&count); err != nil {
		t.Fatalf("拒绝回退后读取媒体恢复日志: %v", err)
	}
	if count != 1 {
		t.Fatalf("拒绝回退后媒体恢复日志数=%d want 1", count)
	}
}

func seedMigrationBillingClaim(
	t *testing.T,
	ctx context.Context,
	conn *pgx.Conn,
	prefix string,
) (tenantID, userID, apiKeyID, claimID int64) {
	t.Helper()
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	if err := conn.QueryRow(ctx,
		`INSERT INTO tenants (name) VALUES ($1) RETURNING id`,
		prefix+"-tenant-"+suffix,
	).Scan(&tenantID); err != nil {
		t.Fatalf("插入 %s 迁移租户: %v", prefix, err)
	}
	if err := conn.QueryRow(ctx, `
INSERT INTO users (tenant_id, display_name)
VALUES ($1,$2)
RETURNING id`, tenantID, prefix+"-user-"+suffix).Scan(&userID); err != nil {
		t.Fatalf("插入 %s 迁移用户: %v", prefix, err)
	}
	if err := conn.QueryRow(ctx, `
INSERT INTO api_keys (tenant_id, user_id, name, key_hash, key_prefix, status)
VALUES ($1,$2,$3,$4,$5,'active')
RETURNING id`,
		tenantID,
		userID,
		prefix+"-key-"+suffix,
		"$2a$10$migration-contract-fixture",
		"hk_"+prefix+"_"+suffix,
	).Scan(&apiKeyID); err != nil {
		t.Fatalf("插入 %s 迁移 API Key: %v", prefix, err)
	}
	if err := conn.QueryRow(ctx, `
INSERT INTO billing_ledger_claims (
    tenant_id, idempotency_key, request_fingerprint, api_key_id, user_id,
    logical_request_id, endpoint_family, requested_model,
    billing_policy_version, request_class, attempt_seq, predicted_cost,
    currency_code, lease_expires_at
) VALUES (
    $1,$2,$3,$4,$5,$6,'media','migration-model',
    '1.0','standard',1,0.25,'USD',now() + interval '1 minute'
)
RETURNING id`,
		tenantID,
		prefix+"-claim-"+suffix,
		prefix+"-fingerprint-"+suffix,
		apiKeyID,
		userID,
		prefix+"-logical-"+suffix,
	).Scan(&claimID); err != nil {
		t.Fatalf("插入 %s 迁移 claim: %v", prefix, err)
	}
	return tenantID, userID, apiKeyID, claimID
}

func connectMigrationDatabase(t *testing.T, ctx context.Context, dsn string) *pgx.Conn {
	t.Helper()
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("连接迁移临时库: %v", err)
	}
	return conn
}
