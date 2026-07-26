//go:build integration_pg

package dbmigrate_test

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

func TestModerationOperatorEvidenceMigrationPreservesValidRequestIdentity(t *testing.T) {
	baseDSN := os.Getenv("HUAKAI_DATABASE_URL")
	if baseDSN == "" {
		t.Skip("HUAKAI_DATABASE_URL not set; skipping integration_pg")
	}
	ctx := context.Background()
	tmpDSN := createTemporaryMigrationDatabase(t, ctx, baseDSN, "huakai_moderation_0234_test")
	runner := newEmbeddedMigrationRunner(t, tmpDSN)
	if err := runner.Migrate(233); err != nil {
		t.Fatalf("迁移到 233: %v", err)
	}
	conn, err := pgx.Connect(ctx, tmpDSN)
	if err != nil {
		t.Fatalf("连接临时库: %v", err)
	}
	defer conn.Close(ctx)

	if err := runner.Migrate(234); err != nil {
		t.Fatalf("空库应用 0234: %v", err)
	}
	if err := runner.Migrate(233); err != nil {
		t.Fatalf("0234 无永久事实时应可回滚: %v", err)
	}

	seed := seedModerationMigrationRows(t, ctx, conn)
	if err := runner.Migrate(234); err != nil {
		t.Fatalf("带旧事件应用 0234: %v", err)
	}

	var preservedRequestID, duplicateRequestID, emptyRequestID string
	if err := conn.QueryRow(ctx, `
SELECT
    (SELECT request_id FROM moderation_violation_events WHERE id=$1),
    (SELECT request_id FROM moderation_violation_events WHERE id=$2),
    (SELECT request_id FROM moderation_violation_events WHERE id=$3)`,
		seed.preservedID, seed.duplicateID, seed.emptyID,
	).Scan(&preservedRequestID, &duplicateRequestID, &emptyRequestID); err != nil {
		t.Fatalf("读取迁移后请求身份: %v", err)
	}
	if preservedRequestID != "client-request-stable" {
		t.Fatalf("有效请求身份被改写为 %q", preservedRequestID)
	}
	if duplicateRequestID != fmt.Sprintf("legacy:m234:duplicate:%d", seed.duplicateID) {
		t.Fatalf("重复请求身份=%q，期望按行修复", duplicateRequestID)
	}
	if emptyRequestID != fmt.Sprintf("legacy:m234:empty:%d", seed.emptyID) {
		t.Fatalf("空请求身份=%q，期望按行修复", emptyRequestID)
	}
	var invalidCount int64
	if err := conn.QueryRow(ctx,
		`SELECT count(*) FROM moderation_violation_events WHERE id=$1`,
		seed.invalidID,
	).Scan(&invalidCount); err != nil {
		t.Fatalf("检查坏关联行: %v", err)
	}
	if invalidCount != 0 {
		t.Fatalf("跨租户坏关联仍有 %d 行", invalidCount)
	}
	var operationTable *string
	if err := conn.QueryRow(ctx,
		`SELECT to_regclass('public.moderation_key_operations')::text`,
	).Scan(&operationTable); err != nil {
		t.Fatalf("检查永久幂等表: %v", err)
	}
	if operationTable == nil {
		t.Fatal("0234 未创建 moderation_key_operations")
	}

	if err := runner.Migrate(233); err == nil {
		t.Fatal("存在永久违规事实时 0234 降级应被拒绝")
	}
	var preservedCount int64
	if err := conn.QueryRow(ctx, `
SELECT count(*) FROM moderation_violation_events
WHERE tenant_id=$1 AND id=$2 AND request_id='client-request-stable'`,
		seed.tenantID, seed.preservedID,
	).Scan(&preservedCount); err != nil {
		t.Fatalf("降级拒绝后读取永久违规事实: %v", err)
	}
	if preservedCount != 1 {
		t.Fatalf("降级拒绝后永久违规事实=%d，期望 1", preservedCount)
	}
}

type moderationMigrationSeed struct {
	preservedID int64
	duplicateID int64
	emptyID     int64
	invalidID   int64
	tenantID    int64
}

func seedModerationMigrationRows(
	t *testing.T,
	ctx context.Context,
	conn *pgx.Conn,
) moderationMigrationSeed {
	t.Helper()
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	var tenantA, tenantB, userA, userB, keyA, keyB int64
	if err := conn.QueryRow(ctx,
		`INSERT INTO tenants (name) VALUES ($1) RETURNING id`,
		"moderation-migration-a-"+suffix,
	).Scan(&tenantA); err != nil {
		t.Fatalf("插入租户 A: %v", err)
	}
	if err := conn.QueryRow(ctx,
		`INSERT INTO tenants (name) VALUES ($1) RETURNING id`,
		"moderation-migration-b-"+suffix,
	).Scan(&tenantB); err != nil {
		t.Fatalf("插入租户 B: %v", err)
	}
	if err := conn.QueryRow(ctx,
		`INSERT INTO users (tenant_id, display_name) VALUES ($1, $2) RETURNING id`,
		tenantA, "user-a-"+suffix,
	).Scan(&userA); err != nil {
		t.Fatalf("插入用户 A: %v", err)
	}
	if err := conn.QueryRow(ctx,
		`INSERT INTO users (tenant_id, display_name) VALUES ($1, $2) RETURNING id`,
		tenantB, "user-b-"+suffix,
	).Scan(&userB); err != nil {
		t.Fatalf("插入用户 B: %v", err)
	}
	if err := conn.QueryRow(ctx, `
INSERT INTO api_keys (tenant_id, user_id, name, key_hash, key_prefix, status)
VALUES ($1, $2, $3, $4, $5, 'active') RETURNING id`,
		tenantA, userA, "key-a-"+suffix, "$2a$10$migration-a", "hk_mig_a_"+suffix,
	).Scan(&keyA); err != nil {
		t.Fatalf("插入 Key A: %v", err)
	}
	if err := conn.QueryRow(ctx, `
INSERT INTO api_keys (tenant_id, user_id, name, key_hash, key_prefix, status)
VALUES ($1, $2, $3, $4, $5, 'active') RETURNING id`,
		tenantB, userB, "key-b-"+suffix, "$2a$10$migration-b", "hk_mig_b_"+suffix,
	).Scan(&keyB); err != nil {
		t.Fatalf("插入 Key B: %v", err)
	}

	insertEvent := func(tenantID, apiKeyID, userID int64, requestID *string) int64 {
		t.Helper()
		var id int64
		if err := conn.QueryRow(ctx, `
INSERT INTO moderation_violation_events (
    tenant_id, api_key_id, user_id, request_id, payload_hash, decision, reason_code
) VALUES ($1, $2, $3, $4, $5, 'block_keyword', 'migration_fixture')
RETURNING id`,
			tenantID, apiKeyID, userID, requestID, strings.Repeat("a", 64),
		).Scan(&id); err != nil {
			t.Fatalf("插入旧违规事件: %v", err)
		}
		return id
	}
	stable := "client-request-stable"
	empty := ""
	return moderationMigrationSeed{
		preservedID: insertEvent(tenantA, keyA, userA, &stable),
		duplicateID: insertEvent(tenantA, keyA, userA, &stable),
		emptyID:     insertEvent(tenantA, keyA, userA, &empty),
		invalidID:   insertEvent(tenantA, keyB, userA, &stable),
		tenantID:    tenantA,
	}
}
