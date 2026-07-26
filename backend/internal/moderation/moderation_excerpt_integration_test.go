//go:build integration_pg

package moderation

import (
	"context"
	"strings"
	"testing"

	dbmoderation "github.com/BloomingProsperity/HUAKAI/internal/db/moderation"
)

// AT-08：摘录列在真库上的完整往返。变异：迁移里漏掉 input_excerpt 列，或
// sql_store 忘记传参时，读回的摘录为空，本用例转红。
func TestModerationExcerpt_真库往返(t *testing.T) {
	ctx := context.Background()
	pool := openModerationIntegrationPool(t, ctx)
	store := NewSQLStore(dbmoderation.New(pool))
	seed := seedModerationAPIKey(t, ctx, pool, "excerpt-roundtrip", "active")

	const excerpt = "帮我写一段关于水质检测的说明"
	event := ModerationEvent{
		TenantID:     seed.tenantID,
		APIKeyID:     seed.apiKeyID,
		UserID:       seed.userID,
		RequestID:    "req-excerpt-roundtrip",
		PayloadHash:  "hash-excerpt-roundtrip",
		Decision:     DecisionBlockKeyword,
		ReasonCode:   "keyword_match",
		InputExcerpt: excerpt,
	}

	if _, err := store.InsertModerationLog(ctx, event); err != nil {
		t.Fatalf("写入审核日志失败: %v", err)
	}
	if err := store.RecordModerationViolationEvent(ctx, event); err != nil {
		t.Fatalf("写入违规事件失败: %v", err)
	}

	logs, err := store.ListModerationLogs(ctx, seed.tenantID, nil, 10, 0)
	if err != nil {
		t.Fatalf("读取审核日志失败: %v", err)
	}
	if len(logs) != 1 {
		t.Fatalf("日志条数=%d want 1", len(logs))
	}
	if logs[0].InputExcerpt != excerpt {
		t.Fatalf("审核日志摘录=%q want %q", logs[0].InputExcerpt, excerpt)
	}

	var storedExcerpt string
	if err := pool.QueryRow(ctx,
		`SELECT input_excerpt FROM moderation_violation_events WHERE tenant_id=$1 AND api_key_id=$2`,
		seed.tenantID, seed.apiKeyID,
	).Scan(&storedExcerpt); err != nil {
		t.Fatalf("读取违规事件摘录失败: %v", err)
	}
	if storedExcerpt != excerpt {
		t.Fatalf("违规事件摘录=%q want %q", storedExcerpt, excerpt)
	}
}

// 摘录列必须有 NOT NULL DEFAULT ''：旧代码路径不传该字段时不得写入失败。
// 变异：迁移里去掉 DEFAULT '' 时转红。
func TestModerationExcerpt_未提供摘录时默认空串(t *testing.T) {
	ctx := context.Background()
	pool := openModerationIntegrationPool(t, ctx)
	seed := seedModerationAPIKey(t, ctx, pool, "excerpt-default", "active")

	if _, err := pool.Exec(ctx,
		`INSERT INTO moderation_violation_events
		   (tenant_id, api_key_id, user_id, payload_hash, decision, reason_code)
		 VALUES ($1, $2, $3, 'hash-default', 'block_keyword', 'keyword_match')`,
		seed.tenantID, seed.apiKeyID, seed.userID,
	); err != nil {
		t.Fatalf("不带摘录列插入失败(缺少 DEFAULT?): %v", err)
	}

	var got string
	if err := pool.QueryRow(ctx,
		`SELECT input_excerpt FROM moderation_violation_events WHERE tenant_id=$1`,
		seed.tenantID,
	).Scan(&got); err != nil {
		t.Fatalf("读取默认摘录失败: %v", err)
	}
	if got != "" {
		t.Fatalf("默认摘录=%q want 空串", got)
	}
}

