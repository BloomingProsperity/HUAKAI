//go:build integration_pg

package moderation

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	dbmoderation "github.com/BloomingProsperity/HUAKAI/internal/db/moderation"
)

// TestModerationLogAcceptsBlockExternal 咬住外部审核结论的审计落库约束。
// 变异：恢复 0082 的旧白名单后，block_external INSERT 会违反 CHECK，本测试变红。
func TestModerationLogAcceptsBlockExternal(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	tx, key := beginModerationDecisionCheckTest(t, ctx, "log-external")

	if _, err := tx.Exec(ctx, `
		INSERT INTO moderation_log (
			tenant_id, api_key_id, user_id, request_id,
			payload_hash, decision, reason_code
		) VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		key.tenantID,
		key.apiKeyID,
		key.userID,
		"decision-check-log-external",
		"hash-log-external",
		string(DecisionBlockExternal),
		"external_match",
	); err != nil {
		t.Fatalf("写入 block_external moderation_log: %v", err)
	}
}

// TestBanCounterBlockExternalPersistsAndDisables 咬住外部审核事件从写入、计数到封号的真链。
// 变异：恢复 0090 的旧白名单后，违规事件 INSERT 会失败，Disabled 不会成为 true。
func TestBanCounterBlockExternalPersistsAndDisables(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	tx, key := beginModerationDecisionCheckTest(t, ctx, "ban-external")
	store := NewSQLStore(dbmoderation.New(tx))
	counter := NewBanCounter(store)

	result, err := counter.RecordAndCheck(ctx, ModerationEvent{
		TenantID:    key.tenantID,
		APIKeyID:    key.apiKeyID,
		UserID:      key.userID,
		RequestID:   "decision-check-ban-external",
		PayloadHash: "hash-ban-external",
		Decision:    DecisionBlockExternal,
		ReasonCode:  "external_match",
	}, ModerationConfig{
		TenantID:         key.tenantID,
		BanThreshold:     1,
		BanWindowSeconds: 3600,
	})
	if err != nil {
		t.Fatalf("RecordAndCheck block_external: %v", err)
	}
	if !result.Disabled || result.Count != 1 {
		t.Fatalf("RecordAndCheck 结果=%+v，期望 Disabled=true 且 Count=1", result)
	}

	var status string
	if err := tx.QueryRow(ctx,
		`SELECT status FROM api_keys WHERE tenant_id=$1 AND id=$2`,
		key.tenantID,
		key.apiKeyID,
	).Scan(&status); err != nil {
		t.Fatalf("查询封号后的 API key: %v", err)
	}
	if status != "disabled" {
		t.Fatalf("API key status=%q，期望 disabled", status)
	}
}

// TestModerationViolationEventsAcceptsBlockBackend 咬住后端审核结论的违规事件白名单。
// 变异：恢复 0090 的旧白名单后，block_backend INSERT 会违反 CHECK，本测试变红。
func TestModerationViolationEventsAcceptsBlockBackend(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	tx, key := beginModerationDecisionCheckTest(t, ctx, "violation-backend")

	if _, err := tx.Exec(ctx, `
		INSERT INTO moderation_violation_events (
			tenant_id, api_key_id, user_id, request_id,
			payload_hash, decision, reason_code
		) VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		key.tenantID,
		key.apiKeyID,
		key.userID,
		"decision-check-violation-backend",
		"hash-violation-backend",
		string(DecisionBlockBackend),
		"backend_unavailable",
	); err != nil {
		t.Fatalf("写入 block_backend moderation_violation_events: %v", err)
	}
}

func beginModerationDecisionCheckTest(t *testing.T, ctx context.Context, suffix string) (pgx.Tx, moderationAPIKeySeed) {
	t.Helper()
	pool := openModerationIntegrationPool(t, ctx)
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("开启 decision CHECK 集成测试事务: %v", err)
	}
	t.Cleanup(func() {
		_ = tx.Rollback(context.Background())
	})

	unique := fmt.Sprintf("decision-check-%s-%d", suffix, time.Now().UnixNano())
	var key moderationAPIKeySeed
	if err := tx.QueryRow(ctx,
		`INSERT INTO tenants (name) VALUES ($1) RETURNING id`,
		"tenant-"+unique,
	).Scan(&key.tenantID); err != nil {
		t.Fatalf("插入 decision CHECK 测试租户: %v", err)
	}
	if err := tx.QueryRow(ctx,
		`INSERT INTO users (tenant_id, display_name) VALUES ($1, $2) RETURNING id`,
		key.tenantID,
		"user-"+unique,
	).Scan(&key.userID); err != nil {
		t.Fatalf("插入 decision CHECK 测试用户: %v", err)
	}
	if err := tx.QueryRow(ctx, `
		INSERT INTO api_keys (
			tenant_id, user_id, name, key_hash, key_prefix, status
		) VALUES ($1, $2, $3, $4, $5, 'active')
		RETURNING id`,
		key.tenantID,
		key.userID,
		"key-"+unique,
		"$2a$10$decision-check-placeholder",
		"hk_test_"+unique,
	).Scan(&key.apiKeyID); err != nil {
		t.Fatalf("插入 decision CHECK 测试 API key: %v", err)
	}
	return tx, key
}