// AT-08 配套：停用开关列的真库默认值必须是「需要人工确认」。
// 变异：迁移把 DEFAULT 写成 true 时转红。
func TestModerationConfig_停用开关默认需人工确认(t *testing.T) {
	ctx := context.Background()
	pool := openModerationIntegrationPool(t, ctx)
	seed := seedModerationAPIKey(t, ctx, pool, "ban-switch-default", "active")

	if _, err := pool.Exec(ctx,
		`INSERT INTO moderation_config (tenant_id) VALUES ($1)`,
		seed.tenantID,
	); err != nil {
		t.Fatalf("插入默认审核配置失败: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM moderation_config WHERE tenant_id=$1`, seed.tenantID)
	})

	store := NewSQLStore(dbmoderation.New(pool))
	cfg, err := store.GetConfig(ctx, seed.tenantID)
	if err != nil {
		t.Fatalf("读取审核配置失败: %v", err)
	}
	if cfg.AutoDisableKeyOnBan {
		t.Fatalf("AutoDisableKeyOnBan=true want false，默认必须是达阈值不自动停用")
	}
}

// 开关经配置写入后必须真的持久化并读回，否则运营在管理端改了等于没改。
// 变异：sql_store 的 Upsert 漏传该字段时转红。
func TestModerationConfig_停用开关往返持久化(t *testing.T) {
	ctx := context.Background()
	pool := openModerationIntegrationPool(t, ctx)
	seed := seedModerationAPIKey(t, ctx, pool, "ban-switch-roundtrip", "active")
	store := NewSQLStore(dbmoderation.New(pool))
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM moderation_config WHERE tenant_id=$1`, seed.tenantID)
	})

	saved, err := store.UpsertConfig(ctx, ModerationConfig{
		TenantID:            seed.tenantID,
		Enabled:             true,
		FailClosed:          true,
		SampleRatePct:       100,
		BanThreshold:        3,
		BanWindowSeconds:    3600,
		AutoDisableKeyOnBan: true,
		UpdatedBy:           "integration-test",
	})
	if err != nil {
		t.Fatalf("写入审核配置失败: %v", err)
	}
	if !saved.AutoDisableKeyOnBan {
		t.Fatalf("写入返回的 AutoDisableKeyOnBan=false want true")
	}

	reloaded, err := store.GetConfig(ctx, seed.tenantID)
	if err != nil {
		t.Fatalf("重新读取审核配置失败: %v", err)
	}
	if !reloaded.AutoDisableKeyOnBan {
		t.Fatalf("重新读取的 AutoDisableKeyOnBan=false want true，开关未持久化")
	}
}

// 摘录列不得承载完整请求体：本用例锁住写入侧真的经过截断，防止有人把
// BuildExcerpt 绕过、直接把 body 塞进事件。
func TestModerationExcerpt_落库长度受上限约束(t *testing.T) {
	ctx := context.Background()
	pool := openModerationIntegrationPool(t, ctx)
	store := NewSQLStore(dbmoderation.New(pool))
	seed := seedModerationAPIKey(t, ctx, pool, "excerpt-length", "active")

	body := []byte(`{"messages":[{"role":"user","content":"` + strings.Repeat("超", 5000) + `"}]}`)
	event := ModerationEvent{
		TenantID:     seed.tenantID,
		APIKeyID:     seed.apiKeyID,
		UserID:       seed.userID,
		PayloadHash:  "hash-excerpt-length",
		Decision:     DecisionBlockKeyword,
		ReasonCode:   "keyword_match",
		InputExcerpt: BuildExcerpt(body, DefaultExcerptMaxRunes),
	}
	if err := store.RecordModerationViolationEvent(ctx, event); err != nil {
		t.Fatalf("写入违规事件失败: %v", err)
	}

	var got string
	if err := pool.QueryRow(ctx,
		`SELECT input_excerpt FROM moderation_violation_events WHERE tenant_id=$1`,
		seed.tenantID,
	).Scan(&got); err != nil {
		t.Fatalf("读取摘录失败: %v", err)
	}
	if n := len([]rune(got)); n != DefaultExcerptMaxRunes {
		t.Fatalf("落库摘录字符数=%d want %d", n, DefaultExcerptMaxRunes)
	}
}
